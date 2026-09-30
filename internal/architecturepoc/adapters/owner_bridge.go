package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

const (
	ownerProtocolMajor = 1

	operationDescribe           = "owner.describe"
	operationListSessions       = "sessions.list"
	operationCreateBoundFixture = "fixture_session.create_bound"
	operationRenameFixture      = "fixture_session.rename"
)

type RunResult struct {
	Stdout  []byte
	Stderr  []byte
	Started bool
}

type CommandRunner interface {
	Run(context.Context, string, []string, []byte) (RunResult, error)
}

type LocalCommandRunner struct{}

func (LocalCommandRunner) Run(ctx context.Context, name string, args []string, input []byte) (RunResult, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return RunResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
	}
	err := command.Wait()
	return RunResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Started: true}, err
}

type SSHCommandRunner struct {
	Host string
}

func (runner SSHCommandRunner) Run(ctx context.Context, name string, args []string, input []byte) (RunResult, error) {
	commandName, commandArgs, err := sshInvocation(runner.Host, name, args)
	if err != nil {
		return RunResult{}, err
	}
	return LocalCommandRunner{}.Run(ctx, commandName, commandArgs, input)
}

func sshInvocation(host, name string, args []string) (string, []string, error) {
	if host == "" || strings.TrimSpace(host) != host || len(strings.Fields(host)) != 1 || strings.ContainsRune(host, '\x00') {
		return "", nil, application.NewError(application.CodeInvalidRequest, "SSH host must be one non-empty argument", nil)
	}
	if name == "" {
		return "", nil, application.NewError(application.CodeInvalidRequest, "remote owner binary is required", nil)
	}
	remote := make([]string, 0, len(args)+1)
	remote = append(remote, shellQuote(name))
	for _, argument := range args {
		remote = append(remote, shellQuote(argument))
	}
	sshArgs := []string{
		"-T", "-o", "ControlPath=none", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		"-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "--", host, strings.Join(remote, " "),
	}
	return "ssh", sshArgs, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

type SSHRunnerFactory func(string) CommandRunner

type CommandOwnerResolver struct {
	localBinary string
	localRunner CommandRunner
	sshRunner   SSHRunnerFactory
}

func NewProcessOwnerResolver(localBinary string) *CommandOwnerResolver {
	return NewCommandOwnerResolver(localBinary, LocalCommandRunner{}, func(host string) CommandRunner {
		return SSHCommandRunner{Host: host}
	})
}

func NewCommandOwnerResolver(localBinary string, localRunner CommandRunner, sshRunner SSHRunnerFactory) *CommandOwnerResolver {
	return &CommandOwnerResolver{localBinary: localBinary, localRunner: localRunner, sshRunner: sshRunner}
}

func (resolver *CommandOwnerResolver) Open(_ context.Context, endpoint application.Endpoint) (application.OwnerClient, error) {
	if strings.TrimSpace(endpoint.Profile) == "" {
		return nil, application.NewError(application.CodeInvalidRequest, "owner profile is required", nil)
	}
	switch endpoint.Access.Kind {
	case "", application.AccessLocal:
		if strings.TrimSpace(resolver.localBinary) == "" || resolver.localRunner == nil {
			return nil, application.NewError(application.CodeInvalidRequest, "local owner binary and runner are required", nil)
		}
		return NewOwnerBridge(resolver.localRunner, resolver.localBinary, endpoint.Profile), nil
	case application.AccessSSH:
		if resolver.sshRunner == nil || endpoint.Access.Host == "" || endpoint.Access.OwnerBinary == "" {
			return nil, application.NewError(application.CodeInvalidRequest, "SSH host and remote owner binary are required", nil)
		}
		return NewOwnerBridge(resolver.sshRunner(endpoint.Access.Host), endpoint.Access.OwnerBinary, endpoint.Profile), nil
	default:
		return nil, application.NewError(application.CodeInvalidRequest, fmt.Sprintf("unknown access kind %q", endpoint.Access.Kind), nil)
	}
}

type OwnerBridge struct {
	runner  CommandRunner
	binary  string
	profile string
	nextID  atomic.Uint64
}

func NewOwnerBridge(runner CommandRunner, binary, profile string) *OwnerBridge {
	return &OwnerBridge{runner: runner, binary: binary, profile: profile}
}

func (bridge *OwnerBridge) Inspect(ctx context.Context) (application.OwnerInfo, error) {
	response, err := bridge.call(ctx, operationDescribe, struct{}{}, false)
	if err != nil {
		return application.OwnerInfo{}, err
	}
	return response.Owner.domain(), nil
}

func (bridge *OwnerBridge) List(ctx context.Context) (application.OwnerInventory, error) {
	response, err := bridge.call(ctx, operationListSessions, wireListArguments{}, false)
	if err != nil {
		return application.OwnerInventory{}, err
	}
	var result wireListResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return application.OwnerInventory{}, application.NewError(application.CodeOperationFailed, "decode owner session list", err)
	}
	if result.Kind != operationListSessions {
		return application.OwnerInventory{}, application.NewError(application.CodeOperationFailed, fmt.Sprintf("unexpected list result kind %q", result.Kind), nil)
	}
	inventory := application.OwnerInventory{Owner: response.Owner.domain()}
	for _, session := range result.Sessions {
		inventory.Sessions = append(inventory.Sessions, session.domain())
	}
	return inventory, nil
}

