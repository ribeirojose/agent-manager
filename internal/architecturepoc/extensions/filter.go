package extensions

import (
	"context"
	"strings"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

type InventoryReader interface {
	Inventory(context.Context) (application.Inventory, error)
}

type FilteredInventory struct {
	reader InventoryReader
}

func NewFilteredInventory(reader InventoryReader) *FilteredInventory {
	return &FilteredInventory{reader: reader}
}

func (query *FilteredInventory) Query(ctx context.Context, group string) (application.Inventory, error) {
	inventory, err := query.reader.Inventory(ctx)
	if err != nil {
		return application.Inventory{}, err
	}
	group = strings.TrimSpace(group)
	if group == "" {
		return inventory, nil
	}
	targets := append([]application.TargetProjection(nil), inventory.Targets...)
	inventory.Targets = targets
	for targetIndex := range inventory.Targets {
		target := &inventory.Targets[targetIndex]
		filtered := make([]application.SessionProjection, 0, len(target.Sessions))
		for _, session := range target.Sessions {
			if session.Session.Group == group {
				filtered = append(filtered, session)
			}
		}
		target.Sessions = filtered
	}
	return inventory, nil
}
