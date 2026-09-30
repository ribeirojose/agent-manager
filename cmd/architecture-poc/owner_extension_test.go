package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/YoanWai/agent-manager/internal/store"
)

func boundFixtureArguments(owner OwnerInfo, id string) BoundCreateFixtureArguments {
	return BoundCreateFixtureArguments{
		CommandGuards: CommandGuards{
			ExpectedEnvironmentID:   owner.EnvironmentID,
			ExpectedOwnerInstanceID: owner.InstanceID,
		},
		Session: fixtureArguments(owner.EnvironmentID, id).Session,
	}
}

func renameFixtureArguments(owner OwnerInfo, id, name string) RenameFixtureArguments {
	return RenameFixtureArguments{
		CommandGuards: CommandGuards{
			ExpectedEnvironmentID:   owner.EnvironmentID,
			ExpectedOwnerInstanceID: owner.InstanceID,
		},
		SessionID: id,
		Name:      name,
	}
}

func requireRPCError(t *testing.T, response Response, code string) {
	t.Helper()
	if response.OK || response.Error == nil || response.Error.Code != code {
		t.Fatalf("response = %+v, want error code %q", response, code)
	}
}

func TestRevision2RejectsRevision3FixtureCommands(t *testing.T) {
	dir := t.TempDir()
	owner := startTestOwner(t, dir, 2)
	described := describeOwner(t, dir)
	for _, capability := range []string{capabilityFixtureCreateBound, capabilityFixtureRename} {
		if slices.Contains(described.Owner.Capabilities, capability) {
			t.Fatalf("revision 2 advertised %q: %v", capability, described.Owner.Capabilities)
		}
	}

	requests := []Request{
		testRequest("bound-create-on-v2", operationCreateBoundFixture, boundFixtureArguments(described.Owner, "must-not-exist")),
		testRequest("rename-on-v2", operationRenameFixture, renameFixtureArguments(described.Owner, "must-not-exist", "renamed")),
	}
	for _, request := range requests {
		requireRPCError(t, mustRPC(t, dir, request), errorMissingCapability)
	}
	if got := len(listSessions(t, dir).Sessions); got != 0 {
		t.Fatalf("revision 2 denials wrote %d sessions", got)
	}
	owner.stop(t)
}

func TestRevision3CreatesThenRenamesAnInventoryFixture(t *testing.T) {
	dir := t.TempDir()
	owner := startTestOwner(t, dir, 3)
	described := describeOwner(t, dir)
	for _, capability := range []string{capabilityFixtureCreateBound, capabilityFixtureRename} {
		if !slices.Contains(described.Owner.Capabilities, capability) {
			t.Fatalf("revision 3 omitted %q: %v", capability, described.Owner.Capabilities)
		}
	}
	if slices.Contains(described.Owner.Capabilities, capabilityFixtureCreate) {
		t.Fatalf("revision 3 retained unsafe legacy capability: %v", described.Owner.Capabilities)
	}

	created := mustRPC(t, dir, testRequest("bound-create", operationCreateBoundFixture,
		boundFixtureArguments(described.Owner, "bound-fixture")))
	if !created.OK {
		t.Fatalf("bound create rejected: %+v", created.Error)
	}
	var createResult CreateFixtureResult
	if err := json.Unmarshal(created.Result, &createResult); err != nil {
		t.Fatalf("decode bound create: %v", err)
	}
	if createResult.Kind != operationCreateBoundFixture || !createResult.Session.InventoryOnly {
		t.Fatalf("bound create result = %+v", createResult)
	}

	renamed := mustRPC(t, dir, testRequest("rename", operationRenameFixture,
		renameFixtureArguments(described.Owner, "bound-fixture", "renamed-fixture")))
	if !renamed.OK {
		t.Fatalf("fixture rename rejected: %+v", renamed.Error)
	}
	var renameResult RenameFixtureResult
	if err := json.Unmarshal(renamed.Result, &renameResult); err != nil {
		t.Fatalf("decode rename: %v", err)
	}
	if renameResult.Kind != operationRenameFixture || renameResult.Session.Name != "renamed-fixture" || !renameResult.Session.InventoryOnly {
		t.Fatalf("rename result = %+v", renameResult)
	}
	sessions := listSessions(t, dir).Sessions
	if len(sessions) != 1 || sessions[0].ID != "bound-fixture" || sessions[0].Name != "renamed-fixture" {
		t.Fatalf("renamed inventory = %+v", sessions)
	}
	owner.stop(t)
}

func TestRevision3RejectsLegacyCreateWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	owner := startTestOwner(t, dir, 3)
	described := describeOwner(t, dir)
	response := mustRPC(t, dir, testRequest("legacy-create-on-v3", operationCreateFixture,
		fixtureArguments(described.Owner.EnvironmentID, "legacy-must-not-exist")))
	requireRPCError(t, response, errorMissingCapability)
	if got := len(listSessions(t, dir).Sessions); got != 0 {
		t.Fatalf("revision 3 legacy denial wrote %d sessions", got)
	}
	owner.stop(t)
}

