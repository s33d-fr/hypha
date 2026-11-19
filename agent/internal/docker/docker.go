// Package docker interroge le démon Docker par sa socket Unix (API HTTP, sans le SDK) :
// instantané des conteneurs (contrat § 3.8) et métriques container.*.
//
// Seules des requêtes GET sont faites : l'agent ne pilote jamais Docker.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// DefaultSocket : socket du démon Docker sous Linux.
const DefaultSocket = "/var/run/docker.sock"

// Collector garde les compteurs précédents de chaque conteneur (CPU et réseau sont des
// cumuls : le débit se calcule entre deux relevés).
type Collector struct {
	socket string
	client *http.Client
	mu     sync.Mutex
	prev   map[string]prevStats
}

type prevStats struct {
	at       time.Time
	cpuNs    uint64
	rx, tx   uint64
	hasValue bool
}

// New crée le collecteur.
func New(socket string) *Collector {
	return &Collector{
		socket: socket,
		prev:   map[string]prevStats{},
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
				MaxIdleConns:    2,
				IdleConnTimeout: 60 * time.Second,
			},
		},
	}
}

// Available dit si la socket existe (containers.enabled: auto).
func Available(socket string) bool {
	fi, err := os.Stat(socket)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

type listItem struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

type statsResp struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

func (c *Collector) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker %s : HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(v)
}

// Collect rend l'instantané et les séries container.* des conteneurs en marche.
// hostMem sert à reconnaître « sans limite » : Docker rapporte alors la mémoire de l'hôte.
func (c *Collector) Collect(ctx context.Context, now time.Time, hostMem uint64, tags model.TagFunc) ([]model.Container, []model.Series, error) {
	var list []listItem
	if err := c.get(ctx, "/containers/json?all=1", &list); err != nil {
		return nil, nil, err
	}
	out := make([]model.Container, len(list))
	stats := make([]*statsResp, len(list))

	// Une requête de stats par conteneur en marche, 4 à la fois au plus.
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, it := range list {
		if it.State != "running" {
			continue
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var s statsResp
			// one-shot : réponse immédiate, sans l'attente d'une seconde que fait Docker
			// pour calculer lui-même un pourcentage ; on calcule entre nos propres relevés.
			if err := c.get(ctx, "/containers/"+id+"/stats?stream=false&one-shot=true", &s); err == nil {
				stats[i] = &s
			}
		}(i, it.ID)
	}
	wg.Wait()

	b := model.NewSeriesBuilder(model.Ms(now), tags)
	c.mu.Lock()
	defer c.mu.Unlock()
	next := map[string]prevStats{}
	for i, it := range list {
		name := it.ID
		if len(it.Names) > 0 {
			name = strings.TrimPrefix(it.Names[0], "/")
		}
		labels := it.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		ct := model.Container{ID: it.ID, Name: name, Image: it.Image, State: it.State, Labels: labels}
		if s := stats[i]; s != nil {
			// Mémoire comme `docker stats` : usage moins le cache de pages inactif.
			usage := s.MemoryStats.Usage
			inactive := s.MemoryStats.Stats["inactive_file"] // cgroup v2
			if v, ok := s.MemoryStats.Stats["total_inactive_file"]; ok {
				inactive = v // cgroup v1
			}
			if inactive < usage {
				usage -= inactive
			}
			limit := s.MemoryStats.Limit
			if hostMem > 0 && limit >= hostMem {
				limit = 0 // pas de limite propre au conteneur
			}
			var rx, tx uint64
			for _, n := range s.Networks {
				rx += n.RxBytes
				tx += n.TxBytes
			}
			cur := prevStats{at: now, cpuNs: s.CPUStats.CPUUsage.TotalUsage, rx: rx, tx: tx, hasValue: true}
			if p, ok := c.prev[it.ID]; ok && p.hasValue {
				dt := now.Sub(p.at).Seconds()
				if dt > 0 {
					if cur.cpuNs >= p.cpuNs {
						// % d'un cœur, comme cpu_pct des processus.
						ct.CPUPct = float64(cur.cpuNs-p.cpuNs) / (dt * 1e9) * 100
					}
					if rx >= p.rx {
						ct.NetRxBps = uint64(float64(rx-p.rx) / dt)
					}
					if tx >= p.tx {
						ct.NetTxBps = uint64(float64(tx-p.tx) / dt)
					}
				}
				kv := []string{"container_name", name, "image", it.Image}
				b.Gauge("container.cpu.usage", "percent", ct.CPUPct, kv...)
				b.Gauge("container.net.rcvd", "byte_per_second", float64(ct.NetRxBps), kv...)
				b.Gauge("container.net.sent", "byte_per_second", float64(ct.NetTxBps), kv...)
			}
			next[it.ID] = cur
			ct.MemUsage, ct.MemLimit = usage, limit
			kv := []string{"container_name", name, "image", it.Image}
			b.Gauge("container.mem.usage", "byte", float64(usage), kv...)
			b.Gauge("container.mem.limit", "byte", float64(limit), kv...)
			ct.CPUPct = float64(int64(ct.CPUPct*100+0.5)) / 100
		}
		out[i] = ct
	}
	c.prev = next
	return out, b.Series(), nil
}
