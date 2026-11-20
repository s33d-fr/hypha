//go:build !windows

package logs

import (
	"context"
	"log/slog"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/state"
)

// Eventlog n'existe que sous Windows.
type Eventlog struct{}

// EventlogAvailable : jamais hors Windows.
func EventlogAvailable() bool { return false }

// NewEventlog rend un lecteur inerte.
func NewEventlog([]config.EventlogChannel, *Pipeline, *state.Store, *slog.Logger, func(string)) *Eventlog {
	return &Eventlog{}
}

// Run ne fait rien.
func (*Eventlog) Run(context.Context) {}

// Once ne fait rien.
func (*Eventlog) Once(context.Context) error { return nil }

// FlushState ne fait rien.
func (*Eventlog) FlushState() {}

// EventlogChannels rend les canaux de la conf tels quels (aucun rôle Windows hors Windows).
func EventlogChannels(list []config.EventlogChannel, _ config.IntegrationsConfig, _ *slog.Logger) []config.EventlogChannel {
	return list
}