func TestRevision3WrongGuardsDoNotMutate(t *testing.T) {
	dir := t.TempDir()
	owner := startTestOwner(t, dir, 3)
	described := describeOwner(t, dir)

	wrongEnvironment := boundFixtureArguments(described.Owner, "wrong-environment")
	wrongEnvironment.ExpectedEnvironmentID = "env-wrong"
	requireRPCError(t, mustRPC(t, dir, testRequest("wrong-create-environment", operationCreateBoundFixture, wrongEnvironment)), errorEnvironmentMismatch)
	wrongOwner := boundFixtureArguments(described.Owner, "wrong-owner")
	wrongOwner.ExpectedOwnerInstanceID = "owner-wrong"
	requireRPCError(t, mustRPC(t, dir, testRequest("wrong-create-owner", operationCreateBoundFixture, wrongOwner)), errorOwnerInstanceMismatch)
	if got := len(listSessions(t, dir).Sessions); got != 0 {
		t.Fatalf("rejected bound creates wrote %d sessions", got)
	}

	accepted := mustRPC(t, dir, testRequest("accepted-create", operationCreateBoundFixture,
		boundFixtureArguments(described.Owner, "guarded-fixture")))
	if !accepted.OK {
		t.Fatalf("guarded create rejected: %+v", accepted.Error)
	}
	wrongRenameEnvironment := renameFixtureArguments(described.Owner, "guarded-fixture", "wrong-environment-name")
	wrongRenameEnvironment.ExpectedEnvironmentID = "env-wrong"
	requireRPCError(t, mustRPC(t, dir, testRequest("wrong-rename-environment", operationRenameFixture, wrongRenameEnvironment)), errorEnvironmentMismatch)
	wrongRenameOwner := renameFixtureArguments(described.Owner, "guarded-fixture", "wrong-owner-name")
	wrongRenameOwner.ExpectedOwnerInstanceID = "owner-wrong"
	requireRPCError(t, mustRPC(t, dir, testRequest("wrong-rename-owner", operationRenameFixture, wrongRenameOwner)), errorOwnerInstanceMismatch)
	sessions := listSessions(t, dir).Sessions
	if len(sessions) != 1 || sessions[0].Name != "fixture-session" {
		t.Fatalf("rejected renames changed inventory: %+v", sessions)
	}
	owner.stop(t)
}

func TestRevision3RejectsRenameOfANonFixtureSession(t *testing.T) {
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	if err := state.CreateSession(store.Session{
		ID: "real-session", Name: "real-name", Tool: "codex", Cwd: dir,
		Group: "architecture-poc", Status: "idle", TmuxSocket: "nonfixture-socket",
	}); err != nil {
		state.Close()
		t.Fatalf("create nonfixture row: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}

	owner := startTestOwner(t, dir, 3)
	described := describeOwner(t, dir)
	response := mustRPC(t, dir, testRequest("rename-nonfixture", operationRenameFixture,
		renameFixtureArguments(described.Owner, "real-session", "must-not-change")))
	requireRPCError(t, response, errorFixtureRequired)
	sessions := listSessions(t, dir).Sessions
	if len(sessions) != 1 || sessions[0].Name != "real-name" || sessions[0].InventoryOnly {
		t.Fatalf("nonfixture row changed: %+v", sessions)
	}
	owner.stop(t)
}

func TestRestartAndClonedProfileRejectAStaleOwnerGuard(t *testing.T) {
	sourceDir := t.TempDir()
	first := startTestOwner(t, sourceDir, 3)
	firstInfo := describeOwner(t, sourceDir).Owner
	created := mustRPC(t, sourceDir, testRequest("source-create", operationCreateBoundFixture,
		boundFixtureArguments(firstInfo, "persistent-fixture")))
	if !created.OK {
		t.Fatalf("source create rejected: %+v", created.Error)
	}
	first.stop(t)

	second := startTestOwner(t, sourceDir, 3)
	secondInfo := describeOwner(t, sourceDir).Owner
	if secondInfo.EnvironmentID != firstInfo.EnvironmentID || secondInfo.InstanceID == firstInfo.InstanceID {
		t.Fatalf("restart identities: first=%+v second=%+v", firstInfo, secondInfo)
	}
	staleRename := renameFixtureArguments(firstInfo, "persistent-fixture", "stale-restart-name")
	requireRPCError(t, mustRPC(t, sourceDir, testRequest("stale-after-restart", operationRenameFixture, staleRename)), errorOwnerInstanceMismatch)
	if sessions := listSessions(t, sourceDir).Sessions; len(sessions) != 1 || sessions[0].Name != "fixture-session" {
		t.Fatalf("stale restart guard changed inventory: %+v", sessions)
	}
	second.stop(t)

	cloneDir := t.TempDir()
	database, err := os.ReadFile(filepath.Join(sourceDir, "state.db"))
	if err != nil {
		t.Fatalf("read source database: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "state.db"), database, 0o600); err != nil {
		t.Fatalf("copy profile database: %v", err)
	}
	clone := startTestOwner(t, cloneDir, 3)
	cloneInfo := describeOwner(t, cloneDir).Owner
	if cloneInfo.EnvironmentID != firstInfo.EnvironmentID || cloneInfo.InstanceID == firstInfo.InstanceID {
		t.Fatalf("clone identities: source=%+v clone=%+v", firstInfo, cloneInfo)
	}
	requireRPCError(t, mustRPC(t, cloneDir, testRequest("stale-against-clone", operationRenameFixture, staleRename)), errorOwnerInstanceMismatch)
	if sessions := listSessions(t, cloneDir).Sessions; len(sessions) != 1 || sessions[0].Name != "fixture-session" {
		t.Fatalf("stale clone guard changed inventory: %+v", sessions)
	}
	clone.stop(t)
}