func (bridge *OwnerBridge) CreateBound(ctx context.Context, guard application.MutationGuard, draft application.FixtureDraft) (application.OwnerMutation, error) {
	arguments := wireBoundCreateArguments{
		ExpectedEnvironmentID: guard.ExpectedEnvironmentID, ExpectedOwnerInstanceID: guard.ExpectedOwnerInstanceID,
		Session: wireFixture{ID: draft.ID, Name: draft.Name, Tool: draft.Tool, Cwd: draft.Cwd, Group: draft.Group},
	}
	return bridge.mutation(ctx, operationCreateBoundFixture, arguments)
}

func (bridge *OwnerBridge) Rename(ctx context.Context, guard application.MutationGuard, sessionID, name string) (application.OwnerMutation, error) {
	arguments := wireRenameArguments{
		ExpectedEnvironmentID: guard.ExpectedEnvironmentID, ExpectedOwnerInstanceID: guard.ExpectedOwnerInstanceID,
		SessionID: sessionID, Name: name,
	}
	return bridge.mutation(ctx, operationRenameFixture, arguments)
}

func (bridge *OwnerBridge) mutation(ctx context.Context, operation string, arguments any) (application.OwnerMutation, error) {
	response, err := bridge.call(ctx, operation, arguments, true)
	if err != nil {
		return application.OwnerMutation{}, err
	}
	var result wireMutationResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return application.OwnerMutation{}, uncertain("decode successful mutation response", err)
	}
	if result.Kind != operation {
		return application.OwnerMutation{}, uncertain(fmt.Sprintf("unexpected mutation result kind %q", result.Kind), nil)
	}
	return application.OwnerMutation{Owner: response.Owner.domain(), Session: result.Session.domain(), Notice: result.Notice}, nil
}

func (bridge *OwnerBridge) call(ctx context.Context, operation string, arguments any, mutation bool) (wireResponse, error) {
	payload, err := json.Marshal(arguments)
	if err != nil {
		return wireResponse{}, application.NewError(application.CodeInvalidRequest, "encode owner arguments", err)
	}
	request := wireRequest{
		ProtocolMajor: ownerProtocolMajor,
		RequestID:     fmt.Sprintf("workspace-%d", bridge.nextID.Add(1)),
		Operation:     operation,
		Arguments:     payload,
	}
	input, err := json.Marshal(request)
	if err != nil {
		return wireResponse{}, application.NewError(application.CodeInvalidRequest, "encode owner request", err)
	}
	run, err := bridge.runner.Run(ctx, bridge.binary, []string{"rpc", "--dir", bridge.profile}, input)
	if err != nil {
		err = commandFailure(err, run.Stderr)
		if mutation && run.Started {
			return wireResponse{}, uncertain("owner transport failed after mutation dispatch", err)
		}
		return wireResponse{}, application.NewError(application.CodeOffline, "owner transport failed", err)
	}
	var response wireResponse
	if err := json.Unmarshal(run.Stdout, &response); err != nil {
		if mutation && run.Started {
			return wireResponse{}, uncertain("owner mutation response was lost or malformed", err)
		}
		return wireResponse{}, application.NewError(application.CodeOperationFailed, "decode owner response", err)
	}
	if response.RequestID != request.RequestID {
		if mutation {
			return wireResponse{}, uncertain("owner mutation response request id did not match", nil)
		}
		return wireResponse{}, application.NewError(application.CodeOperationFailed, "owner response request id did not match", nil)
	}
	if response.ProtocolMajor != ownerProtocolMajor {
		if mutation {
			return wireResponse{}, uncertain("owner mutation response used an unsupported protocol", nil)
		}
		return wireResponse{}, application.NewError(application.CodeUnsupportedProtocol,
			fmt.Sprintf("owner response protocol %d is unsupported", response.ProtocolMajor), nil)
	}
	if response.Error != nil {
		return wireResponse{}, application.NewError(ownerErrorCode(response.Error.Code), response.Error.Message, nil)
	}
	if !response.OK {
		return wireResponse{}, application.NewError(application.CodeOperationFailed, "owner rejected request without an error code", nil)
	}
	return response, nil
}

