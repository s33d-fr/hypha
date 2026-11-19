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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// cloudChecks : contrôles d'un relevé, ceux des ressources indexés par le nom de la ressource.
func cloudChecks(s vsSnapshot) map[string]ovhCheck {
	out := map[string]ovhCheck{}
	for _, c := range s.checks {
		out[c.name+"/"+strings.Join(c.kv, ",")] = c
	}
	for _, r := range s.resources {
		for _, c := range r.checks {
			out[c.name+"/"+r.name] = c
		}
	}
	return out
}

func expectChecks(t *testing.T, got map[string]ovhCheck, want map[string]struct {
	st   int
	text string
}) {
	t.Helper()
	for key, w := range want {
		c, ok := got[key]
		if !ok || c.status != w.st || !strings.Contains(c.msg, w.text) {
			t.Fatalf("%s : %+v (attendu %d %q) ; %v", key, c, w.st, w.text, got)
		}
	}
}

func TestScalewayPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "bonne-cle" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"message":"authentication is denied","type":"denied_authentication"}`)
			return
		}
		switch r.URL.Path {
		case "/instance/v1/zones/fr-par-1/servers":
			fmt.Fprint(w, `{"servers":[
				{"id":"s1","name":"web","commercial_type":"DEV1-S","state":"running","private_ip":"172.16.1.2","public_ips":[{"address":"51.15.0.1"}],
				 "image":{"name":"Debian 13"},"maintenances":[{"reason":"migration de l'hôte","start_date":"2026-11-25T02:00:00Z"}]},
				{"id":"s2","name":"bloc","commercial_type":"PLAY2-PICO","state":"locked"}]}`)
		case "/baremetal/v1/zones/fr-par-1/servers":
			fmt.Fprint(w, `{"servers":[{"id":"m1","name":"metal","status":"ready","offer_name":"EM-A115X-SSD","ping_status":"ping_status_down",
				"ips":[{"address":"62.210.0.9","version":"IPv4"}]}]}`)
		case "/instance/v1/zones/pl-waw-1/servers":
			fmt.Fprint(w, `{"servers":[]}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"zone not available"}`)
		}
	}))
	defer srv.Close()
	c := newScaleway(config.ScalewayConfig{Enabled: "auto", SecretKey: "bonne-cle", Zones: []string{"fr-par-1", "pl-waw-1"}, Endpoint: srv.URL,
		Interval: config.Duration(5 * time.Minute)}, Deps{})
	s := c.poll(context.Background())
	if len(s.errs) != 0 {
		t.Fatalf("erreurs : %v", s.errs)
	}
	expectChecks(t, cloudChecks(s), map[string]struct {
		st   int
		text string
	}{
		"scaleway.instance.state/web":    {model.CheckWarn, "maintenance planifiée : migration de l'hôte 2026-11-25T02:00:00Z"},
		"scaleway.instance.state/bloc":   {model.CheckCrit, "verrouillée par Scaleway"},
		"scaleway.baremetal.state/metal": {model.CheckCrit, "ne répond plus au ping"},
		"scaleway.can_connect/":          {model.CheckOK, "2 instance(s) et 1 serveur(s) Elastic Metal dans 2 zone(s)"},
	})
	c = newScaleway(config.ScalewayConfig{Enabled: "auto", SecretKey: "mauvaise", Zones: []string{"fr-par-1"}, Endpoint: srv.URL,
		Interval: config.Duration(5 * time.Minute)}, Deps{})
	s = c.poll(context.Background())
	if conn := cloudChecks(s)["scaleway.can_connect/"]; conn.status != model.CheckCrit || !strings.Contains(conn.msg, "authentication is denied") || strings.Contains(conn.msg, "mauvaise") {
		t.Fatalf("refus : %+v", conn)
	}
}

func TestAzurePoll(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tenant-1/oauth2/v2.0/token" {
			r.ParseForm()
			if r.Form.Get("client_secret") != "bon" || r.Form.Get("scope") != "https://management.azure.com/.default" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided.\r\nTrace ID: x"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"jeton-arm","expires_in":3599}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer jeton-arm" {
			w.WriteHeader(401)
			return
		}
		sub := "/subscriptions/sub-1"
		switch {
		case r.URL.Path == sub+"/providers/Microsoft.Compute/virtualMachines" && r.URL.Query().Get("skiptoken") == "":
			fmt.Fprintf(w, `{"value":[{"id":"/subscriptions/sub-1/resourceGroups/rg-web/providers/Microsoft.Compute/virtualMachines/vm-web","name":"vm-web",
				"location":"francecentral","properties":{"hardwareProfile":{"vmSize":"Standard_B2s"},"storageProfile":{"osDisk":{"osType":"Linux"}},
				"instanceView":{"statuses":[{"code":"ProvisioningState/succeeded"},{"code":"PowerState/running"}]}}}],
				"nextLink":"%s%s/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01&skiptoken=2"}`, srv.URL, sub)
		case r.URL.Path == sub+"/providers/Microsoft.Compute/virtualMachines":
			fmt.Fprint(w, `{"value":[{"id":"/subscriptions/sub-1/resourceGroups/RG-DB/providers/Microsoft.Compute/virtualMachines/vm-db","name":"vm-db",
				"location":"francecentral","properties":{"hardwareProfile":{"vmSize":"Standard_D4s_v5"},
				"instanceView":{"statuses":[{"code":"PowerState/running"}]}}}]}`)
		case r.URL.Path == sub+"/providers/Microsoft.ResourceHealth/availabilityStatuses":
			fmt.Fprint(w, `{"value":[
				{"id":"/subscriptions/sub-1/resourceGroups/rg-db/providers/Microsoft.Compute/virtualMachines/vm-db/providers/Microsoft.ResourceHealth/availabilityStatuses/current",
				 "properties":{"availabilityState":"Unavailable","summary":"We're sorry, your virtual machine isn't available because of a platform problem."}},
				{"id":"/subscriptions/sub-1/resourceGroups/rg-web/providers/Microsoft.Compute/virtualMachines/vm-web/providers/Microsoft.ResourceHealth/availabilityStatuses/current",
				 "properties":{"availabilityState":"Available","summary":"There aren't any known problems."}}]}`)
		case r.URL.Path == sub+"/resourcegroups":
			fmt.Fprint(w, `{"value":[{"name":"rg-web"},{"name":"RG-DB"}]}`)
		case r.URL.Path == sub+"/providers/Microsoft.Network/networkInterfaces", r.URL.Path == sub+"/providers/Microsoft.Network/publicIPAddresses":
			fmt.Fprint(w, `{"value":[]}`)
		case r.URL.Path == sub+"/providers/Microsoft.AlertsManagement/alerts":
			fmt.Fprint(w, `{"value":[{"name":"cpu-haut","properties":{"essentials":{"severity":"Sev1","monitorCondition":"Fired","alertState":"New",
				"targetResourceName":"vm-web","alertRule":"cpu-haut"}}}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	cfg := config.AzureConfig{Enabled: "auto", TenantID: "tenant-1", ClientID: "app", ClientSecret: "bon", Subscriptions: []string{"sub-1"},
		Endpoint: srv.URL, LoginEndpoint: srv.URL, Interval: config.Duration(5 * time.Minute)}
	s := newAzure(cfg, Deps{}).poll(context.Background())
	if len(s.errs) != 0 {
		t.Fatalf("erreurs : %v", s.errs)
	}
	expectChecks(t, cloudChecks(s), map[string]struct {
		st   int
		text string
	}{
		"azure.vm.health/vm-web":                {model.CheckOK, "vm-web (Standard_B2s) : running ; santé Azure : disponible"},
		"azure.vm.health/vm-db":                 {model.CheckCrit, "Azure la dit indisponible : We're sorry"},
		"azure.alerts/azure_subscription,sub-1": {model.CheckCrit, "1 alerte(s) Azure Monitor déclenchée(s), la plus grave en Sev1 : cpu-haut (Sev1, vm-web)"},
		"azure.can_connect/":                    {model.CheckOK, "2 VM dans 1 abonnement(s)"},
	})
	for _, r := range s.resources {
		if r.name == "vm-db" && r.tags["azure_resource_group"] != "RG-DB" {
			t.Fatalf("groupe de ressources : %v", r.tags)
		}
	}
	cfg.ClientSecret = "mauvais"
	s = newAzure(cfg, Deps{}).poll(context.Background())
	if conn := cloudChecks(s)["azure.can_connect/"]; conn.status != model.CheckCrit || !strings.Contains(conn.msg, "AADSTS7000215") ||
		strings.Contains(conn.msg, "Trace ID") || strings.Contains(conn.msg, "mauvais") {
		t.Fatalf("refus : %+v", conn)
	}
}

func TestGCPPoll(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			r.ParseForm()
			parts := strings.Split(r.Form.Get("assertion"), ".")
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || len(parts) != 3 {
				w.WriteHeader(400)
				return
			}
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var c map[string]any
			json.Unmarshal(claims, &c)
			if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) != nil || c["iss"] != "hypha@projet.iam.gserviceaccount.com" ||
				c["scope"] != "https://www.googleapis.com/auth/compute.readonly" || c["aud"] != srv.URL+"/token" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"jeton-gce","expires_in":3599,"token_type":"Bearer"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer jeton-gce" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/compute/v1/projects/projet/aggregated/instances" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":404,"message":"The resource 'projects/inconnu' was not found"}}`)
			return
		}
		fmt.Fprint(w, `{"items":{
			"zones/europe-west9-a":{"instances":[{"id":"1","name":"web","status":"RUNNING","machineType":"https://x/zones/europe-west9-a/machineTypes/e2-small",
				"zone":"https://x/zones/europe-west9-a","networkInterfaces":[{"networkIP":"172.16.200.2","accessConfigs":[{"natIP":"34.1.2.3"}]}]}]},
			"zones/europe-west1-b":{"instances":[{"id":"2","name":"calcul","status":"REPAIRING","machineType":"https://x/machineTypes/n2-standard-4",
				"zone":"https://x/zones/europe-west1-b","scheduling":{"preemptible":true}}]},
			"zones/us-east1-c":{"warning":{"code":"NO_RESULTS_ON_PAGE"}}}}`)
	}))
	defer srv.Close()
	cred := filepath.Join(t.TempDir(), "cle.json")
	b, _ := json.Marshal(map[string]string{"client_email": "hypha@projet.iam.gserviceaccount.com", "token_uri": srv.URL + "/token",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	os.WriteFile(cred, b, 0o600)
	cfg := config.GCPConfig{Enabled: "auto", CredentialsFile: cred, Projects: []string{"projet", "inconnu"}, Endpoint: srv.URL, Interval: config.Duration(5 * time.Minute)}
	s := newGCP(cfg, Deps{}).poll(context.Background())
	expectChecks(t, cloudChecks(s), map[string]struct {
		st   int
		text string
	}{
		"gcp.instance.status/web":    {model.CheckOK, "web (e2-small, europe-west9-a) : RUNNING"},
		"gcp.instance.status/calcul": {model.CheckCrit, "Google répare l'hôte de l'instance (préemptible)"},
		"gcp.can_connect/":           {model.CheckWarn, "lecture partielle : inconnu : HTTP 404 : The resource 'projects/inconnu' was not found"},
	})
	if len(s.resources) != 2 || s.resources[1].device.IP != "172.16.200.2" || len(s.resources[1].ips) != 2 {
		t.Fatalf("ressources : %+v", s.resources)
	}
}
