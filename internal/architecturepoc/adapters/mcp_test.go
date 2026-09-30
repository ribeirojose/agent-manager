package adapters

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpStubApplication struct {
	inventory application.Inventory
	renamed   application.MutationResult
	target    application.SessionTarget
	token     application.ReaderToken
	name      string
}

func (stub *mcpStubApplication) Inventory(context.Context) (application.Inventory, error) {
	return stub.inventory, nil
}

func (stub *mcpStubApplication) RenameFixture(_ context.Context, target application.SessionTarget, token application.ReaderToken, name string) (application.MutationResult, error) {
	stub.target, stub.token, stub.name = target, token, name
	return stub.renamed, nil
}

func connectMCP(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(t.Context(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "architecture-poc-test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestMCPUsesRealSDKToolsForFilteredInventoryAndRename(t *testing.T) {
	token := application.ReaderToken{
		Target: application.ConnectionTarget("lab"), Generation: 2, RefreshSequence: 1,
		EnvironmentID: "env", OwnerInstanceID: "owner", Revision: 3, CanRenameFixture: true,
	}
	target := application.SessionTarget{Owner: token.Target, SessionID: "edge"}
	stub := &mcpStubApplication{
		inventory: application.Inventory{Scope: application.ScopeWorkspace, Targets: []application.TargetProjection{{
			Target: token.Target, Label: "Lab", Token: token,
			Sessions: []application.SessionProjection{
				{Target: application.SessionTarget{Owner: token.Target, SessionID: "core"}, Token: token, Session: application.Session{ID: "core", Group: "core"}},
				{Target: target, Token: token, Session: application.Session{ID: "edge", Group: "edge", InventoryOnly: true}},
			},
		}}},
		renamed: application.MutationResult{Target: target, Token: token, Session: application.Session{ID: "edge", Name: "Renamed", Group: "edge", InventoryOnly: true}},
	}
	session := connectMCP(t, NewMCPServer(stub))
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"inventory", "rename_fixture"}) {
		t.Fatalf("tools = %v", names)
	}

	listed, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "inventory", Arguments: map[string]any{"group": "edge"}})
	if err != nil || listed.IsError {
		t.Fatalf("inventory call = %+v, %v", listed, err)
	}
	payload, err := json.Marshal(listed.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var inventoryOutput InventoryToolOutput
	if err := json.Unmarshal(payload, &inventoryOutput); err != nil {
		t.Fatal(err)
	}
	if inventoryOutput.Inventory == nil || len(inventoryOutput.Inventory.Targets) != 1 || len(inventoryOutput.Inventory.Targets[0].Sessions) != 1 || inventoryOutput.Inventory.Targets[0].Sessions[0].Session.ID != "edge" {
		t.Fatalf("filtered MCP inventory = %+v", inventoryOutput)
	}
	encodedToken, err := EncodeReaderToken(token)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "rename_fixture", Arguments: map[string]any{
		"target_id": EncodeSessionTarget(target), "token": encodedToken, "name": "Renamed",
	}})
	if err != nil || renamed.IsError {
		t.Fatalf("rename call = %+v, %v", renamed, err)
	}
	renamePayload, err := json.Marshal(renamed.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var renameOutput RenameToolOutput
	if err := json.Unmarshal(renamePayload, &renameOutput); err != nil {
		t.Fatal(err)
	}
	if renameOutput.Mutation == nil || renameOutput.Mutation.TargetID != "connection/lab/edge" || stub.target != target || stub.token != token || stub.name != "Renamed" {
		t.Fatalf("rename output=%+v call=%+v/%+v/%q", renameOutput, stub.target, stub.token, stub.name)
	}
}

type mcpMemoryRepository struct {
	state application.WorkspaceState
}

func (repository *mcpMemoryRepository) View(_ context.Context, work func(application.WorkspaceState) error) error {
	return work(repository.state)
}

func (repository *mcpMemoryRepository) Update(_ context.Context, work func(*application.WorkspaceState) error) error {
	return work(&repository.state)
}

type mcpOwner struct {
	info     application.OwnerInfo
	sessions []application.Session
}