func commandFailure(cause error, stderr []byte) error {
	diagnostic := strings.TrimSpace(string(stderr))
	const limit = 512
	if len(diagnostic) > limit {
		diagnostic = diagnostic[:limit] + "..."
	}
	if diagnostic == "" {
		return cause
	}
	return fmt.Errorf("%w: %s", cause, diagnostic)
}

func uncertain(message string, cause error) error {
	return application.NewError(application.CodeUncertainOutcome, message+"; outcome is unknown, inspect before deciding and do not retry automatically", cause)
}

func ownerErrorCode(value string) application.Code {
	code := application.Code(value)
	switch code {
	case application.CodeInvalidRequest, application.CodeMissingCapability, application.CodeEnvironmentMismatch,
		application.CodeOwnerInstanceMismatch, application.CodeFixtureRequired, application.CodeUnsupportedProtocol,
		application.CodeUnknownOperation, application.CodeOperationFailed:
		return code
	default:
		return application.CodeOperationFailed
	}
}

type wireRequest struct {
	ProtocolMajor int             `json:"protocol_major"`
	RequestID     string          `json:"request_id"`
	Operation     string          `json:"operation"`
	Arguments     json.RawMessage `json:"arguments"`
}

type wireResponse struct {
	ProtocolMajor int             `json:"protocol_major"`
	RequestID     string          `json:"request_id"`
	OK            bool            `json:"ok"`
	Owner         wireOwner       `json:"owner"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type wireOwner struct {
	InstanceID    string   `json:"instance_id"`
	EnvironmentID string   `json:"environment_id"`
	Revision      int      `json:"revision"`
	Capabilities  []string `json:"capabilities"`
}

func (owner wireOwner) domain() application.OwnerInfo {
	return application.OwnerInfo{InstanceID: owner.InstanceID, EnvironmentID: owner.EnvironmentID, Revision: owner.Revision, Capabilities: append([]string(nil), owner.Capabilities...)}
}

type wireListArguments struct {
	IncludeArchived bool `json:"include_archived"`
}

type wireListResult struct {
	Kind     string        `json:"kind"`
	Sessions []wireSession `json:"sessions"`
}

type wireFixture struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Tool  string `json:"tool"`
	Cwd   string `json:"cwd"`
	Group string `json:"group"`
}

type wireBoundCreateArguments struct {
	ExpectedEnvironmentID   string      `json:"expected_environment_id"`
	ExpectedOwnerInstanceID string      `json:"expected_owner_instance_id"`
	Session                 wireFixture `json:"session"`
}

type wireRenameArguments struct {
	ExpectedEnvironmentID   string `json:"expected_environment_id"`
	ExpectedOwnerInstanceID string `json:"expected_owner_instance_id"`
	SessionID               string `json:"session_id"`
	Name                    string `json:"name"`
}

type wireMutationResult struct {
	Kind    string      `json:"kind"`
	Session wireSession `json:"session"`
	Notice  string      `json:"notice"`
}

type wireSession struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Tool          string `json:"tool"`
	Cwd           string `json:"cwd"`
	Group         string `json:"group"`
	Status        string `json:"status"`
	Archived      bool   `json:"archived"`
	InventoryOnly bool   `json:"inventory_only"`
}

func (session wireSession) domain() application.Session {
	return application.Session{
		ID: session.ID, Name: session.Name, Tool: session.Tool, Cwd: session.Cwd, Group: session.Group,
		Status: session.Status, Archived: session.Archived, InventoryOnly: session.InventoryOnly,
	}
}
