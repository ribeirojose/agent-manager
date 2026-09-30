package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

type runnerFunc func(context.Context, string, []string, []byte) (RunResult, error)

func (run runnerFunc) Run(ctx context.Context, name string, args []string, input []byte) (RunResult, error) {
	return run(ctx, name, args, input)
}

func TestOwnerBridgeUsesTypedBoundOperationsAndParsesOwnerMetadata(t *testing.T) {
	var operations []wireRequest
	runner := runnerFunc(func(_ context.Context, name string, args []string, input []byte) (RunResult, error) {
		if name != "/tmp/architecture-poc" || !reflect.DeepEqual(args, []string{"rpc", "--dir", "/tmp/profile"}) {
			t.Fatalf("command = %q %v", name, args)
		}
		var request wireRequest
		if err := json.Unmarshal(input, &request); err != nil {
			t.Fatal(err)
		}
		operations = append(operations, request)
		owner := wireOwner{InstanceID: "owner-3", EnvironmentID: "env-3", Revision: 3, Capabilities: []string{application.CapabilitySessionsRead, application.CapabilityFixtureCreateBound, application.CapabilityFixtureRename}}
		var result any
		switch request.Operation {
		case operationDescribe:
			result = map[string]any{"kind": operationDescribe}
		case operationListSessions:
			result = wireListResult{Kind: operationListSessions, Sessions: []wireSession{{ID: "one", Name: "One", Tool: "codex", Cwd: "/tmp/one", Group: "core", Status: "fixture", InventoryOnly: true}}}
		case operationCreateBoundFixture:
			result = wireMutationResult{Kind: operationCreateBoundFixture, Session: wireSession{ID: "created", Name: "Created", Tool: "codex", Cwd: "/tmp/created", Group: "core", Status: "fixture", InventoryOnly: true}, Notice: "created fixture only"}
		case operationRenameFixture:
			result = wireMutationResult{Kind: operationRenameFixture, Session: wireSession{ID: "one", Name: "Renamed", Tool: "codex", Cwd: "/tmp/one", Group: "core", Status: "fixture", InventoryOnly: true}, Notice: "renamed fixture only"}
		default:
			t.Fatalf("unexpected operation %q", request.Operation)
		}
		payload, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		response, err := json.Marshal(wireResponse{ProtocolMajor: 1, RequestID: request.RequestID, OK: true, Owner: owner, Result: payload})
		return RunResult{Stdout: response, Started: true}, err
	})
	bridge := NewOwnerBridge(runner, "/tmp/architecture-poc", "/tmp/profile")
	ctx := context.Background()
	if info, err := bridge.Inspect(ctx); err != nil || info.InstanceID != "owner-3" || info.Revision != 3 {
		t.Fatalf("inspect = %+v, %v", info, err)
	}
	listed, err := bridge.List(ctx)
	if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].ID != "one" {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	guard := application.MutationGuard{ExpectedEnvironmentID: "env-3", ExpectedOwnerInstanceID: "owner-3"}
	created, err := bridge.CreateBound(ctx, guard, application.FixtureDraft{ID: "created", Name: "Created", Tool: "codex", Cwd: "/tmp/created", Group: "core"})
	if err != nil || created.Session.ID != "created" || created.Notice == "" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	renamed, err := bridge.Rename(ctx, guard, "one", "Renamed")
	if err != nil || renamed.Session.Name != "Renamed" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	if got := []string{operations[0].Operation, operations[1].Operation, operations[2].Operation, operations[3].Operation}; !reflect.DeepEqual(got, []string{operationDescribe, operationListSessions, operationCreateBoundFixture, operationRenameFixture}) {
		t.Fatalf("operations = %v", got)
	}
	var createArgs wireBoundCreateArguments
	if err := json.Unmarshal(operations[2].Arguments, &createArgs); err != nil {
		t.Fatal(err)
	}
	if createArgs.ExpectedEnvironmentID != "env-3" || createArgs.ExpectedOwnerInstanceID != "owner-3" {
		t.Fatalf("create guards = %+v", createArgs)
	}
	var renameArgs wireRenameArguments
	if err := json.Unmarshal(operations[3].Arguments, &renameArgs); err != nil {
		t.Fatal(err)
	}
	if renameArgs.ExpectedEnvironmentID != "env-3" || renameArgs.ExpectedOwnerInstanceID != "owner-3" || renameArgs.SessionID != "one" {
		t.Fatalf("rename args = %+v", renameArgs)
	}
}

