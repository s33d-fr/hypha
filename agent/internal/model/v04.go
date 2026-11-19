package model

// Corps de la 0.4.0 (docs/CONTRAT-04.md).

// Inventory : corps de /api/v1/intake/inventory (§ 15), hors objet agent et ts.
type Inventory struct {
	Hardware Hardware  `json:"hardware"`
	OS       OSInfo    `json:"os"`
	Updates  *Updates  `json:"updates,omitempty"` // absent si la liste n'a pas pu être lue
	Packages []Package `json:"packages"`
}

// Hardware : matériel de l'hôte.
type Hardware struct {
	Vendor        string         `json:"vendor"`
	Product       string         `json:"product"`
	Serial        string         `json:"serial"`
	BIOSVendor    string         `json:"bios_vendor"`
	BIOSVersion   string         `json:"bios_version"`
	BIOSDate      string         `json:"bios_date"`
	CPU           CPUInfo        `json:"cpu"`
	MemoryModules []MemoryModule `json:"memory_modules"`
	Disks         []Disk         `json:"disks"`
	Virtual       string         `json:"virtual"` // kvm, virtualbox, vmware, hyperv, xen, docker…, none
}

// CPUInfo : processeurs.
type CPUInfo struct {
	Model   string `json:"model"`
	Sockets int    `json:"sockets"`
	Cores   int    `json:"cores"`
	Threads int    `json:"threads"`
}

// MemoryModule : une barrette de mémoire.
type MemoryModule struct {
	Size     uint64 `json:"size"`
	Type     string `json:"type"`
	SpeedMHz int    `json:"speed_mhz"`
	Slot     string `json:"slot"`
}

// Disk : un disque physique.
type Disk struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Serial     string `json:"serial"`
	Size       uint64 `json:"size"`
	Rotational bool   `json:"rotational"`
	SMART      string `json:"smart,omitempty"` // PASSED, FAILED, ou vide si inconnu
}

// OSInfo : système d'exploitation.
type OSInfo struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Codename       string `json:"codename"`
	Kernel         string `json:"kernel"`
	InstalledAt    int64  `json:"installed_at,omitempty"`
	LastBoot       int64  `json:"last_boot"`
	RebootRequired bool   `json:"reboot_required"`
	Timezone       string `json:"timezone"`
}

// Updates : mises à jour en attente.
type Updates struct {
	Pending  int             `json:"pending"`
	Security int             `json:"security"`
	List     []PendingUpdate `json:"list"`
}

// PendingUpdate : une mise à jour en attente.
type PendingUpdate struct {
	Name      string `json:"name"`
	Current   string `json:"current"`
	Candidate string `json:"candidate"`
	Security  bool   `json:"security"`
}

// Package : un logiciel installé.
type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	Source  string `json:"source"`
}

// SyntheticResult : un résultat de /api/v1/intake/synthetics (§ 29).
type SyntheticResult struct {
	TestID     int64          `json:"test_id"`
	TS         int64          `json:"ts"`
	OK         bool           `json:"ok"`
	StatusCode *int           `json:"status_code"`
	LatencyMS  *float64       `json:"latency_ms"`
	Error      *string        `json:"error"`
	Details    map[string]any `json:"details"`
	Location   string         `json:"location"`
}

// DBMQuery : une requête de /api/v1/intake/dbm (§ 30), écarts depuis l'échantillon précédent.
type DBMQuery struct {
	Engine         string  `json:"engine"`
	Instance       string  `json:"instance"`
	DB             string  `json:"db"`
	QueryID        string  `json:"query_id"`
	Query          string  `json:"query"`
	Calls          int64   `json:"calls"`
	TotalMS        float64 `json:"total_ms"`
	Rows           int64   `json:"rows"`
	SharedBlksHit  int64   `json:"shared_blks_hit"`
	SharedBlksRead int64   `json:"shared_blks_read"`
}
