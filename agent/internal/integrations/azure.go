package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Azure (§ 48) : VM, Resource Health et alertes d'Azure Monitor, en lecture seule ──────────
//
// Principal de service (tenant_id, client_id, client_secret) au rôle « Lecteur » de chaque
// abonnement. Jeton OAuth2 (client credentials), puis Azure Resource Manager : VM (taille,
// système) et leur état d'alimentation, adresses lues sur les interfaces réseau et les IP
// publiques, santé de chaque ressource vue par Azure (Resource Health : Unavailable,
// Degraded), alertes d'Azure Monitor déclenchées. Les VM deviennent des ressources cloud ; les
// alertes forment un contrôle par abonnement, qui revient au vert dès qu'aucune n'est déclenchée.

type azToken struct {
	mu  sync.Mutex
	tok string
	exp time.Time
}

func newAzure(cfg config.AzureConfig, d Deps) *cloudConn {
	client := &http.Client{Timeout: 30 * time.Second}
	login := strings.TrimRight(firstNonEmpty(cfg.LoginEndpoint, "https://login.microsoftonline.com"), "/")
	arm := strings.TrimRight(firstNonEmpty(cfg.Endpoint, "https://management.azure.com"), "/")
	tk := &azToken{}
	token := func(ctx context.Context) (string, error) {
		tk.mu.Lock()
		defer tk.mu.Unlock()
		if tk.tok != "" && time.Until(tk.exp) > 2*time.Minute {
			return tk.tok, nil
		}
		form := url.Values{"grant_type": {"client_credentials"}, "client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret},
			"scope": {"https://management.azure.com/.default"}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, login+"/"+url.PathEscape(cfg.TenantID)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
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
			desc := r.Desc
			if i := strings.Index(desc, "\r\n"); i > 0 {
				desc = desc[:i]
			}
			return "", fmt.Errorf("jeton refusé (HTTP %d %s) %s", resp.StatusCode, r.Error, model.Truncate(desc, 200))
		}
		tk.tok, tk.exp = r.Token, time.Now().Add(time.Duration(r.Expires)*time.Second)
		return tk.tok, nil
	}
	c := &cloudConn{id: "azure", enabled: cfg.Enabled, configured: cfg.TenantID != "" && cfg.ClientID != "" && cfg.ClientSecret != "" && len(cfg.Subscriptions) > 0,
		missing: "tenant_id, client_id, client_secret et subscriptions sont nécessaires", interval: cfg.Interval.D(), pub: newResourcePublisher(d)}
	c.poll = func(ctx context.Context) vsSnapshot {
		start := time.Now()
		var s vsSnapshot
		tok, err := token(ctx)
		if err != nil {
			s.errs = append(s.errs, err.Error())
			s.checks = append(s.checks, ovhCheck{"azure.can_connect", model.CheckCrit, err.Error(), nil})
			s.at, s.took = time.Now(), time.Since(start)
			return s
		}
		hdr := map[string]string{"Authorization": "Bearer " + tok}
		vms := 0
		for _, sub := range cfg.Subscriptions {
			n, err := azSubscription(ctx, client, arm, hdr, sub, cfg.Tags, &s)
			vms += n
			if err != nil {
				s.errs = append(s.errs, sub+" : "+err.Error())
			}
		}
		conn := ovhCheck{"azure.can_connect", model.CheckOK, fmt.Sprintf("%d VM dans %d abonnement(s)", vms, len(cfg.Subscriptions)), nil}
		if len(s.errs) > 0 {
			conn.status, conn.msg = model.CheckWarn, conn.msg+" ; lecture partielle : "+model.Truncate(strings.Join(s.errs, " ; "), 300)
			if len(s.errs) == len(cfg.Subscriptions) && vms == 0 {
				conn.status = model.CheckCrit
			}
		}
		s.checks = append(s.checks, conn)
		s.at, s.took = time.Now(), time.Since(start)
		return s
	}
	return c
}

