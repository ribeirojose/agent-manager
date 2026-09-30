package adapters

import (
	"context"
	"testing"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/extensions"
)

type blockingInventoryQuery struct {
	started chan struct{}
	release chan struct{}
	result  application.Inventory
}

func (query *blockingInventoryQuery) Query(context.Context, string) (application.Inventory, error) {
	close(query.started)
	<-query.release
	return query.result, nil
}

type blockingRenameCommand struct {
	started chan struct{}
	release chan struct{}
	result  application.MutationResult
}

func (command *blockingRenameCommand) Execute(context.Context, extensions.RenameRequest) (application.MutationResult, error) {
	close(command.started)
	<-command.release
	return command.result, nil
}

func TestTUIUpdateReturnsCommandWithoutRunningInventoryInline(t *testing.T) {
	query := &blockingInventoryQuery{
		started: make(chan struct{}), release: make(chan struct{}),
		result: application.Inventory{Scope: application.ScopeWorkspace},
	}
	root := NewRootModel(NewInventoryFeature(query), NewRenameFeature(&blockingRenameCommand{started: make(chan struct{}), release: make(chan struct{})}))
	model, command := root.Update(LoadInventory{Group: "core"})
	if command == nil {
		t.Fatal("Update omitted async command")
	}
	select {
	case <-query.started:
		t.Fatal("Update ran remote query inline")
	default:
	}
	reply := make(chan any, 1)
	go func() { reply <- command() }()
	<-query.started
	close(query.release)
	message := <-reply
	updated, _ := model.Update(message)
	got := updated.(*RootModel).Inventory.State
	if got.Loading || got.Inventory.Scope != application.ScopeWorkspace || got.Error != nil {
		t.Fatalf("inventory feature state = %+v", got)
	}
}

func TestTUIRejectsRenameReplyAfterTargetInvalidation(t *testing.T) {
	target := application.SessionTarget{Owner: application.ConnectionTarget("lab"), SessionID: "fixture"}
	token := application.ReaderToken{Target: target.Owner, Generation: 3, EnvironmentID: "env", OwnerInstanceID: "owner"}
	rename := &blockingRenameCommand{
		started: make(chan struct{}), release: make(chan struct{}),
		result: application.MutationResult{Target: target, Token: token, Session: application.Session{ID: "fixture", Name: "old-route-result"}},
	}
	root := NewRootModel(NewInventoryFeature(&blockingInventoryQuery{started: make(chan struct{}), release: make(chan struct{})}), NewRenameFeature(rename))
	model, command := root.Update(SubmitFixtureRename{Request: extensions.RenameRequest{Target: target, Token: token, Name: "old-route-result"}})
	reply := make(chan any, 1)
	go func() { reply <- command() }()
	<-rename.started
	model, _ = model.Update(InvalidateTargets{})
	close(rename.release)
	model, _ = model.Update(<-reply)
	state := model.(*RootModel).Rename.State
	if state.Result.Session.ID != "" || state.Loading {
		t.Fatalf("stale mutation reply applied after retarget: %+v", state)
	}
}

func TestTwoTUIFeaturesOwnIndependentState(t *testing.T) {
	query := &blockingInventoryQuery{started: make(chan struct{}), release: make(chan struct{}), result: application.Inventory{Scope: application.ScopeDevice}}
	rename := &blockingRenameCommand{started: make(chan struct{}), release: make(chan struct{})}
	root := NewRootModel(NewInventoryFeature(query), NewRenameFeature(rename))
	model, command := root.Update(LoadInventory{})
	go func() { _ = command() }()
	<-query.started
	state := model.(*RootModel)
	if !state.Inventory.State.Loading || state.Rename.State.Loading {
		t.Fatalf("feature state leaked: inventory=%+v rename=%+v", state.Inventory.State, state.Rename.State)
	}
	close(query.release)
}
