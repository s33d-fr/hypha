package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── RabbitMQ (§ 39) : API de gestion (overview, nodes, queues) ─────────────────────────────
//
// Compte au rôle « monitoring » ; sans compte, guest/guest, que RabbitMQ n'accepte que depuis la
// machine elle-même. Alarmes mémoire et disque d'un nœud (publications bloquées) en CRIT ;
// messages en attente, sans accusé, débits et consommateurs par file (200 files au plus).

const rabbitMaxQueues = 200

type RabbitMQ struct {
	cfg    config.RabbitMQConfig
	det    detector
	client *http.Client
}

func newRabbitMQ(cfg config.RabbitMQConfig) *RabbitMQ {
	mode := cfg.Enabled
	if mode == "auto" && (cfg.Username != "" || (cfg.URL != "" && cfg.URL != "http://127.0.0.1:15672")) {
		mode = "true"
	}
	return &RabbitMQ{cfg: cfg, det: detector{mode: mode}, client: &http.Client{Timeout: 10 * time.Second}}
}

func (r *RabbitMQ) name() string          { return "rabbitmq" }
func (r *RabbitMQ) period() time.Duration { return 30 * time.Second }
func (r *RabbitMQ) close()                {}

func (r *RabbitMQ) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.cfg.URL, "/")+path, nil)
	if err != nil {
		return err
	}
	user, pass := r.cfg.Username, r.cfg.Password
	if user == "" {
		user, pass = "guest", "guest"
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("User-Agent", "hypha-agent")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("compte %q refusé (HTTP 401)", user)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

type rabbitRate struct {
	Rate float64 `json:"rate"`
}

type rabbitOverview struct {
	Version     string `json:"rabbitmq_version"`
	ClusterName string `json:"cluster_name"`
	Objects     struct {
		Queues      float64 `json:"queues"`
		Connections float64 `json:"connections"`
		Channels    float64 `json:"channels"`
		Consumers   float64 `json:"consumers"`
	} `json:"object_totals"`
	Queue struct {
		Messages float64 `json:"messages"`
		Ready    float64 `json:"messages_ready"`
		Unacked  float64 `json:"messages_unacknowledged"`
	} `json:"queue_totals"`
	Stats struct {
		Publish     rabbitRate `json:"publish_details"`
		DeliverGet  rabbitRate `json:"deliver_get_details"`
		Ack         rabbitRate `json:"ack_details"`
		Redeliver   rabbitRate `json:"redeliver_details"`
		Unroutable  rabbitRate `json:"drop_unroutable_details"`
	} `json:"message_stats"`
}

type rabbitNode struct {
	Name          string  `json:"name"`
	Running       bool    `json:"running"`
	MemUsed       float64 `json:"mem_used"`
	MemLimit      float64 `json:"mem_limit"`
	MemAlarm      bool    `json:"mem_alarm"`
	DiskFree      float64 `json:"disk_free"`
	DiskFreeLimit float64 `json:"disk_free_limit"`
	DiskAlarm     bool    `json:"disk_free_alarm"`
	FDUsed        float64 `json:"fd_used"`
	FDTotal       float64 `json:"fd_total"`
	Partitions    []any   `json:"partitions"`
}

type rabbitQueue struct {
	Name      string  `json:"name"`
	Vhost     string  `json:"vhost"`
	Messages  float64 `json:"messages"`
	Ready     float64 `json:"messages_ready"`
	Unacked   float64 `json:"messages_unacknowledged"`
	Consumers float64 `json:"consumers"`
	State     string  `json:"state"`
}

func (r *RabbitMQ) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := r.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	kv := []string{"instance", r.cfg.URL}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var ov rabbitOverview
	if err := r.get(ctx, "/api/overview", &ov); err != nil {
		if silent, st := r.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "rabbitmq.can_connect", Status: model.CheckCrit, Message: err.Error(), Tags: tags(kv...)}},
			"erreur : " + err.Error()
	}
	r.det.ok()
	g := func(name, unit string, v float64, extra ...string) {
		b.Gauge(name, unit, v, append(append([]string{}, kv...), extra...)...)
	}
	g("rabbitmq.queues", "queue", ov.Objects.Queues)
	g("rabbitmq.connections", "connection", ov.Objects.Connections)
	g("rabbitmq.channels", "channel", ov.Objects.Channels)
	g("rabbitmq.consumers", "consumer", ov.Objects.Consumers)
	g("rabbitmq.messages", "message", ov.Queue.Messages)
	g("rabbitmq.messages.ready", "message", ov.Queue.Ready)
	g("rabbitmq.messages.unacked", "message", ov.Queue.Unacked)
	g("rabbitmq.messages.publish_rate", "message/second", ov.Stats.Publish.Rate)
	g("rabbitmq.messages.deliver_rate", "message/second", ov.Stats.DeliverGet.Rate)
	g("rabbitmq.messages.ack_rate", "message/second", ov.Stats.Ack.Rate)
	g("rabbitmq.messages.redeliver_rate", "message/second", ov.Stats.Redeliver.Rate)
	g("rabbitmq.messages.unroutable_rate", "message/second", ov.Stats.Unroutable.Rate)

	checks := []model.Check{}
	var nodes []rabbitNode
	if err := r.get(ctx, "/api/nodes", &nodes); err == nil {
		for _, n := range nodes {
			nkv := append(append([]string{}, kv...), "node", n.Name)
			if n.MemLimit > 0 {
				b.Gauge("rabbitmq.node.mem.usage", "percent", round1(100*n.MemUsed/n.MemLimit), nkv...)
			}
			b.Gauge("rabbitmq.node.disk_free", "byte", n.DiskFree, nkv...)
			if n.FDTotal > 0 {
				b.Gauge("rabbitmq.node.fd.usage", "percent", round1(100*n.FDUsed/n.FDTotal), nkv...)
			}
			st, msg := model.CheckOK, "nœud en marche"
			var alarms []string
			if n.MemAlarm {
				alarms = append(alarms, "alarme mémoire")
			}
			if n.DiskAlarm {
				alarms = append(alarms, "alarme disque")
			}
			switch {
			case !n.Running:
				st, msg = model.CheckCrit, "nœud arrêté"
			case len(alarms) > 0:
				st, msg = model.CheckCrit, strings.Join(alarms, ", ")+" : publications bloquées"
			case len(n.Partitions) > 0:
				st, msg = model.CheckCrit, fmt.Sprintf("partition réseau avec %d nœud(s)", len(n.Partitions))
			}
			checks = append(checks, model.Check{TS: ts, Name: "rabbitmq.node", Status: st, Message: n.Name + " : " + msg, Tags: tags(nkv...)})
		}
	}
	var queues []rabbitQueue
	if err := r.get(ctx, "/api/queues?columns=name,vhost,messages,messages_ready,messages_unacknowledged,consumers,state", &queues); err == nil {
		sort.Slice(queues, func(i, j int) bool { return queues[i].Messages > queues[j].Messages })
		if len(queues) > rabbitMaxQueues {
			queues = queues[:rabbitMaxQueues]
		}
		for _, q := range queues {
			qkv := []string{"queue", q.Name, "vhost", q.Vhost}
			g("rabbitmq.queue.messages", "message", q.Messages, qkv...)
			g("rabbitmq.queue.messages.ready", "message", q.Ready, qkv...)
			g("rabbitmq.queue.messages.unacked", "message", q.Unacked, qkv...)
			g("rabbitmq.queue.consumers", "consumer", q.Consumers, qkv...)
		}
	}
	name := ov.ClusterName
	if name == "" {
		name = "RabbitMQ"
	}
	checks = append(checks, model.Check{TS: ts, Name: "rabbitmq.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("RabbitMQ %s joignable (%s)", ov.Version, name), Tags: tags(kv...)})
	return checks, "ok"
}