// azList : liste ARM complète (value + nextLink).
func azList[T any](ctx context.Context, client *http.Client, first string, hdr map[string]string) ([]T, error) {
	var all []T
	next := first
	for page := 0; page < 50 && next != ""; page++ {
		var r struct {
			Value []T    `json:"value"`
			Next  string `json:"nextLink"`
		}
		if _, err := getJSON(ctx, client, next, hdr, &r); err != nil {
			return all, err
		}
		all = append(all, r.Value...)
		next = r.Next
	}
	return all, nil
}

type azVM struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Props    struct {
		Hardware struct {
			Size string `json:"vmSize"`
		} `json:"hardwareProfile"`
		Storage struct {
			OS struct {
				Type string `json:"osType"`
			} `json:"osDisk"`
		} `json:"storageProfile"`
		View struct {
			Statuses []struct {
				Code    string `json:"code"`
				Display string `json:"displayStatus"`
			} `json:"statuses"`
		} `json:"instanceView"`
	} `json:"properties"`
}

// azNIC : interface réseau (Microsoft.Network), rattachée à sa VM ; seule source des adresses,
// absentes de la liste des VM.
type azNIC struct {
	ID    string `json:"id"`
	Props struct {
		Primary bool `json:"primary"`
		VM      struct {
			ID string `json:"id"`
		} `json:"virtualMachine"`
		IPConfigs []struct {
			Props struct {
				Private string `json:"privateIPAddress"`
				Public  struct {
					ID string `json:"id"`
				} `json:"publicIPAddress"`
			} `json:"properties"`
		} `json:"ipConfigurations"`
	} `json:"properties"`
}

type azPublicIP struct {
	ID    string `json:"id"`
	Props struct {
		IP string `json:"ipAddress"`
	} `json:"properties"`
}

// azAddresses : adresses de chaque VM (identifiant en minuscules), privées puis publiques,
// interface principale d'abord.
func azAddresses(ctx context.Context, client *http.Client, base string, hdr map[string]string, problems *[]string) map[string][]string {
	nics, err := azList[azNIC](ctx, client, base+"/providers/Microsoft.Network/networkInterfaces?api-version=2024-05-01", hdr)
	if err != nil {
		*problems = append(*problems, "interfaces réseau : "+err.Error())
		return nil
	}
	public := map[string]string{}
	if pips, err := azList[azPublicIP](ctx, client, base+"/providers/Microsoft.Network/publicIPAddresses?api-version=2024-05-01", hdr); err == nil {
		for _, p := range pips {
			if p.Props.IP != "" {
				public[strings.ToLower(p.ID)] = p.Props.IP
			}
		}
	} else {
		*problems = append(*problems, "adresses publiques : "+err.Error())
	}
	sort.SliceStable(nics, func(i, j int) bool {
		if nics[i].Props.Primary != nics[j].Props.Primary {
			return nics[i].Props.Primary
		}
		return nics[i].ID < nics[j].ID
	})
	priv, pub := map[string][]string{}, map[string][]string{}
	for _, n := range nics {
		vm := strings.ToLower(n.Props.VM.ID)
		if vm == "" {
			continue
		}
		for _, c := range n.Props.IPConfigs {
			if c.Props.Private != "" {
				priv[vm] = append(priv[vm], c.Props.Private)
			}
			if ip := public[strings.ToLower(c.Props.Public.ID)]; ip != "" {
				pub[vm] = append(pub[vm], ip)
			}
		}
	}
	out := map[string][]string{}
	for vm, l := range priv {
		out[vm] = append(out[vm], l...)
	}
	for vm, l := range pub {
		out[vm] = append(out[vm], l...)
	}
	return out
}

// azUserInitiated : motif Resource Health d'un arrêt voulu par le client (« Customer Initiated »,
// « UserInitiated » selon les versions de l'API), par opposition à « Platform Initiated » ou
// « Unplanned ».
func azUserInitiated(reason string) bool {
	r := strings.ToLower(strings.ReplaceAll(reason, " ", ""))
	return r == "customerinitiated" || r == "userinitiated"
}

