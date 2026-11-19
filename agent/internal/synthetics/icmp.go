package synthetics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// errICMPUnavailable : l'écho ICMP est impossible sur cet hôte ; le test est ignoré (motif
// dans l'état du collecteur) plutôt que compté en échec.
var errICMPUnavailable = errors.New("icmp indisponible")

// probeICMP : count échos (3 par défaut), délai par écho timeout_s (5 s) ; latence = moyenne.
func probeICMP(ctx context.Context, target string, cfg map[string]any) Result {
	timeout := dialTimeout(cfg, 5)
	count := int(cfgFloat(cfg, "count", 3))
	count = min(max(count, 1), 10)
	host, _, err := splitHostPort(target, 0)
	if err != nil {
		return Result{Error: "écho ICMP impossible : " + err.Error()}
	}
	addr, dnsMS, err := resolveFirst(ctx, host)
	if err != nil {
		return Result{Error: err.Error()}
	}
	ip := net.ParseIP(addr)
	rtts, err := ping(ctx, ip, count, timeout)
	if errors.Is(err, errICMPUnavailable) {
		return Result{Skip: err.Error()}
	}
	if err != nil {
		return Result{Error: "écho ICMP impossible : " + err.Error()}
	}
	details := map[string]any{"dns_ms": dnsMS, "address": addr, "packets_sent": count, "packets_received": len(rtts),
		"packet_loss_pct": round1(100 * float64(count-len(rtts)) / float64(count))}
	if len(rtts) == 0 {
		return Result{Error: "aucune réponse à l'écho ICMP", Details: details}
	}
	lo, hi, sum := rtts[0], rtts[0], 0.0
	for _, r := range rtts {
		lo, hi, sum = min(lo, r), max(hi, r), sum+r
	}
	avg := round2(sum / float64(len(rtts)))
	details["rtt_min_ms"], details["rtt_avg_ms"], details["rtt_max_ms"] = round2(lo), avg, round2(hi)
	return Result{OK: true, LatencyMS: &avg, Details: details}
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func unavailable(why string) error { return fmt.Errorf("%w : %s", errICMPUnavailable, why) }

// sleepCtx attend d, ou l'annulation.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
