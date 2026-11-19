//go:build !linux

package integrations

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// DialChildCommand : sous-commande cachée, sans objet hors Linux.
const DialChildCommand = "__pg-dial"

var errNoPeer = errors.New("connexion par socket locale réservée à Linux : renseigner integrations.postgres.dsn")

// peerDialer : l'authentification peer n'existe que sous Linux ; ailleurs, un dsn est requis.
func peerDialer(string) (pgconn.DialFunc, error) { return nil, errNoPeer }

// RunDialChild n'existe que sous Linux.
func RunDialChild(string) error { return errNoPeer }
