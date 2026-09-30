package adapters

import (
	"context"
	"fmt"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/extensions"
	tea "github.com/charmbracelet/bubbletea"
)

type InventoryQuery interface {
	Query(context.Context, string) (application.Inventory, error)
}

type RenameCommand interface {
	Execute(context.Context, extensions.RenameRequest) (application.MutationResult, error)
}

type InventoryFeatureState struct {
	Generation uint64
	Group      string
	Loading    bool
	Inventory  application.Inventory
	Error      error
}

type InventoryFeature struct {
	query InventoryQuery
	State InventoryFeatureState
}

func NewInventoryFeature(query InventoryQuery) *InventoryFeature {
	return &InventoryFeature{query: query}
}

func (feature *InventoryFeature) begin(group string) tea.Cmd {
	feature.State.Generation++
	generation := feature.State.Generation
	feature.State.Group = group
	feature.State.Loading = true
	feature.State.Error = nil
	return func() tea.Msg {
		inventory, err := feature.query.Query(context.Background(), group)
		return inventoryLoaded{generation: generation, inventory: inventory, err: err}
	}
}

func (feature *InventoryFeature) apply(message inventoryLoaded) {
	if message.generation != feature.State.Generation {
		return
	}
	feature.State.Loading = false
	feature.State.Error = message.err
	if message.err == nil {
		feature.State.Inventory = message.inventory
	}
}

func (feature *InventoryFeature) invalidate() {
	feature.State.Generation++
	feature.State.Loading = false
	feature.State.Inventory = application.Inventory{}
	feature.State.Error = nil
}

type RenameFeatureState struct {
	Generation uint64
	Loading    bool
	Request    extensions.RenameRequest
	Result     application.MutationResult
	Error      error
}

type RenameFeature struct {
	command RenameCommand
	State   RenameFeatureState
}

func NewRenameFeature(command RenameCommand) *RenameFeature {
	return &RenameFeature{command: command}
}

func (feature *RenameFeature) begin(request extensions.RenameRequest) tea.Cmd {
	feature.State.Generation++
	generation := feature.State.Generation
	feature.State.Request = request
	feature.State.Loading = true
	feature.State.Result = application.MutationResult{}
	feature.State.Error = nil
	return func() tea.Msg {
		result, err := feature.command.Execute(context.Background(), request)
		return fixtureRenamed{generation: generation, result: result, err: err}
	}
}

func (feature *RenameFeature) apply(message fixtureRenamed) {
	if message.generation != feature.State.Generation {
		return
	}
	feature.State.Loading = false
	feature.State.Error = message.err
	if message.err == nil {
		feature.State.Result = message.result
	}
}

func (feature *RenameFeature) invalidate() {
	feature.State.Generation++
	feature.State.Loading = false
	feature.State.Request = extensions.RenameRequest{}
	feature.State.Result = application.MutationResult{}
	feature.State.Error = nil
}

type RootModel struct {
	Inventory *InventoryFeature
	Rename    *RenameFeature
}

func NewRootModel(inventory *InventoryFeature, rename *RenameFeature) *RootModel {
	return &RootModel{Inventory: inventory, Rename: rename}
}

type LoadInventory struct {
	Group string
}

type SubmitFixtureRename struct {
	Request extensions.RenameRequest
}

type InvalidateTargets struct{}

type inventoryLoaded struct {
	generation uint64
	inventory  application.Inventory
	err        error
}

type fixtureRenamed struct {
	generation uint64
	result     application.MutationResult
	err        error
}

func (model *RootModel) Init() tea.Cmd { return nil }

func (model *RootModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case LoadInventory:
		return model, model.Inventory.begin(message.Group)
	case inventoryLoaded:
		model.Inventory.apply(message)
	case SubmitFixtureRename:
		return model, model.Rename.begin(message.Request)
	case fixtureRenamed:
		model.Rename.apply(message)
	case InvalidateTargets:
		model.Inventory.invalidate()
		model.Rename.invalidate()
	}
	return model, nil
}

func (model *RootModel) View() string {
	return fmt.Sprintf("inventory targets=%d loading=%t; rename loading=%t",
		len(model.Inventory.State.Inventory.Targets), model.Inventory.State.Loading, model.Rename.State.Loading)
}
