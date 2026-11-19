//go:build !linux

package logs

import (
	"context"
	"log/slog"

	"s33d.fr/hypha/agent/internal/state"
)

// Journald n'existe que sous Linux.
type Journald struct{}

// JournaldAvailable : jamais hors Linux.
func JournaldAvailable() bool { return false }

// NewJournald rend un lecteur inerte.
func NewJournald(*Pipeline, *state.Store, *slog.Logger, func(string)) *Journald { return &Journald{} }

// Run ne fait rien.
func (*Journald) Run(context.Context) {}

// Once ne fait rien.
func (*Journald) Once(context.Context) error { return nil }

// FlushState ne fait rien.
func (*Journald) FlushState() {}
