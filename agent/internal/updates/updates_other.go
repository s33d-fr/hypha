//go:build !linux && !windows

package updates

import (
	"context"
	"errors"
)

func collect(context.Context) Result {
	return Result{Err: errors.New("mises à jour non relevées sur ce système")}
}

func quickReboot() (bool, bool) { return false, false }
