package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/adapters"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/extensions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCLIConnectRefreshInspectAndRenameShareOneApplicationContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("disposable owner script uses /bin/sh")
	}
	dir := t.TempDir()
	owner := filepath.Join(dir, "owner")
	writeFakeOwner(t, owner)
	config := filepath.Join(dir, "workspace.json")
	profile := filepath.Join(dir, "profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}

	connectOutput := runForJSON(t, []string{"connect", "--config", config, "--owner-binary", owner, "--id", "lab", "--label", "Lab", "--profile", profile})
	var connection adapters.ConnectionDocument
	decodeOnlyJSON(t, connectOutput, &connection)
	if connection.ID != "lab" || connection.Generation == 0 || connection.SavedEnvironmentID != "env-3" {
		t.Fatalf("connect output = %+v", connection)
	}

	refreshOutput := runForJSON(t, []string{"refresh", "--config", config, "--owner-binary", owner, "--id", "lab"})
	var refreshed adapters.TargetDocument
	decodeOnlyJSON(t, refreshOutput, &refreshed)
	if refreshed.TargetID != "connection/lab" || len(refreshed.Sessions) != 1 || refreshed.Sessions[0].Token == "" {
		t.Fatalf("refresh output = %+v", refreshed)
	}

	inspectOutput := runForJSON(t, []string{"inspect", "--config", config, "--owner-binary", owner, "--local-profile", profile, "--scope", "workspace", "--group", "core"})
	var inventory adapters.InventoryDocument
	decodeOnlyJSON(t, inspectOutput, &inventory)
	if inventory.Scope != application.ScopeWorkspace || len(inventory.Targets) != 2 || len(inventory.Targets[1].Sessions) != 1 {
		t.Fatalf("inspect output = %+v", inventory)
	}
	row := inventory.Targets[1].Sessions[0]

	renameOutput := runForJSON(t, []string{
		"fixture-rename", "--config", config, "--owner-binary", owner, "--local-profile", profile, "--scope", "workspace",
		"--target", row.TargetID, "--token", row.Token, "--name", "Renamed",
	})
	var mutation adapters.MutationDocument
	decodeOnlyJSON(t, renameOutput, &mutation)
	if mutation.TargetID != "connection/lab/fixture" || mutation.Session.Name != "Renamed" || mutation.Token != row.Token {
		t.Fatalf("rename output = %+v", mutation)
	}
}

func TestCLIRejectsInvalidScopeBeforeWritingStdout(t *testing.T) {
	var stdout bytes.Buffer
	err := runCLI(context.Background(), []string{"inspect", "--config", "x", "--owner-binary", "x", "--local-profile", "x", "--scope", "fleet"}, bytes.NewReader(nil), &stdout)
	if application.CodeOf(err) != application.CodeInvalidRequest || stdout.Len() != 0 {
		t.Fatalf("invalid scope = %v, stdout=%q", err, stdout.String())
	}
	var stderr bytes.Buffer
	if writeErr := writeCLIError(&stderr, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	var envelope cliErrorEnvelope
	decodeOnlyJSON(t, stderr.Bytes(), &envelope)
	if envelope.Error.Code != application.CodeInvalidRequest || envelope.Error.Message == "" {
		t.Fatalf("error envelope = %+v", envelope)
	}
}

func TestDemoRunsTwoRealRevision3OwnersWithoutLaunchingAgents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owner POC uses Unix sockets and signals")
	}
	ownerBinary := filepath.Join(t.TempDir(), "architecture-poc")
	command := exec.Command("go", "build", "-o", ownerBinary, "../architecture-poc")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build owner POC: %v\n%s", err, output)
	}
	output := runForJSON(t, []string{"demo", "--owner-binary", ownerBinary})
	var demo DemoDocument
	decodeOnlyJSON(t, output, &demo)
	if demo.Kind != "architecture_workspace_poc_demo" || len(demo.Workspace.Targets) != 2 || len(demo.Device.Targets) != 1 {
		t.Fatalf("demo = %+v", demo)
	}
	if demo.Workspace.Targets[0].Sessions[0].TargetID == demo.Workspace.Targets[1].Sessions[0].TargetID {
		t.Fatalf("demo did not namespace colliding ids: %+v", demo.Workspace.Targets)
	}
	if demo.Renamed.Session.Name != "Remote Renamed" || demo.Renamed.Notice == "" {
		t.Fatalf("demo rename = %+v", demo.Renamed)
	}
	if demo.Effects != "inventory-only fixtures; no agent or tmux process launched" {
		t.Fatalf("demo effects = %q", demo.Effects)
	}
}

