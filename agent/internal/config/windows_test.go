package config

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWindowsEventlogChannels(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
logs:
  windows_eventlog:
    - System
    - channel: Security
      event_ids: [4625, "4720-4726", 4740]
      exclude_event_ids: [4634]
      levels: [audit_failure, Info]
      service: securite
      tags: {role: dc}
      event_id: 4624
    - channel: Application
      exclude_providers: [HYPHA-bruit, "Microsoft-Windows-Security-SPP*"]
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs : %v", errs)
	}
	ev := c.Logs.WindowsEventlog
	if len(ev) != 3 || ev[0].Channel != "System" || !ev[0].Plain() || ev[1].Plain() {
		t.Fatalf("canaux : %+v", ev)
	}
	sec := ev[1]
	if sec.Service != "securite" || sec.Tags["role"] != "dc" || strings.Join(sec.Levels, ",") != "audit_failure,information" {
		t.Fatalf("Security : %+v", sec)
	}
	ids, err := ParseEventIDs(sec.EventIDs)
	if err != nil || len(ids) != 3 || ids[1] != (IDRange{4720, 4726}) || ids[2] != (IDRange{4740, 4740}) {
		t.Fatalf("identifiants : %v %v", ids, err)
	}
	warned := false
	for _, w := range c.Warnings {
		warned = warned || strings.Contains(w, "clé inconnue ignorée : event_id")
	}
	if !warned {
		t.Fatalf("clé inconnue non signalée : %v", c.Warnings)
	}
	if got := strings.Join(EventlogNames(ev), " "); got != "System Security* Application*" {
		t.Fatalf("noms : %q", got)
	}
	// Un canal sans filtre s'écrit en texte dans check-config.
	y, err := c.RedactedYAML()
	if err != nil || !strings.Contains(y, "- System\n") || !strings.Contains(y, "channel: Security") {
		t.Fatalf("YAML :\n%s", y)
	}
	// Par défaut : System et Application.
	if d := Default().Logs.WindowsEventlog; len(d) != 2 || d[0].Channel != "System" || d[1].Channel != "Application" {
		t.Fatalf("défaut : %+v", d)
	}
}

func TestWindowsValidation(t *testing.T) {
	c := Default()
	c.APIKey = testKey
	c.Logs.WindowsEventlog = []EventlogChannel{
		{Channel: "System"}, {Channel: "system"}, {Channel: ""},
		{Channel: "Security", Levels: []string{"grave"}, EventIDs: []string{"70000", "10-5"}, Providers: []string{"["}},
		{Channel: "Application", Service: "a b", Tags: map[string]string{"Mauvaise": "x"}},
	}
	c.Integrations.PerfCounters = []PerfCounterSet{
		{Name: "disques", Counters: []PerfCounter{
			{Path: `\PhysicalDisk(*)\Avg. Disk sec/Read`},
			{Path: `\\serveur\Processor(_Total)\% Processor Time`},
			{Path: `\Process(sql*)\% Processor Time`},
			{Path: `Processor\% Processor Time`, Metric: "Mauvais Nom", Unit: "Octets"},
		}},
		{Name: "disques"},
		{Name: "Majuscules", Counters: []PerfCounter{{Path: `\Memory\Available MBytes`}}, Interval: Duration(time.Second)},
	}
	c.Integrations.WMI = []WMIQuery{
		{Name: "os", Class: "Win32_OperatingSystem", Properties: []WMIProperty{{Property: "FreePhysicalMemory"}}},
		{Name: "mal", Class: "Win32 Service;", WMINamespace: `root\cimv2;x`, Filter: "Name='a'; DROP", TagBy: []string{"Na me"},
			Properties: []WMIProperty{{Property: "1x"}}},
		{Name: "vide", Class: "Win32_Service"},
	}
	c.Integrations.WindowsDNS.ExcludeZones = []string{"["}
	c.Integrations.WindowsDNS.Address = "127.0.0.1"
	c.normalize()
	errs := strings.Join(c.Validate(true), "\n")
	for _, want := range []string{
		"logs.windows_eventlog[1] (system) : canal en double",
		"logs.windows_eventlog[2] : channel manquant",
		`niveau "grave" inconnu`,
		`identifiant "70000"`,
		`source "[" invalide`,
		`service "a b" invalide`,
		`étiquette "Mauvaise"`,
		`counters[1] : path "\\\\serveur\\Processor(_Total)\\% Processor Time"`,
		`counters[2] : instance "sql*" : (*) entier seulement`,
		`counters[3] : path "Processor\\% Processor Time"`,
		`metric "Mauvais Nom" invalide`,
		`unit "Octets" invalide`,
		"integrations.perf_counters[1] (disques) : name en double",
		"integrations.perf_counters[1] (disques) : counters vide",
		"integrations.perf_counters[2] (Majuscules) : name invalide",
		"interval 1s hors de 10s à 1h",
		`class "Win32 Service;" invalide`,
		`wmi_namespace "root\\cimv2;x" invalide`,
		"filter invalide",
		`tag_by : propriété "Na me" invalide`,
		`propriété "1x" invalide`,
		"integrations.wmi[2] (vide) : properties vide",
		`exclude_zones : motif "[" invalide`,
		`address "127.0.0.1" attendue`,
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("erreur attendue absente : %s", want)
		}
	}
	if strings.Contains(errs, "integrations.wmi[0]") || strings.Contains(errs, "perf_counters[0] (disques) : counters[0]") {
		t.Fatalf("entrée valide refusée :\n%s", errs)
	}
	if t.Failed() {
		t.Logf("erreurs rendues :\n%s", errs)
	}
	ws := strings.Join(c.Warnings, "\n")
	if runtime.GOOS != "windows" && !strings.Contains(ws, "ignorés sur cet hôte") {
		t.Fatalf("avertissement hors Windows absent : %s", ws)
	}
}

