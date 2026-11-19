package integrations

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Scaleway (§ 47) : instances et Elastic Metal par l'API, en lecture seule ────────────────
//
// Clé d'API d'un membre IAM aux droits de lecture (InstancesReadOnly, ElasticMetalReadOnly),
// envoyée dans X-Auth-Token. Par zone : instances (état, verrouillage, maintenances planifiées)
// et serveurs Elastic Metal (état, réponse au ping de Scaleway), publiés en ressources cloud.

func newScaleway(cfg config.ScalewayConfig, d Deps) *cloudConn {
	client := &http.Client{Timeout: 30 * time.Second}
	base := strings.TrimRight(firstNonEmpty(cfg.Endpoint, "https://api.scaleway.com"), "/")
	hdr := map[string]string{"X-Auth-Token": cfg.SecretKey}
	c := &cloudConn{id: "scaleway", enabled: cfg.Enabled, configured: cfg.SecretKey != "" && len(cfg.Zones) > 0,
		missing: "secret_key et zones sont nécessaires", interval: cfg.Interval.D(), pub: newResourcePublisher(d)}
	c.poll = func(ctx context.Context) vsSnapshot {
		start := time.Now()
		var s vsSnapshot
		servers, metal := 0, 0
		for _, zone := range cfg.Zones {
			n, err := scwInstances(ctx, client, base, hdr, zone, cfg.Tags, &s)
			servers += n
			if err != nil {
				s.errs = append(s.errs, zone+" (instances) : "+err.Error())
			}
			n, err = scwMetal(ctx, client, base, hdr, zone, cfg.Tags, &s)
			metal += n
			if err != nil {
				s.errs = append(s.errs, zone+" (Elastic Metal) : "+err.Error())
			}
		}
		conn := ovhCheck{"scaleway.can_connect", model.CheckOK,
			fmt.Sprintf("%d instance(s) et %d serveur(s) Elastic Metal dans %d zone(s)", servers, metal, len(cfg.Zones)), nil}
		switch {
		case len(s.errs) == 2*len(cfg.Zones):
			conn.status, conn.msg = model.CheckCrit, model.Truncate(strings.Join(s.errs, " ; "), 400)
		case len(s.errs) > 0:
			conn.status, conn.msg = model.CheckWarn, conn.msg+" ; lecture partielle : "+model.Truncate(strings.Join(s.errs, " ; "), 300)
		}
		s.checks = append(s.checks, conn)
		s.at, s.took = time.Now(), time.Since(start)
		return s
	}
	return c
}

type scwIP struct {
	Address string `json:"address"`
}

type scwServer struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	CommercialType string   `json:"commercial_type"`
	State          string   `json:"state"`
	StateDetail    string   `json:"state_detail"`
	Arch           string   `json:"arch"`
	PrivateIP      string   `json:"private_ip"`
	PublicIP       *scwIP   `json:"public_ip"`
	PublicIPs      []scwIP  `json:"public_ips"`
	Tags           []string `json:"tags"`
	Image          *struct {
		Name string `json:"name"`
	} `json:"image"`
	Maintenances []struct {
		Reason    string `json:"reason"`
		StartDate string `json:"start_date"`
	} `json:"maintenances"`
}

func scwResource(kind, zone, id, name string, extra map[string]string) *cloudResource {
	tags := map[string]string{"provider": "scaleway", "scw_zone": zone}
	for k, v := range extra {
		tags[k] = v
	}
	return &cloudResource{key: "scaleway:" + kind + ":" + zone + ":" + id, name: name, tags: tags}
}

