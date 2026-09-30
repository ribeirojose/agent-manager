package adapters

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
)

const (
	operationArchiveSession  = "session.archive"
	operationMaintainInbox   = "inbox.maintain"
	capabilityArchiveSession = "session.archive.bound"
	capabilityMaintainInbox  = "inbox.maintain.bound"
)

type MigrationOwner struct {
	bridge   *OwnerBridge
	observed application.OwnerInfo
}

func NewMigrationOwner(ctx context.Context, runner CommandRunner, binary, profile string) (*MigrationOwner, error) {
	bridge := NewOwnerBridge(runner, binary, profile)
	info, err := bridge.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	return &MigrationOwner{bridge: bridge, observed: info}, nil
}

type migrationGuard struct {
	EnvironmentID string `json:"expected_environment_id"`
	InstanceID    string `json:"expected_owner_instance_id"`
}

type migrationArchiveArguments struct {
	migrationGuard
	CallerID  string `json:"caller_id"`
	SessionID string `json:"session_id"`
	Archived  bool   `json:"archived"`
	Frontend  string `json:"frontend"`
}

func (owner *MigrationOwner) Archive(request sessioncmd.ArchiveRequest) (sessioncmd.Session, error) {
	var front string
	switch request.Words.ListSessions {
	case sessioncmd.CLIVocabulary().ListSessions:
		front = "cli"
	case sessioncmd.MCPVocabulary().ListSessions:
		front = "mcp"
	default:
		return sessioncmd.Session{}, application.NewError(application.CodeInvalidRequest, "unknown archive frontend", nil)
	}
	response, err := owner.call(operationArchiveSession, capabilityArchiveSession, migrationArchiveArguments{
		migrationGuard: owner.guard(), CallerID: request.CallerID, SessionID: request.TargetID, Archived: request.Archived, Frontend: front,
	})
	if err != nil {
		return sessioncmd.Session{}, err
	}
	var result struct {
		Kind    string             `json:"kind"`
		Session sessioncmd.Session `json:"session"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return sessioncmd.Session{}, uncertain("decode canonical archive result", err)
	}
	if result.Kind != operationArchiveSession || result.Session.ID != request.TargetID || result.Session.Archived != request.Archived {
		return sessioncmd.Session{}, uncertain("canonical archive result did not match the dispatched operation", nil)
	}
	return result.Session, nil
}

func (owner *MigrationOwner) MaintainInbox() error {
	response, err := owner.call(operationMaintainInbox, capabilityMaintainInbox, owner.guard())
	if err != nil {
		return err
	}
	var result struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return uncertain("decode inbox maintenance result", err)
	}
	if result.Kind != operationMaintainInbox {
		return uncertain("unexpected inbox maintenance result", nil)
	}
	return nil
}

func (owner *MigrationOwner) guard() migrationGuard {
	return migrationGuard{EnvironmentID: owner.observed.EnvironmentID, InstanceID: owner.observed.InstanceID}
}

func (owner *MigrationOwner) call(operation, capability string, arguments any) (wireResponse, error) {
	if !slices.Contains(owner.observed.Capabilities, capability) {
		return wireResponse{}, application.NewError(application.CodeMissingCapability, "observed owner does not support "+capability, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := owner.bridge.call(ctx, operation, arguments, true)
	if err != nil {
		return wireResponse{}, err
	}
	if response.Owner.EnvironmentID != owner.observed.EnvironmentID || response.Owner.InstanceID != owner.observed.InstanceID {
		return wireResponse{}, uncertain("successful owner response came from a different binding", nil)
	}
	return response, nil
}
