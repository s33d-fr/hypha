package integrations

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Google Cloud (§ 49) : instances Compute Engine, en lecture seule ────────────────────────
//
// Compte de service au rôle « Lecteur de Compute » (roles/compute.viewer), clé JSON dans
// credentials_file. Jeton OAuth2 par une assertion JWT signée RS256, puis la liste agrégée des
// instances de chaque projet (toutes zones) : état, type, adresses, publiées en ressources cloud.
// REPAIRING (Google répare l'hôte de l'instance) en critique.

type gcpKey struct {
	Email      string `json:"client_email"`
	PrivateKey string `json:"private_key"`
	TokenURI   string `json:"token_uri"`
	ProjectID  string `json:"project_id"`
}

// gcpAssertion : jeton JWT RS256 du compte de service pour l'échange OAuth2.
func gcpAssertion(k gcpKey, scope string, now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(k.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("clé privée du compte de service illisible")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if rk, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
			parsed = rk
		} else {
			return "", fmt.Errorf("clé privée du compte de service illisible : %v", err)
		}
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("clé privée du compte de service : RSA attendue")
	}
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iss": k.Email, "scope": scope, "aud": k.TokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
	unsigned := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + enc.EncodeToString(sig), nil
}

func newGCP(cfg config.GCPConfig, d Deps) *cloudConn {
	client := &http.Client{Timeout: 30 * time.Second}
	api := strings.TrimRight(firstNonEmpty(cfg.Endpoint, "https://compute.googleapis.com"), "/")
	var mu sync.Mutex
	var tok string
	var exp time.Time
	token := func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if tok != "" && time.Until(exp) > 2*time.Minute {
			return tok, nil
		}
		raw, err := os.ReadFile(cfg.CredentialsFile)
		if err != nil {
			return "", fmt.Errorf("clé du compte de service illisible : %s", cfg.CredentialsFile)
		}
		var k gcpKey
		if err := json.Unmarshal(raw, &k); err != nil || k.Email == "" || k.PrivateKey == "" {
			return "", fmt.Errorf("clé du compte de service invalide : %s", cfg.CredentialsFile)
		}
		k.TokenURI = firstNonEmpty(cfg.TokenEndpoint, k.TokenURI, "https://oauth2.googleapis.com/token")
		jwt, err := gcpAssertion(k, "https://www.googleapis.com/auth/compute.readonly", time.Now())
		if err != nil {
			return "", err
		}
		form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {jwt}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.TokenURI, strings.NewReader(form.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var r struct {
			Token   string  `json:"access_token"`
			Expires float64 `json:"expires_in"`
			Error   string  `json:"error"`
			Desc    string  `json:"error_description"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r)
		if resp.StatusCode != http.StatusOK || r.Token == "" {
			return "", fmt.Errorf("jeton refusé (HTTP %d %s) %s", resp.StatusCode, r.Error, model.Truncate(r.Desc, 200))
		}
		tok, exp = r.Token, time.Now().Add(time.Duration(r.Expires)*time.Second)
		return tok, nil
	}
	c := &cloudConn{id: "gcp", enabled: cfg.Enabled, configured: cfg.CredentialsFile != "" && len(cfg.Projects) > 0,
		missing: "credentials_file et projects sont nécessaires", interval: cfg.Interval.D(), pub: newResourcePublisher(d)}
	c.poll = func(ctx context.Context) vsSnapshot {
		start := time.Now()
		var s vsSnapshot
		t, err := token(ctx)
		if err != nil {
			s.errs = append(s.errs, err.Error())
			s.checks = append(s.checks, ovhCheck{"gcp.can_connect", model.CheckCrit, err.Error(), nil})
			s.at, s.took = time.Now(), time.Since(start)
			return s
		}
		hdr := map[string]string{"Authorization": "Bearer " + t}
		total := 0
		for _, p := range cfg.Projects {
			n, err := gcpProject(ctx, client, api, hdr, p, cfg.Tags, &s)
			total += n
			if err != nil {
				s.errs = append(s.errs, p+" : "+err.Error())
			}
		}
		conn := ovhCheck{"gcp.can_connect", model.CheckOK, fmt.Sprintf("%d instance(s) dans %d projet(s)", total, len(cfg.Projects)), nil}
		if len(s.errs) > 0 {
			conn.status, conn.msg = model.CheckWarn, conn.msg+" ; lecture partielle : "+model.Truncate(strings.Join(s.errs, " ; "), 300)
			if len(s.errs) == len(cfg.Projects) {
				conn.status = model.CheckCrit
			}
		}
		s.checks = append(s.checks, conn)
		s.at, s.took = time.Now(), time.Since(start)
		return s
	}
	return c
}

type gceInstance struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	MachineType string            `json:"machineType"`
	Zone        string            `json:"zone"`
	Labels      map[string]string `json:"labels"`
	LastStart   string            `json:"lastStartTimestamp"`
	Interfaces  []struct {
		IP     string `json:"networkIP"`
		Access []struct {
			NatIP string `json:"natIP"`
		} `json:"accessConfigs"`
	} `json:"networkInterfaces"`
	Scheduling struct {
		Preemptible bool `json:"preemptible"`
	} `json:"scheduling"`
}

func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func gcpProject(ctx context.Context, client *http.Client, api string, hdr map[string]string, project string, extra map[string]string, s *vsSnapshot) (int, error) {
	var all []gceInstance
	pageToken := ""
	for page := 0; page < 50; page++ {
		u := api + "/compute/v1/projects/" + url.PathEscape(project) + "/aggregated/instances?maxResults=500"
		if pageToken != "" {
			u += "&pageToken=" + url.QueryEscape(pageToken)
		}
		var r struct {
			Items map[string]struct {
				Instances []gceInstance `json:"instances"`
			} `json:"items"`
			Next string `json:"nextPageToken"`
		}
		if _, err := getJSON(ctx, client, u, hdr, &r); err != nil {
			return len(all), err
		}
		for _, z := range r.Items {
			all = append(all, z.Instances...)
		}
		if r.Next == "" {
			break
		}
		pageToken = r.Next
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	for _, in := range all {
		zone, mt := lastSegment(in.Zone), lastSegment(in.MachineType)
		tags := map[string]string{"provider": "gcp", "gcp_project": project, "gcp_zone": zone}
		for k, v := range extra {
			tags[k] = v
		}
		r := &cloudResource{key: "gcp:" + project + ":" + in.ID, name: in.Name, tags: tags}
		ip := ""
		for _, nic := range in.Interfaces {
			if nic.IP != "" {
				r.ips = append(r.ips, nic.IP)
				ip = firstNonEmpty(ip, nic.IP)
			}
			for _, a := range nic.Access {
				if a.NatIP != "" {
					r.ips = append(r.ips, a.NatIP)
				}
			}
		}
		r.device = model.Device{Provider: "gcp", Type: "gce_instance", Service: project, DisplayName: in.Name, IP: ip, Region: zone,
			PowerState: in.Status, Model: mt, Vendor: "Google Cloud"}
		r.platform = "Google Compute Engine"
		if t, err := time.Parse(time.RFC3339, in.LastStart); err == nil && in.Status == "RUNNING" {
			r.bootTime = t.UnixMilli()
		}
		kv := []string{"gcp_project", project, "gcp_instance", in.Name}
		st, msg := model.CheckOK, fmt.Sprintf("%s (%s, %s) : %s", in.Name, mt, zone, in.Status)
		switch in.Status {
		case "REPAIRING":
			st, msg = model.CheckCrit, msg+", Google répare l'hôte de l'instance"
		case "RUNNING", "TERMINATED", "STOPPED", "SUSPENDED", "PROVISIONING", "STAGING", "STOPPING", "SUSPENDING":
		default:
			st = model.CheckWarn
		}
		if in.Scheduling.Preemptible {
			msg += " (préemptible)"
		}
		r.checks = append(r.checks, ovhCheck{"gcp.instance.status", st, msg, kv})
		s.resources = append(s.resources, r)
	}
	s.gauges = append(s.gauges, ovhGauge{name: "gcp.instances", unit: "count", v: float64(len(all)), kv: []string{"gcp_project", project}})
	return len(all), nil
}
