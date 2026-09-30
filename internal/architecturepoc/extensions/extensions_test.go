package extensions

import (
	"context"
	"testing"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

type stubClient struct {
	inventory application.Inventory
	target    application.SessionTarget
	token     application.ReaderToken
	name      string
}

func (client *stubClient) Inventory(context.Context) (application.Inventory, error) {
	return client.inventory, nil
}

func (client *stubClient) RenameFixture(_ context.Context, target application.SessionTarget, token application.ReaderToken, name string) (application.MutationResult, error) {
	client.target, client.token, client.name = target, token, name
	session := application.Session{ID: target.SessionID, Name: name, InventoryOnly: true}
	return application.MutationResult{Target: target, Token: token, Session: session}, nil
}

func TestFilteredInventoryIsAnExtensionOverTheNarrowReadPort(t *testing.T) {
	client := &stubClient{inventory: application.Inventory{
		Scope: application.ScopeWorkspace,
		Targets: []application.TargetProjection{{
			Target: application.DeviceTarget(),
			Sessions: []application.SessionProjection{
				{Session: application.Session{ID: "core", Group: "core"}},
				{Session: application.Session{ID: "edge", Group: "edge"}},
			},
		}},
	}}
	filtered, err := NewFilteredInventory(client).Query(context.Background(), "edge")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Targets) != 1 || len(filtered.Targets[0].Sessions) != 1 || filtered.Targets[0].Sessions[0].Session.ID != "edge" {
		t.Fatalf("filtered inventory = %+v", filtered)
	}
	if len(client.inventory.Targets[0].Sessions) != 2 {
		t.Fatal("query mutated the source projection")
	}
}

func TestFixtureRenameIsAnExtensionOverTheNarrowCommandPort(t *testing.T) {
	client := &stubClient{}
	target := application.SessionTarget{Owner: application.ConnectionTarget("lab"), SessionID: "fixture"}
	token := application.ReaderToken{Target: target.Owner, Generation: 7}
	renamed, err := NewFixtureRename(client).Execute(context.Background(), RenameRequest{Target: target, Token: token, Name: "renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Session.Name != "renamed" || renamed.Target != target || client.target != target || client.token != token || client.name != "renamed" {
		t.Fatalf("rename result=%+v call=%+v/%+v/%q", renamed, client.target, client.token, client.name)
	}
}