type azHealth struct {
	ID    string `json:"id"`
	Props struct {
		State   string `json:"availabilityState"`
		Summary string `json:"summary"`
		Reason  string `json:"reasonType"`
	} `json:"properties"`
}

type azAlert struct {
	Props struct {
		Essentials struct {
			Severity  string `json:"severity"`
			Condition string `json:"monitorCondition"`
			State     string `json:"alertState"`
			Target    string `json:"targetResourceName"`
			TargetID  string `json:"targetResource"`
			TargetRG  string `json:"targetResourceGroup"`
			Rule      string `json:"alertRule"`
		} `json:"essentials"`
	} `json:"properties"`
	Name string `json:"name"`
}

func azSubscription(ctx context.Context, client *http.Client, arm string, hdr map[string]string, sub string, extra map[string]string, s *vsSnapshot) (int, error) {
	base := arm + "/subscriptions/" + url.PathEscape(sub)
	vms, err := azList[azVM](ctx, client, base+"/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01", hdr)
	if err != nil {
		return 0, err
	}
	var problems []string
	// statusOnly=true ne rend que l'état d'exécution (instanceView), sans taille ni système (vu sur
	// un vrai abonnement le 19/11) : deux listes, fusionnées par identifiant.
	if st, err := azList[azVM](ctx, client, base+"/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01&statusOnly=true", hdr); err == nil {
		view := map[string]int{}
		for i := range st {
			view[strings.ToLower(st[i].ID)] = i
		}
		for i := range vms {
			if j, ok := view[strings.ToLower(vms[i].ID)]; ok {
				vms[i].Props.View = st[j].Props.View
			}
		}
	} else {
		problems = append(problems, "état des VM : "+err.Error())
	}
	addrs := azAddresses(ctx, client, base, hdr, &problems)
	health := map[string]azHealth{}
	if hs, err := azList[azHealth](ctx, client, base+"/providers/Microsoft.ResourceHealth/availabilityStatuses?api-version=2022-10-01", hdr); err == nil {
		for _, h := range hs {
			// …/virtualMachines/vm1/providers/Microsoft.ResourceHealth/availabilityStatuses/current
			if i := strings.Index(strings.ToLower(h.ID), "/providers/microsoft.resourcehealth/"); i > 0 {
				health[strings.ToLower(h.ID[:i])] = h
			}
		}
	} else {
		problems = append(problems, "Resource Health : "+err.Error())
	}
	sort.Slice(vms, func(i, j int) bool { return vms[i].Name < vms[j].Name })
	for _, vm := range vms {
		power := ""
		for _, st := range vm.Props.View.Statuses {
			if p, ok := strings.CutPrefix(st.Code, "PowerState/"); ok {
				power = p
			}
		}
		rg := ""
		if parts := strings.Split(vm.ID, "/"); len(parts) > 4 {
			rg = parts[4]
		}
		tags := map[string]string{"provider": "azure", "azure_subscription": sub, "azure_location": vm.Location, "azure_resource_group": rg}
		for k, v := range extra {
			tags[k] = v
		}
		r := &cloudResource{key: "azure:" + strings.ToLower(vm.ID), name: vm.Name, tags: tags,
			device: model.Device{Provider: "azure", Type: "azure_vm", Service: sub, DisplayName: vm.Name, Region: vm.Location,
				PowerState: power, Model: vm.Props.Hardware.Size, Vendor: "Microsoft Azure"}}
		r.platform, r.version = "Azure VM", vm.Props.Storage.OS.Type
		if ips := addrs[strings.ToLower(vm.ID)]; len(ips) > 0 {
			r.ips, r.device.IP = ips, ips[0]
		}
		kv := []string{"azure_subscription", sub, "azure_vm", vm.Name}
		st, msg := model.CheckOK, fmt.Sprintf("%s (%s) : %s", vm.Name, vm.Props.Hardware.Size, firstNonEmpty(power, "état inconnu"))
		if h, ok := health[strings.ToLower(vm.ID)]; ok {
			switch {
			case h.Props.State == "Unavailable" && azUserInitiated(h.Props.Reason):
				// VM arrêtée ou désallouée par son propriétaire : état voulu, comme « stopped » chez AWS
				// et « TERMINATED » chez Google (vu le 19/11 : « Customer Initiated »).
				msg += " ; arrêt demandé (Azure : " + model.Truncate(h.Props.Summary, 160) + ")"
			case h.Props.State == "Unavailable":
				st, msg = model.CheckCrit, msg+" ; Azure la dit indisponible : "+model.Truncate(h.Props.Summary, 200)
			case h.Props.State == "Degraded":
				st, msg = model.CheckWarn, msg+" ; Azure la dit dégradée : "+model.Truncate(h.Props.Summary, 200)
			case h.Props.State == "Available":
				msg += " ; santé Azure : disponible"
			}
		}
		r.checks = append(r.checks, ovhCheck{"azure.vm.health", st, msg, kv})
		s.resources = append(s.resources, r)
	}
	s.gauges = append(s.gauges, ovhGauge{name: "azure.vms", unit: "count", v: float64(len(vms)), kv: []string{"azure_subscription", sub}})

	// Une alerte déclenchée survit 30 jours à sa ressource et ne peut plus se fermer (ARM l'adresse
	// par la ressource : « ResourceNotFound », vu le 19/11) : écartée quand son groupe de ressources
	// a disparu ou qui vise une VM absente de la liste.
	groups := map[string]bool{}
	if rgs, err := azList[struct {
		Name string `json:"name"`
	}](ctx, client, base+"/resourcegroups?api-version=2021-04-01", hdr); err == nil {
		for _, g := range rgs {
			groups[strings.ToLower(g.Name)] = true
		}
	} else {
		groups = nil
		problems = append(problems, "groupes de ressources : "+err.Error())
	}
	vmIDs := map[string]bool{}
	for _, vm := range vms {
		vmIDs[strings.ToLower(vm.ID)] = true
	}
	alerts, err := azList[azAlert](ctx, client, base+"/providers/Microsoft.AlertsManagement/alerts?api-version=2019-05-05-preview&monitorCondition=Fired&alertState=New", hdr)
	if err != nil {
		problems = append(problems, "alertes : "+err.Error())
	} else {
		st, worst := model.CheckOK, ""
		var list []string
		orphans := 0
		for _, a := range alerts {
			e := a.Props.Essentials
			if e.Condition != "Fired" {
				continue
			}
			target := strings.ToLower(e.TargetID)
			if (groups != nil && e.TargetRG != "" && !groups[strings.ToLower(e.TargetRG)]) ||
				(strings.Contains(target, "/providers/microsoft.compute/virtualmachines/") && !vmIDs[target]) {
				orphans++
				continue
			}
			list = append(list, fmt.Sprintf("%s (%s, %s)", firstNonEmpty(a.Name, e.Rule), e.Severity, e.Target))
			if e.Severity == "Sev0" || e.Severity == "Sev1" {
				st = model.CheckCrit
			} else if st == model.CheckOK {
				st = model.CheckWarn
			}
			if worst == "" || e.Severity < worst {
				worst = e.Severity
			}
		}
		sort.Strings(list)
		fired := len(list)
		msg := "aucune alerte Azure Monitor déclenchée"
		if fired > 0 {
			if len(list) > 5 {
				list = append(list[:5], fmt.Sprintf("et %d autre(s)", len(list)-5))
			}
			msg = fmt.Sprintf("%d alerte(s) Azure Monitor déclenchée(s), la plus grave en %s : %s", fired, worst, strings.Join(list, ", "))
		}
		if orphans > 0 {
			msg += fmt.Sprintf(" ; %d alerte(s) sur des ressources supprimées, ignorée(s)", orphans)
		}
		s.checks = append(s.checks, ovhCheck{"azure.alerts", st, msg, []string{"azure_subscription", sub}})
	}
	if len(problems) > 0 {
		return len(vms), fmt.Errorf("%s", strings.Join(problems, " ; "))
	}
	return len(vms), nil
}