func scwInstances(ctx context.Context, client *http.Client, base string, hdr map[string]string, zone string, extra map[string]string, s *vsSnapshot) (int, error) {
	var all []scwServer
	for page := 1; page <= 20; page++ {
		var r struct {
			Servers []scwServer `json:"servers"`
		}
		if _, err := getJSON(ctx, client, fmt.Sprintf("%s/instance/v1/zones/%s/servers?per_page=100&page=%d", base, zone, page), hdr, &r); err != nil {
			return len(all), err
		}
		all = append(all, r.Servers...)
		if len(r.Servers) < 100 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	for _, sv := range all {
		r := scwResource("instance", zone, sv.ID, sv.Name, extra)
		ip := sv.PrivateIP
		for _, p := range sv.PublicIPs {
			r.ips = append(r.ips, p.Address)
			ip = firstNonEmpty(ip, p.Address)
		}
		if sv.PublicIP != nil && sv.PublicIP.Address != "" {
			ip = firstNonEmpty(ip, sv.PublicIP.Address)
		}
		if sv.PrivateIP != "" {
			r.ips = append(r.ips, sv.PrivateIP)
		}
		r.device = model.Device{Provider: "scaleway", Type: "scw_instance", Service: zone, DisplayName: sv.Name, IP: ip, Region: zone,
			PowerState: sv.State, Model: sv.CommercialType, Vendor: "Scaleway"}
		r.platform = "Scaleway Instance"
		if sv.Image != nil {
			r.version = sv.Image.Name
		}
		kv := []string{"scw_zone", zone, "scw_server", sv.Name}
		st, msg := model.CheckOK, fmt.Sprintf("%s (%s) : %s", sv.Name, sv.CommercialType, sv.State)
		switch sv.State {
		case "locked":
			st, msg = model.CheckCrit, msg+", verrouillée par Scaleway (abus signalé ou facture impayée)"
		case "running", "stopped", "stopped in place", "starting", "stopping":
		default:
			st = model.CheckWarn
		}
		var maint []string
		for _, m := range sv.Maintenances {
			maint = append(maint, strings.TrimSpace(m.Reason+" "+m.StartDate))
		}
		if len(maint) > 0 {
			msg += " ; maintenance planifiée : " + strings.Join(maint, ", ")
			if st == model.CheckOK {
				st = model.CheckWarn
			}
		}
		r.checks = append(r.checks, ovhCheck{"scaleway.instance.state", st, msg, kv})
		s.resources = append(s.resources, r)
	}
	s.gauges = append(s.gauges, ovhGauge{name: "scaleway.instances", unit: "count", v: float64(len(all)), kv: []string{"scw_zone", zone}})
	return len(all), nil
}

type scwMetalServer struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	OfferName  string   `json:"offer_name"`
	PingStatus string   `json:"ping_status"`
	Tags       []string `json:"tags"`
	IPs        []struct {
		Address string `json:"address"`
		Version string `json:"version"`
	} `json:"ips"`
	Install *struct {
		OSID string `json:"os_id"`
	} `json:"install"`
}

func scwMetal(ctx context.Context, client *http.Client, base string, hdr map[string]string, zone string, extra map[string]string, s *vsSnapshot) (int, error) {
	var all []scwMetalServer
	for page := 1; page <= 20; page++ {
		var r struct {
			Servers []scwMetalServer `json:"servers"`
		}
		code, err := getJSON(ctx, client, fmt.Sprintf("%s/baremetal/v1/zones/%s/servers?page_size=100&page=%d", base, zone, page), hdr, &r)
		if code == http.StatusNotFound || (code == http.StatusBadRequest && page == 1) {
			return 0, nil // Elastic Metal n'existe pas dans cette zone
		}
		if err != nil {
			return len(all), err
		}
		all = append(all, r.Servers...)
		if len(r.Servers) < 100 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	for _, sv := range all {
		r := scwResource("baremetal", zone, sv.ID, sv.Name, extra)
		ip := ""
		for _, p := range sv.IPs {
			r.ips = append(r.ips, p.Address)
			if p.Version == "IPv4" {
				ip = firstNonEmpty(ip, p.Address)
			}
		}
		r.device = model.Device{Provider: "scaleway", Type: "scw_baremetal", Service: zone, DisplayName: sv.Name, IP: ip, Region: zone,
			PowerState: sv.Status, Model: sv.OfferName, Vendor: "Scaleway"}
		r.platform = "Scaleway Elastic Metal"
		kv := []string{"scw_zone", zone, "scw_server", sv.Name}
		st, msg := model.CheckOK, fmt.Sprintf("%s (%s) : %s", sv.Name, sv.OfferName, sv.Status)
		switch {
		case sv.Status == "error" || sv.Status == "locked":
			st = model.CheckCrit
		case sv.Status == "ready" && sv.PingStatus == "ping_status_down":
			st, msg = model.CheckCrit, msg+", ne répond plus au ping de Scaleway"
		case sv.Status != "ready" && sv.Status != "stopped":
			st = model.CheckWarn
		}
		r.checks = append(r.checks, ovhCheck{"scaleway.baremetal.state", st, msg, kv})
		s.resources = append(s.resources, r)
	}
	s.gauges = append(s.gauges, ovhGauge{name: "scaleway.baremetal.servers", unit: "count", v: float64(len(all)), kv: []string{"scw_zone", zone}})
	return len(all), nil
}

