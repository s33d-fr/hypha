package agent

import (
	"context"
	"time"

	"s33d.fr/hypha/agent/internal/logs"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/openmetrics"
	"s33d.fr/hypha/agent/internal/plugins"
	"s33d.fr/hypha/agent/internal/sender"
	"s33d.fr/hypha/agent/internal/syslog"
)

// Moteurs génériques de la 0.5.0 : lecteur OpenMetrics et greffons, chacun
// dans ses propres boucles (une cible lente ou un greffon long ne retarde ni les métriques
// système ni les intégrations) ; réception syslog, branchée sur la file des journaux.

func (a *Agent) postResults(series []model.Series, checks []model.Check) {
	if len(series) > 0 {
		a.postSeries(series)
	}
	if len(checks) > 0 {
		a.sender.Post(sender.RouteChecks, map[string]any{"checks": checks}, nil)
	}
}

func (a *Agent) setupV05() {
	cfg := a.cfg
	if len(cfg.Integrations.OpenMetrics) > 0 {
		a.omMgr = openmetrics.New(cfg.Integrations.OpenMetrics, openmetrics.Options{
			Version: a.opt.Version, Tags: a.tags, Post: a.postResults, SetStatus: a.status.Set, Log: a.log,
		})
	}
	if len(cfg.Integrations.Plugins) > 0 {
		a.pluginsMgr = plugins.New(cfg.Integrations.Plugins, plugins.Options{
			Tags: a.tags, Post: a.postResults, SetStatus: a.status.Set, Log: a.log, ConfigFiles: cfg.PluginFiles,
		})
	}
	if !cfg.Syslog.Enabled {
		a.status.Set("syslog", "disabled")
	}
}

func (a *Agent) startV05(ctx context.Context, goRun func(func())) {
	if a.omMgr != nil {
		goRun(func() { a.omMgr.Run(ctx) })
	}
	if a.pluginsMgr != nil {
		goRun(func() { a.pluginsMgr.Run(ctx) })
	}
}

// onceV05 : un relevé des cibles OpenMetrics et un lancement de chaque greffon (run --once).
func (a *Agent) onceV05(ctx context.Context) {
	if a.omMgr != nil {
		a.omMgr.Once(ctx)
	}
	if a.pluginsMgr != nil {
		a.pluginsMgr.Once(ctx)
	}
}

// startSyslog : la réception tourne comme une source de journaux (arrêtée avant la file).
func (a *Agent) startSyslog(pipe *logs.Pipeline, start func(run func(context.Context), oneShot func(context.Context) error, flush func())) {
	s := syslog.New(a.cfg.Syslog, pipe.TrySubmit, a.log)
	a.syslogSrv = s
	a.status.Set("syslog", "ok")
	start(s.Run, func(context.Context) error { return nil }, func() {})
}

// flushSyslog : mesures et contrôle de la réception syslog, à chaque intervalle.
func (a *Agent) flushSyslog(now time.Time) ([]model.Series, []model.Check) {
	if a.syslogSrv == nil {
		return nil, nil
	}
	s, c := a.syslogSrv.Flush(now, a.tags)
	a.status.Set("syslog", a.syslogSrv.State())
	return s, c
}

// capabilitiesV05 : cibles, greffons et réception syslog en service.
func (a *Agent) capabilitiesV05() []string {
	var out []string
	if a.omMgr != nil {
		out = append(out, a.omMgr.Active()...)
	}
	if a.pluginsMgr != nil {
		out = append(out, a.pluginsMgr.Active()...)
	}
	if a.syslogSrv != nil {
		out = append(out, "syslog")
	}
	return out
}