func TestOwnerBridgeClassifiesLostMutationResponseAsUncertain(t *testing.T) {
	runner := runnerFunc(func(context.Context, string, []string, []byte) (RunResult, error) {
		return RunResult{Started: true}, errors.New("connection closed")
	})
	bridge := NewOwnerBridge(runner, "owner", "/profile")
	guard := application.MutationGuard{ExpectedEnvironmentID: "env", ExpectedOwnerInstanceID: "instance"}
	if _, err := bridge.Rename(context.Background(), guard, "fixture", "name"); application.CodeOf(err) != application.CodeUncertainOutcome || !strings.Contains(err.Error(), "do not retry") {
		t.Fatalf("mutation transport error = %v", err)
	}
	if _, err := bridge.List(context.Background()); application.CodeOf(err) != application.CodeOffline {
		t.Fatalf("read transport error = %v", err)
	}
}

func TestOwnerBridgePreservesStableOwnerRejection(t *testing.T) {
	runner := runnerFunc(func(_ context.Context, _ string, _ []string, input []byte) (RunResult, error) {
		var request wireRequest
		if err := json.Unmarshal(input, &request); err != nil {
			t.Fatal(err)
		}
		response, err := json.Marshal(wireResponse{
			ProtocolMajor: 1, RequestID: request.RequestID,
			Owner: wireOwner{InstanceID: "owner-old", EnvironmentID: "env-old", Revision: 2},
			Error: &wireError{Code: string(application.CodeMissingCapability), Message: "revision 2 lacks fixture rename"},
		})
		return RunResult{Stdout: response, Started: true}, err
	})
	bridge := NewOwnerBridge(runner, "owner", "/profile")
	guard := application.MutationGuard{ExpectedEnvironmentID: "env-old", ExpectedOwnerInstanceID: "owner-old"}
	if _, err := bridge.Rename(context.Background(), guard, "fixture", "name"); application.CodeOf(err) != application.CodeMissingCapability {
		t.Fatalf("owner rejection = %v", err)
	}
}

func TestSSHInvocationDisablesForwardingAndQuotesEveryRemoteArgument(t *testing.T) {
	name, args, err := sshInvocation("demo@example", "/opt/owner bin", []string{"rpc", "--dir", "/srv/a'b"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "ssh" {
		t.Fatalf("binary = %q", name)
	}
	wantPrefix := []string{"-T", "-o", "ControlPath=none", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "--", "demo@example"}
	if len(args) != len(wantPrefix)+1 || !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("ssh args = %#v", args)
	}
	if got, want := args[len(args)-1], `'/opt/owner bin' 'rpc' '--dir' '/srv/a'"'"'b'`; got != want {
		t.Fatalf("remote command = %q, want %q", got, want)
	}
}

func TestCommandOwnerResolverSelectsRouteOnlyInAdapter(t *testing.T) {
	local := runnerFunc(func(context.Context, string, []string, []byte) (RunResult, error) { return RunResult{}, nil })
	var sshHost string
	resolver := NewCommandOwnerResolver("/local/owner", local, func(host string) CommandRunner {
		sshHost = host
		return local
	})
	if _, err := resolver.Open(context.Background(), application.Endpoint{Profile: "/local", Access: application.Access{Kind: application.AccessLocal}}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Open(context.Background(), application.Endpoint{Profile: "/remote", Access: application.Access{Kind: application.AccessSSH, Host: "demo@example", OwnerBinary: "/remote/owner"}}); err != nil {
		t.Fatal(err)
	}
	if sshHost != "demo@example" {
		t.Fatalf("ssh factory host = %q", sshHost)
	}
}

func TestLocalRunnerKeepsWarningStderrOutOfValidOwnerJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("disposable owner script uses /bin/sh")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "owner")
	script := `#!/bin/sh
input=$(cat)
request_id=$(printf '%s' "$input" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
printf '%s\n' 'known host warning' >&2
printf '{"protocol_major":1,"request_id":"%s","ok":true,"owner":{"instance_id":"owner","environment_id":"env","revision":3,"capabilities":["sessions.read"]},"result":{"kind":"sessions.list","sessions":[]}}\n' "$request_id"
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	bridge := NewOwnerBridge(LocalCommandRunner{}, binary, dir)
	listed, err := bridge.List(context.Background())
	if err != nil || listed.Owner.InstanceID != "owner" || len(listed.Sessions) != 0 {
		t.Fatalf("valid stdout with warning stderr = %+v, %v", listed, err)
	}
}

func TestLocalRunnerSurfacesBoundedNonzeroStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("disposable owner script uses /bin/sh")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "owner")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' 'remote diagnostic' >&2\nexit 7\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	bridge := NewOwnerBridge(LocalCommandRunner{}, binary, dir)
	if _, err := bridge.List(context.Background()); application.CodeOf(err) != application.CodeOffline || !strings.Contains(err.Error(), "remote diagnostic") {
		t.Fatalf("nonzero command error = %v", err)
	}
}