func TestPerfCountersAndWMIDefaults(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
integrations:
  perf_counters:
    - name: sql-server
      counters:
        - path: '\SQLServer:Locks(_Total)\Lock Waits/sec'
        - path: '\Processor(*)\% Processor Time'
          metric: cpu
          exclude_instances: [_Total]
  wmi:
    - name: os
      class: Win32_OperatingSystem
      properties: [FreePhysicalMemory, {property: NumberOfProcesses, metric: processus, unit: count}]
`,
		"conf.d/50-plus.yaml": `integrations:
  perf_counters:
    - name: memoire
      namespace: mem
      interval: 1m
      timeout: 5s
      max_series: 50
      counters: [{path: '\Memory\Available MBytes'}]
  wmi:
    - name: disques
      wmi_namespace: root/Microsoft/Windows/Storage
      class: MSFT_PhysicalDisk
      properties: [HealthStatus]
      tag_by: [FriendlyName]
  active_directory: {logs: false}
  windows_dns: {enabled: false, test_zone: exemple.fr}
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs : %v", errs)
	}
	pc := c.Integrations.PerfCounters
	if len(pc) != 2 || pc[0].Name != "sql-server" || pc[1].Name != "memoire" {
		t.Fatalf("jeux de compteurs (listes additionnées) : %+v", pc)
	}
	sql := pc[0]
	if sql.Namespace != "sql_server" || sql.Interval.D() != 30*time.Second || sql.Timeout.D() != 10*time.Second || sql.MaxSeries != 500 {
		t.Fatalf("défauts : %+v", sql)
	}
	if sql.Counters[0].Metric != "lock_waits_per_s" || sql.Counters[0].InstanceTag != "instance" || sql.Counters[1].Metric != "cpu" {
		t.Fatalf("compteurs : %+v", sql.Counters)
	}
	if m := pc[1]; m.Namespace != "mem" || m.Interval.D() != time.Minute || m.Timeout.D() != 5*time.Second || m.MaxSeries != 50 {
		t.Fatalf("réglages : %+v", m)
	}
	w := c.Integrations.WMI
	if len(w) != 2 || w[0].WMINamespace != `root\cimv2` || w[0].Interval.D() != time.Minute || w[0].Properties[0].Metric != "free_physical_memory" ||
		w[0].Properties[1].Metric != "processus" || w[0].Properties[1].Unit != "count" {
		t.Fatalf("WMI : %+v", w)
	}
	if w[1].WMINamespace != `root\Microsoft\Windows\Storage` || w[1].TagBy[0] != "FriendlyName" {
		t.Fatalf("WMI (conf.d) : %+v", w[1])
	}
	ad, dns := c.Integrations.ActiveDirectory, c.Integrations.WindowsDNS
	if ad.Enabled != "auto" || ad.LogsOn() || dns.Enabled != "false" || dns.LogsOn() || dns.TestZone != "exemple.fr" || dns.Address != "127.0.0.1:53" {
		t.Fatalf("rôles : %+v %+v", ad, dns)
	}
	if d := Default().Integrations; !d.ActiveDirectory.LogsOn() || !d.WindowsDNS.LogsOn() {
		t.Fatal("journaux des rôles coupés par défaut")
	}
}

func TestMetricNameAndCounterPath(t *testing.T) {
	for in, want := range map[string]string{
		"% Processor Time":         "processor_time_pct",
		"Total Query Received/sec": "total_query_received_per_s",
		"FreePhysicalMemory":       "free_physical_memory",
		"Avg. Disk sec/Read":       "avg_disk_sec_read",
		"DRA Inbound Bytes Total":  "dra_inbound_bytes_total",
		"LDAPClientSessions":       "ldap_client_sessions",
		"%":                        "value_pct",
	} {
		if got := MetricName(in); got != want {
			t.Errorf("MetricName(%q) = %q, attendu %q", in, got, want)
		}
	}
	for path, want := range map[string][4]string{
		`\Processor(*)\% Processor Time`:                  {"Processor", "*", "% Processor Time", "ok"},
		`\Memory\Available MBytes`:                        {"Memory", "", "Available MBytes", "ok"},
		`\MSSQL$PROD:Buffer Manager\Page life expectancy`: {"MSSQL$PROD:Buffer Manager", "", "Page life expectancy", "ok"},
		`\Netlogon(\\essai.local)\Semaphore Waiters`:      {"Netlogon", `\\essai.local`, "Semaphore Waiters", "ok"},
		`\\hote\Memory\Available MBytes`:                  {"", "", "", ""},
		`\Memory`:                                         {"", "", "", ""},
		`\Processor()\x`:                                  {"", "", "", ""},
	} {
		o, i, c, ok := SplitCounterPath(path)
		got := [4]string{o, i, c, ""}
		if ok {
			got[3] = "ok"
		}
		if !ok {
			got = [4]string{"", "", "", ""}
		}
		if got != want {
			t.Errorf("SplitCounterPath(%q) = %q, attendu %q", path, got, want)
		}
	}
}