func (owner *mcpOwner) Inspect(context.Context) (application.OwnerInfo, error) {
	return owner.info, nil
}
func (owner *mcpOwner) List(context.Context) (application.OwnerInventory, error) {
	return application.OwnerInventory{Owner: owner.info, Sessions: owner.sessions}, nil
}
func (owner *mcpOwner) CreateBound(context.Context, application.MutationGuard, application.FixtureDraft) (application.OwnerMutation, error) {
	return application.OwnerMutation{}, nil
}
func (owner *mcpOwner) Rename(context.Context, application.MutationGuard, string, string) (application.OwnerMutation, error) {
	return application.OwnerMutation{}, nil
}

type mcpResolver map[string]*mcpOwner

func (resolver mcpResolver) Open(_ context.Context, endpoint application.Endpoint) (application.OwnerClient, error) {
	return resolver[endpoint.Profile], nil
}

func TestLongLivedDeviceMCPRemainsLocalAfterOutboundConnectionIsSaved(t *testing.T) {
	local := &mcpOwner{info: application.OwnerInfo{InstanceID: "local-owner", EnvironmentID: "local-env", Revision: 3, Capabilities: []string{application.CapabilitySessionsRead}}, sessions: []application.Session{{ID: "same", Group: "core"}}}
	remote := &mcpOwner{info: application.OwnerInfo{InstanceID: "remote-owner", EnvironmentID: "remote-env", Revision: 3, Capabilities: []string{application.CapabilitySessionsRead}}, sessions: []application.Session{{ID: "same", Group: "core"}}}
	repository := &mcpMemoryRepository{state: application.WorkspaceState{DeviceID: "local", NextGeneration: 1}}
	workspace := application.NewWorkspace(repository, mcpResolver{"local": local, "remote": remote}, application.Endpoint{Profile: "local"}, func() time.Time { return time.Unix(1, 0).UTC() })
	session := connectMCP(t, NewMCPServer(workspace.DeviceClient()))
	if _, err := workspace.Connect(t.Context(), application.NewConnection{ID: "lab", Label: "Lab", Endpoint: application.Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(t.Context(), "lab"); err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "inventory"})
	if err != nil || result.IsError {
		t.Fatalf("inventory = %+v, %v", result, err)
	}
	payload, _ := json.Marshal(result.StructuredContent)
	var output InventoryToolOutput
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatal(err)
	}
	if output.Inventory == nil || output.Inventory.Scope != application.ScopeDevice || len(output.Inventory.Targets) != 1 || output.Inventory.Targets[0].TargetID != "device" {
		t.Fatalf("long-lived device MCP traversed outbound connection: %+v", output.Inventory)
	}
}

func TestMCPReturnsStableStructuredDomainErrors(t *testing.T) {
	target := application.SessionTarget{Owner: application.ConnectionTarget("lab"), SessionID: "fixture"}
	token := application.ReaderToken{Target: target.Owner, Generation: 1, EnvironmentID: "env", OwnerInstanceID: "owner", CanRenameFixture: true}
	stub := &mcpStubApplication{}
	server := NewMCPServer(mcpFailingApplication{mcpStubApplication: stub})
	session := connectMCP(t, server)
	encoded, err := EncodeReaderToken(token)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "rename_fixture", Arguments: map[string]any{
		"target_id": EncodeSessionTarget(target), "token": encoded, "name": "blocked",
	}})
	if err != nil || !result.IsError {
		t.Fatalf("domain error call = %+v, %v", result, err)
	}
	payload, _ := json.Marshal(result.StructuredContent)
	var output RenameToolOutput
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatal(err)
	}
	if output.Error == nil || output.Error.Code != application.CodeScopeViolation {
		t.Fatalf("structured domain error = %s (%+v)", payload, output)
	}
}

type mcpFailingApplication struct {
	*mcpStubApplication
}

func (mcpFailingApplication) RenameFixture(context.Context, application.SessionTarget, application.ReaderToken, string) (application.MutationResult, error) {
	return application.MutationResult{}, application.NewError(application.CodeScopeViolation, "device client cannot traverse connection", nil)
}
