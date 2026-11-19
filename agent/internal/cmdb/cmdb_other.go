//go:build !linux && !windows

package cmdb

import (
	"context"
	"errors"

	"s33d.fr/hypha/agent/internal/model"
)

// Collect : inventaire non pris en charge hors Linux et Windows.
func Collect(context.Context, bool) (model.Inventory, []error) {
	return model.Inventory{Packages: []model.Package{}}, []error{errors.New("inventaire non pris en charge sur ce système")}
}
