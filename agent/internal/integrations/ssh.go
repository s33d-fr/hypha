package integrations

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── SSH : disponibilité et bannière (aucune authentification) ───────────────────────────────
//
// Connexion TCP, lecture de la bannière (« SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5 »), envoi de
// l'identification de l'agent puis fermeture : le serveur journalise une connexion fermée avant
// authentification, sans tentative de mot de passe. ssh.can_connect par cible : CRIT si le port
// ne répond pas ou ne parle pas SSH, WARN si la bannière ne vérifie pas expect_banner ou annonce
// le protocole 1.

type SSHProbe struct {
	cfg config.SSHConfig
	det detector
	res []*regexp.Regexp
}

func newSSHProbe(cfg config.SSHConfig) *SSHProbe {
	s := &SSHProbe{cfg: cfg, det: detector{mode: intent(cfg.Enabled, len(cfg.Targets) > 0)}}
	for _, t := range cfg.Targets {
		var re *regexp.Regexp
		if t.ExpectBanner != "" {
			re, _ = regexp.Compile(t.ExpectBanner)
		}
		s.res = append(s.res, re)
	}
	return s
}

func (s *SSHProbe) name() string          { return "ssh" }
func (s *SSHProbe) period() time.Duration { return 60 * time.Second }
func (s *SSHProbe) close()                {}

// sshBanner lit la bannière (RFC 4253 : des lignes peuvent la précéder) et le temps de réponse.
func sshBanner(ctx context.Context, address string, timeout time.Duration) (string, time.Duration, error) {
	start := time.Now()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	rd := bufio.NewReaderSize(conn, 512)
	for i := 0; i < 10; i++ {
		line, err := rd.ReadString('\n')
		if err != nil {
			if line == "" {
				return "", 0, fmt.Errorf("aucune bannière SSH reçue")
			}
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "SSH-") {
			elapsed := time.Since(start)
			_, _ = conn.Write([]byte("SSH-2.0-HYPHA_agent\r\n"))
			return model.Truncate(line, 255), elapsed, nil
		}
		if err != nil {
			break
		}
	}
	return "", 0, fmt.Errorf("le service ne parle pas SSH")
}

func (s *SSHProbe) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := s.det.skip(now); skip {
		return nil, st
	}
	if len(s.cfg.Targets) == 0 {
		return nil, "absent"
	}
	s.det.ok()
	ts := model.Ms(now)
	var checks []model.Check
	failed := 0
	for i, t := range s.cfg.Targets {
		kv := []string{"target", t.Name, "address", t.Address}
		for k, v := range t.Tags {
			kv = append(kv, k, v)
		}
		banner, rt, err := sshBanner(ctx, t.Address, t.Timeout.D())
		if err != nil {
			failed++
			b.Gauge("ssh.up", "", 0, kv...)
			checks = append(checks, model.Check{TS: ts, Name: "ssh.can_connect", Status: model.CheckCrit,
				Message: fmt.Sprintf("%s : %v", t.Address, cleanNetErr(err)), Tags: tags(kv...)})
			continue
		}
		b.Gauge("ssh.up", "", 1, kv...)
		b.Gauge("ssh.response_time", "second", rt.Seconds(), kv...)
		st, msg := model.CheckOK, fmt.Sprintf("%s en %d ms", strings.TrimPrefix(banner, "SSH-2.0-"), rt.Milliseconds())
		switch {
		case !strings.HasPrefix(banner, "SSH-2.0-") && !strings.HasPrefix(banner, "SSH-1.99-"):
			st, msg = model.CheckWarn, "protocole SSH 1 annoncé : "+banner
		case s.res[i] != nil && !s.res[i].MatchString(banner):
			st, msg = model.CheckWarn, "bannière inattendue : "+banner
		}
		checks = append(checks, model.Check{TS: ts, Name: "ssh.can_connect", Status: st, Message: msg, Tags: tags(kv...)})
	}
	if failed > 0 {
		return checks, fmt.Sprintf("partiel : %d cible(s) injoignable(s)", failed)
	}
	return checks, "ok"
}
