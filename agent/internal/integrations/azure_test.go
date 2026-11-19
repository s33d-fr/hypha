package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Réponses calquées sur un vrai abonnement (19/11) : la liste statusOnly=true ne porte que
// l'instanceView, la liste complète que la taille et le système ; les adresses ne sont que sur
// les interfaces réseau et les IP publiques.
func TestAzureReponsesReelles(t *testing.T) {
	const sub = "sub-1"
	vmA := "/subscriptions/sub-1/resourceGroups/rg-a/providers/Microsoft.Compute/virtualMachines/vm-a"
	vmB := "/subscriptions/sub-1/resourceGroups/rg-a/providers/Microsoft.Compute/virtualMachines/vm-b"
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			r.ParseForm()
			if r.Form.Get("client_secret") != "secret" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided."}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"jeton","expires_in":3599}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer jeton" {
			w.WriteHeader(401)
			return
		}
		key := r.URL.Path
		if r.URL.Query().Get("statusOnly") == "true" {
			key += "?statusOnly"
		}
		paths = append(paths, key)
		switch key {
		case "/subscriptions/sub-1/providers/Microsoft.Compute/virtualMachines":
			fmt.Fprintf(w, `{"value":[
				{"id":%q,"name":"vm-a","location":"francecentral","properties":{"hardwareProfile":{"vmSize":"Standard_B1ls"},"storageProfile":{"osDisk":{"osType":"Linux"}}}},
				{"id":%q,"name":"vm-b","location":"francecentral","properties":{"hardwareProfile":{"vmSize":"Standard_D2s_v5"},"storageProfile":{"osDisk":{"osType":"Windows"}}}}]}`, vmA, vmB)
		case "/subscriptions/sub-1/providers/Microsoft.Compute/virtualMachines?statusOnly":
			fmt.Fprintf(w, `{"value":[
				{"id":%q,"name":"vm-a","properties":{"instanceView":{"statuses":[{"code":"ProvisioningState/succeeded"},{"code":"PowerState/running","displayStatus":"VM running"}]}}},
				{"id":%q,"name":"vm-b","properties":{"instanceView":{"statuses":[{"code":"PowerState/deallocated"}]}}}]}`, vmA, vmB)
		case "/subscriptions/sub-1/providers/Microsoft.Network/networkInterfaces":
			// Groupe de ressources en majuscules dans la référence : la correspondance ignore la casse.
			fmt.Fprint(w, `{"value":[
				{"id":"/n/nic-a2","properties":{"primary":false,"virtualMachine":{"id":"/subscriptions/sub-1/resourceGroups/RG-A/providers/Microsoft.Compute/virtualMachines/vm-a"},
					"ipConfigurations":[{"properties":{"privateIPAddress":"172.16.1.4"}}]}},
				{"id":"/n/nic-a1","properties":{"primary":true,"virtualMachine":{"id":"/subscriptions/sub-1/resourceGroups/RG-A/providers/Microsoft.Compute/virtualMachines/vm-a"},
					"ipConfigurations":[{"properties":{"privateIPAddress":"172.16.0.4","publicIPAddress":{"id":"/p/PIP-A"}}}]}},
				{"id":"/n/orpheline","properties":{"ipConfigurations":[{"properties":{"privateIPAddress":"172.16.9.9"}}]}}]}`)
		case "/subscriptions/sub-1/providers/Microsoft.Network/publicIPAddresses":
			fmt.Fprint(w, `{"value":[{"id":"/p/pip-a","properties":{"ipAddress":"20.19.1.2"}}]}`)
		case "/subscriptions/sub-1/providers/Microsoft.ResourceHealth/availabilityStatuses":
			// Réponse lue le 19/11 pour une VM désallouée par son propriétaire.
			fmt.Fprintf(w, `{"value":[{"id":%q,"properties":{"availabilityState":"Unavailable","reasonType":"Customer Initiated",
				"summary":"This virtual machine is stopped and deallocated as requested by an authorized user or process."}}]}`,
				vmB+"/providers/Microsoft.ResourceHealth/availabilityStatuses/current")
		case "/subscriptions/sub-1/providers/Microsoft.AlertsManagement/alerts":
			// Une alerte réelle, puis deux orphelines (groupe supprimé ; VM supprimée d'un groupe restant).
			fmt.Fprintf(w, `{"value":[
				{"name":"cpu-haute","properties":{"essentials":{"severity":"Sev3","monitorCondition":"Fired","alertState":"New","targetResourceName":"vm-a",
					"targetResourceGroup":"rg-a","targetResource":%q}}},
				{"name":"orpheline-1","properties":{"essentials":{"severity":"Sev1","monitorCondition":"Fired","alertState":"New","targetResourceName":"vm-x",
					"targetResourceGroup":"rg-supprime","targetResource":"/subscriptions/sub-1/resourcegroups/rg-supprime/providers/microsoft.compute/virtualmachines/vm-x"}}},
				{"name":"orpheline-2","properties":{"essentials":{"severity":"Sev0","monitorCondition":"Fired","alertState":"New","targetResourceName":"vm-y",
					"targetResourceGroup":"rg-a","targetResource":"/subscriptions/sub-1/resourcegroups/rg-a/providers/microsoft.compute/virtualmachines/vm-y"}}}]}`, strings.ToLower(vmA))
		case "/subscriptions/sub-1/resourcegroups":
			fmt.Fprint(w, `{"value":[{"name":"RG-A"},{"name":"autre"}]}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":"NotFound"}}`)
		}
	}))
	defer srv.Close()
	c := newAzure(config.AzureConfig{Enabled: "auto", TenantID: "tenant", ClientID: "app", ClientSecret: "secret", Subscriptions: []string{sub},
		Endpoint: srv.URL, LoginEndpoint: srv.URL, Interval: config.Duration(5 * time.Minute)}, Deps{})
	s := c.poll(context.Background())
	if len(s.errs) != 0 {
		t.Fatalf("erreurs : %v", s.errs)
	}
	checks := map[string]ovhCheck{}
	for _, ch := range s.checks {
		checks[ch.name] = ch
	}
	if ch := checks["azure.can_connect"]; ch.status != model.CheckOK || ch.msg != "2 VM dans 1 abonnement(s)" {
		t.Fatalf("connexion : %+v", ch)
	}
	if ch := checks["azure.alerts"]; ch.status != model.CheckWarn || !strings.Contains(ch.msg, "1 alerte(s) Azure Monitor déclenchée(s), la plus grave en Sev3 : cpu-haute (Sev3, vm-a) ; 2 alerte(s) sur des ressources supprimées, ignorée(s)") {
		t.Fatalf("alertes : %+v", ch)
	}
	if len(s.resources) != 2 {
		t.Fatalf("ressources : %d", len(s.resources))
	}
	a, b := s.resources[0], s.resources[1]
	if a.name != "vm-a" || a.checks[0].status != model.CheckOK || !strings.Contains(a.checks[0].msg, "vm-a (Standard_B1ls) : running") ||
		a.device.Model != "Standard_B1ls" || a.device.PowerState != "running" || a.version != "Linux" {
		t.Fatalf("vm-a : %+v %+v", a.device, a.checks)
	}
	if got := strings.Join(a.ips, ","); got != "172.16.0.4,172.16.1.4,20.19.1.2" || a.device.IP != "172.16.0.4" {
		t.Fatalf("adresses de vm-a : %q, IP %q", got, a.device.IP)
	}
	// Arrêt voulu : vert, comme « stopped » chez AWS ; une indisponibilité de la plateforme reste
	// CRIT (TestAzurePoll, vm-db).
	if b.checks[0].status != model.CheckOK || !strings.Contains(b.checks[0].msg, "vm-b (Standard_D2s_v5) : deallocated ; arrêt demandé (Azure : This virtual machine is stopped") ||
		len(b.ips) != 0 || b.device.IP != "" {
		t.Fatalf("vm-b : %+v ips %v", b.checks, b.ips)
	}
	want := "/subscriptions/sub-1/providers/Microsoft.Compute/virtualMachines," +
		"/subscriptions/sub-1/providers/Microsoft.Compute/virtualMachines?statusOnly," +
		"/subscriptions/sub-1/providers/Microsoft.Network/networkInterfaces," +
		"/subscriptions/sub-1/providers/Microsoft.Network/publicIPAddresses," +
		"/subscriptions/sub-1/providers/Microsoft.ResourceHealth/availabilityStatuses," +
		"/subscriptions/sub-1/resourcegroups," +
		"/subscriptions/sub-1/providers/Microsoft.AlertsManagement/alerts"
	if got := strings.Join(paths, ","); got != want {
		t.Fatalf("appels :\n %s\nattendu :\n %s", got, want)
	}
	// Secret refusé : CRIT, message d'Entra ID repris, sans le secret.
	c = newAzure(config.AzureConfig{Enabled: "auto", TenantID: "tenant", ClientID: "app", ClientSecret: "secret-a-ne-pas-voir", Subscriptions: []string{sub},
		Endpoint: srv.URL, LoginEndpoint: srv.URL, Interval: config.Duration(5 * time.Minute)}, Deps{})
	s = c.poll(context.Background())
	if len(s.checks) != 1 || s.checks[0].status != model.CheckCrit || !strings.Contains(s.checks[0].msg, "AADSTS7000215") ||
		strings.Contains(s.checks[0].msg, "secret-a-ne-pas-voir") {
		t.Fatalf("refus : %+v", s.checks)
	}
}
