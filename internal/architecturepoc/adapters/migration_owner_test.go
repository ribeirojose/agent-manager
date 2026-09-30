package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
)

func TestMigrationOwnerRejectsOldCapabilitiesBeforeDispatch(t *testing.T) {
	owner := &MigrationOwner{
		bridge: NewOwnerBridge(runnerFunc(func(context.Context, string, []string, []byte) (RunResult, error) {
			t.Fatal("old owner must not receive mutation dispatch")
			return RunResult{}, nil
		}), "owner", "profile"),
		observed: application.OwnerInfo{EnvironmentID: "env", InstanceID: "instance", Revision: 3},
	}
	_, err := owner.Archive(sessioncmd.ArchiveRequest{CallerID: "cafe", TargetID: "beef", Archived: true, Words: sessioncmd.CLIVocabulary()})
	if application.CodeOf(err) != application.CodeMissingCapability {
		t.Fatalf("archive error = %v", err)
	}
	if err := owner.MaintainInbox(); application.CodeOf(err) != application.CodeMissingCapability {
		t.Fatalf("maintenance error = %v", err)
	}
}

func TestMigrationOwnerDoesNotRetryLostMutationResponse(t *testing.T) {
	calls := 0
	owner := &MigrationOwner{
		bridge: NewOwnerBridge(runnerFunc(func(context.Context, string, []string, []byte) (RunResult, error) {
			calls++
			return RunResult{Started: true}, errors.New("response pipe lost")
		}), "owner", "profile"),
		observed: application.OwnerInfo{EnvironmentID: "env", InstanceID: "instance", Revision: 4, Capabilities: []string{capabilityArchiveSession}},
	}
	sessions := sessioncmd.NewSessionsWithArchiveOwner(t.TempDir(), sessioncmd.CLIVocabulary(), owner)
	_, err := sessions.Archive("cafe", "beef", true)
	if application.CodeOf(err) != application.CodeUncertainOutcome || calls != 1 {
		t.Fatalf("lost response = %v; calls = %d", err, calls)
	}
}