func TestCLIStdioMCPAndTUIObserveTheSameInventory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("disposable owner script uses /bin/sh")
	}
	dir := t.TempDir()
	owner := filepath.Join(dir, "owner")
	writeFakeOwner(t, owner)
	profile := filepath.Join(dir, "profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "workspace.json")
	common := []string{"--config", config, "--owner-binary", owner, "--local-profile", profile, "--scope", "workspace"}
	cliPayload := runForJSON(t, append([]string{"inspect"}, common...))
	var cliInventory adapters.InventoryDocument
	decodeOnlyJSON(t, cliPayload, &cliInventory)

	workspaceBinary := filepath.Join(dir, "architecture-workspace-poc")
	build := exec.Command("go", "build", "-o", workspaceBinary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workspace POC: %v\n%s", err, output)
	}
	commandArgs := append([]string{"mcp"}, common...)
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-contract-test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: exec.Command(workspaceBinary, commandArgs...)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	called, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "inventory"})
	if err != nil || called.IsError {
		t.Fatalf("stdio inventory = %+v, %v", called, err)
	}
	mcpPayload, _ := json.Marshal(called.StructuredContent)
	var mcpOutput adapters.InventoryToolOutput
	if err := json.Unmarshal(mcpPayload, &mcpOutput); err != nil {
		t.Fatal(err)
	}
	if mcpOutput.Inventory == nil {
		t.Fatalf("stdio MCP omitted inventory: %s", mcpPayload)
	}

	applicationClient := openWorkspace(config, owner, profile).WorkspaceClient()
	root := adapters.NewRootModel(
		adapters.NewInventoryFeature(extensions.NewFilteredInventory(applicationClient)),
		adapters.NewRenameFeature(extensions.NewFixtureRename(applicationClient)),
	)
	model, load := root.Update(adapters.LoadInventory{})
	model, _ = model.Update(load())
	tuiDocument, err := adapters.PresentInventory(model.(*adapters.RootModel).Inventory.State.Inventory)
	if err != nil {
		t.Fatal(err)
	}
	assertInventoryIdentity(t, cliInventory, *mcpOutput.Inventory)
	assertInventoryIdentity(t, cliInventory, tuiDocument)
}

func assertInventoryIdentity(t *testing.T, left, right adapters.InventoryDocument) {
	t.Helper()
	if left.Scope != right.Scope || len(left.Targets) != len(right.Targets) {
		t.Fatalf("inventory scope/targets differ: left=%+v right=%+v", left, right)
	}
	for index := range left.Targets {
		if left.Targets[index].TargetID != right.Targets[index].TargetID || len(left.Targets[index].Sessions) != len(right.Targets[index].Sessions) {
			t.Fatalf("inventory target differs: left=%+v right=%+v", left.Targets[index], right.Targets[index])
		}
		for sessionIndex := range left.Targets[index].Sessions {
			if left.Targets[index].Sessions[sessionIndex].TargetID != right.Targets[index].Sessions[sessionIndex].TargetID || left.Targets[index].Sessions[sessionIndex].Session.ID != right.Targets[index].Sessions[sessionIndex].Session.ID {
				t.Fatalf("inventory session differs: left=%+v right=%+v", left.Targets[index].Sessions[sessionIndex], right.Targets[index].Sessions[sessionIndex])
			}
		}
	}
}

func runForJSON(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout bytes.Buffer
	if err := runCLI(context.Background(), args, bytes.NewReader(nil), &stdout); err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return stdout.Bytes()
}

func decodeOnlyJSON(t *testing.T, payload []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %q: %v", payload, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("output contains another JSON value: %q (%v)", payload, err)
	}
}

func writeFakeOwner(t *testing.T, path string) {
	t.Helper()
	script := `#!/bin/sh
input=$(cat)
request_id=$(printf '%s' "$input" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
owner='{"instance_id":"owner-3","environment_id":"env-3","revision":3,"capabilities":["owner.describe","sessions.read","fixture_session.create_bound","fixture_session.rename"]}'
case "$input" in
  *'"operation":"sessions.list"'*)
    result='{"kind":"sessions.list","sessions":[{"id":"fixture","name":"Fixture","tool":"codex","cwd":"/tmp/fixture","group":"core","status":"fixture","archived":false,"inventory_only":true}]}' ;;
  *'"operation":"fixture_session.rename"'*)
    result='{"kind":"fixture_session.rename","session":{"id":"fixture","name":"Renamed","tool":"codex","cwd":"/tmp/fixture","group":"core","status":"fixture","archived":false,"inventory_only":true},"notice":"fixture metadata only"}' ;;
  *'"operation":"fixture_session.create_bound"'*)
    result='{"kind":"fixture_session.create_bound","session":{"id":"created","name":"Created","tool":"codex","cwd":"/tmp/created","group":"core","status":"fixture","archived":false,"inventory_only":true},"notice":"fixture metadata only"}' ;;
  *) result='{"kind":"owner.describe"}' ;;
esac
printf '{"protocol_major":1,"request_id":"%s","ok":true,"owner":%s,"result":%s}\n' "$request_id" "$owner" "$result"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}
