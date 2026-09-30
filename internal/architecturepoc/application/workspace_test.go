package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryRepository struct {
	mu    sync.Mutex
	state WorkspaceState
}

func (repository *memoryRepository) View(_ context.Context, work func(WorkspaceState) error) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return work(cloneState(repository.state))
}

func (repository *memoryRepository) Update(_ context.Context, work func(*WorkspaceState) error) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state := cloneState(repository.state)
	if err := work(&state); err != nil {
		return err
	}
	repository.state = cloneState(state)
	return nil
}

type fakeResolver struct {
	mu      sync.Mutex
	clients map[string]*fakeOwner
}

func (resolver *fakeResolver) Open(_ context.Context, endpoint Endpoint) (OwnerClient, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	client := resolver.clients[endpoint.Profile]
	if client == nil {
		return nil, NewError(CodeOffline, "endpoint is offline", nil)
	}
	return client, nil
}

func (resolver *fakeResolver) set(profile string, owner *fakeOwner) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.clients[profile] = owner
}

type fakeOwner struct {
	mu            sync.Mutex
	info          OwnerInfo
	sessions      []Session
	listStarted   chan struct{}
	listRelease   chan struct{}
	listErr       error
	inspectCalls  int
	listCalls     int
	createCalls   int
	renameCalls   int
	lastGuard     MutationGuard
	renameApplied string
	renameStarted chan struct{}
	renameRelease chan struct{}
}

func (owner *fakeOwner) Inspect(context.Context) (OwnerInfo, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.inspectCalls++
	return cloneOwner(owner.info), nil
}

func (owner *fakeOwner) List(ctx context.Context) (OwnerInventory, error) {
	owner.mu.Lock()
	owner.listCalls++
	started, release, failure := owner.listStarted, owner.listRelease, owner.listErr
	info, sessions := cloneOwner(owner.info), append([]Session(nil), owner.sessions...)
	owner.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return OwnerInventory{}, ctx.Err()
		case <-release:
		}
	}
	if failure != nil {
		return OwnerInventory{}, failure
	}
	return OwnerInventory{Owner: info, Sessions: sessions}, nil
}

func (owner *fakeOwner) CreateBound(_ context.Context, guard MutationGuard, draft FixtureDraft) (OwnerMutation, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.createCalls++
	owner.lastGuard = guard
	if err := owner.checkGuard(guard); err != nil {
		return OwnerMutation{}, err
	}
	session := Session{ID: draft.ID, Name: draft.Name, Tool: draft.Tool, Cwd: draft.Cwd, Group: draft.Group, Status: "fixture", InventoryOnly: true}
	owner.sessions = append(owner.sessions, session)
	return OwnerMutation{Owner: cloneOwner(owner.info), Session: session}, nil
}

func (owner *fakeOwner) Rename(_ context.Context, guard MutationGuard, sessionID, name string) (OwnerMutation, error) {
	owner.mu.Lock()
	owner.renameCalls++
	owner.lastGuard = guard
	started, release := owner.renameStarted, owner.renameRelease
	owner.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		<-release
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if err := owner.checkGuard(guard); err != nil {
		return OwnerMutation{}, err
	}
	for index := range owner.sessions {
		if owner.sessions[index].ID == sessionID {
			owner.sessions[index].Name = name
			owner.renameApplied = name
			return OwnerMutation{Owner: cloneOwner(owner.info), Session: owner.sessions[index]}, nil
		}
	}
	return OwnerMutation{}, NewError(CodeNotFound, "fixture not found", nil)
}

func (owner *fakeOwner) checkGuard(guard MutationGuard) error {
	if guard.ExpectedEnvironmentID != owner.info.EnvironmentID {
		return NewError(CodeEnvironmentMismatch, "wrong environment", nil)
	}
	if guard.ExpectedOwnerInstanceID != owner.info.InstanceID {
		return NewError(CodeOwnerInstanceMismatch, "wrong owner instance", nil)
	}
	return nil
}

func ownerInfo(environment, instance string, revision int, capabilities ...string) OwnerInfo {
	return OwnerInfo{EnvironmentID: environment, InstanceID: instance, Revision: revision, Capabilities: capabilities}
}

func fixture(id, group string) Session {
	return Session{ID: id, Name: id, Tool: "codex", Cwd: "/tmp/" + id, Group: group, Status: "fixture", InventoryOnly: true}
}

func newTestWorkspace(local *fakeOwner, remotes map[string]*fakeOwner) (*Workspace, *memoryRepository, *fakeResolver) {
	repository := &memoryRepository{state: WorkspaceState{DeviceID: "local", NextGeneration: 1}}
	resolver := &fakeResolver{clients: map[string]*fakeOwner{"local": local}}
	for profile, owner := range remotes {
		resolver.clients[profile] = owner
	}
	clock := func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
	return NewWorkspace(repository, resolver, Endpoint{Profile: "local"}, clock), repository, resolver
}

func TestInventoryNamespacesCollidingLocalAndRemoteSessions(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead), sessions: []Session{fixture("same", "core")}}
	remote := &fakeOwner{info: ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead), sessions: []Session{fixture("same", "core")}}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()

	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	inventory, err := workspace.WorkspaceClient().Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Targets) != 2 || len(inventory.Targets[0].Sessions)+len(inventory.Targets[1].Sessions) != 2 {
		t.Fatalf("inventory = %+v", inventory)
	}
	localTarget := inventory.Targets[0].Sessions[0].Target
	remoteTarget := inventory.Targets[1].Sessions[0].Target
	if localTarget == remoteTarget || localTarget.Owner.Kind != TargetDevice || remoteTarget.Owner.ConnectionID != "lab" {
		t.Fatalf("colliding ids were not namespaced: local=%+v remote=%+v", localTarget, remoteTarget)
	}
}

func TestConnectionLabelRenamePreservesIdentityAndReaderToken(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	remote := &fakeOwner{info: ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead), sessions: []Session{fixture("one", "core")}}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	connected, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Before", Endpoint: Endpoint{Profile: "remote"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	before := remoteTarget(t, workspace, "lab")
	if _, err := workspace.RenameConnection(ctx, "lab", "After"); err != nil {
		t.Fatal(err)
	}
	after := remoteTarget(t, workspace, "lab")
	if after.Label != "After" || after.Token != before.Token || after.Token.Generation != connected.Generation {
		t.Fatalf("rename changed identity: before=%+v after=%+v", before, after)
	}
}

func TestOfflineRefreshRetainsStaleProjectionAndReportsFailure(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	remote := &fakeOwner{info: ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead), sessions: []Session{fixture("cached", "core")}}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.listErr = NewError(CodeOffline, "link down", nil)
	remote.mu.Unlock()
	if _, err := workspace.RefreshConnection(ctx, "lab"); CodeOf(err) != CodeOffline {
		t.Fatalf("refresh error = %v", err)
	}
	target := remoteTarget(t, workspace, "lab")
	if target.Freshness.State != FreshnessStale || target.Freshness.Failure == nil || len(target.Sessions) != 1 || target.Sessions[0].Session.ID != "cached" {
		t.Fatalf("offline projection = %+v", target)
	}
	if remote.createCalls != 0 || remote.renameCalls != 0 {
		t.Fatalf("read failure caused writes: create=%d rename=%d", remote.createCalls, remote.renameCalls)
	}
}

func TestRetargetRejectsLateRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	remote := &fakeOwner{
		info: ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead), sessions: []Session{fixture("late", "core")},
		listStarted: started, listRelease: release,
	}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := workspace.RefreshConnection(ctx, "lab"); done <- err }()
	<-started
	if _, err := workspace.RetargetConnection(ctx, "lab", Endpoint{Profile: "other"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; CodeOf(err) != CodeStaleTarget {
		t.Fatalf("late refresh error = %v", err)
	}
	state, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	connection := state.Connections[0]
	if connection.Endpoint.Profile != "other" || connection.SavedEnvironmentID != "" || len(connection.Projection.Sessions) != 0 {
		t.Fatalf("late data survived retarget: %+v", connection)
	}
}

func TestOwnerReplacementRejectsMutationWithoutRediscovery(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	oldOwner := &fakeOwner{
		info:     ownerInfo("env-shared", "owner-old", 3, CapabilitySessionsRead, CapabilityFixtureRename),
		sessions: []Session{fixture("one", "core")},
	}
	workspace, _, resolver := newTestWorkspace(local, map[string]*fakeOwner{"remote": oldOwner})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	row := remoteTarget(t, workspace, "lab").Sessions[0]
	replacement := &fakeOwner{
		info:     ownerInfo("env-shared", "owner-new", 3, CapabilitySessionsRead, CapabilityFixtureRename),
		sessions: []Session{fixture("one", "core")},
	}
	resolver.set("remote", replacement)
	if _, err := workspace.WorkspaceClient().RenameFixture(ctx, row.Target, row.Token, "must-not-apply"); CodeOf(err) != CodeOwnerInstanceMismatch {
		t.Fatalf("replacement error = %v", err)
	}
	if replacement.inspectCalls != 0 || replacement.renameCalls != 1 || replacement.renameApplied != "" {
		t.Fatalf("mutation rebound or applied: inspect=%d rename=%d applied=%q", replacement.inspectCalls, replacement.renameCalls, replacement.renameApplied)
	}
}

func TestOldOwnerIsReadableButBoundWritesStayUnavailable(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	oldOwner := &fakeOwner{info: ownerInfo("env-old", "owner-old", 2, CapabilitySessionsRead), sessions: []Session{fixture("legacy", "core")}}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"old": oldOwner})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "old", Label: "Old", Endpoint: Endpoint{Profile: "old"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	row := remoteTarget(t, workspace, "old").Sessions[0]
	if _, err := workspace.WorkspaceClient().RenameFixture(ctx, row.Target, row.Token, "new-name"); CodeOf(err) != CodeMissingCapability {
		t.Fatalf("old owner write error = %v", err)
	}
	if oldOwner.renameCalls != 0 {
		t.Fatalf("unsupported write reached old owner %d times", oldOwner.renameCalls)
	}
}

func TestDeviceInventoryNeverTraversesSavedOutboundConnections(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead), sessions: []Session{fixture("local", "core")}}
	remote := &fakeOwner{info: ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead), sessions: []Session{fixture("remote", "core")}}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	before := remote.listCalls
	remote.mu.Unlock()
	inventory, err := workspace.DeviceClient().Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Targets) != 1 || inventory.Targets[0].Target.Kind != TargetDevice {
		t.Fatalf("device inventory = %+v", inventory)
	}
	remote.mu.Lock()
	after := remote.listCalls
	remote.mu.Unlock()
	if after != before {
		t.Fatalf("device inventory traversed remote: before=%d after=%d", before, after)
	}
}

func TestCreateBoundUsesSelectedProjectionToken(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	remote := &fakeOwner{info: ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead, CapabilityFixtureCreateBound)}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	target := remoteTarget(t, workspace, "lab")
	created, err := workspace.WorkspaceClient().CreateFixture(ctx, target.Target, target.Token, FixtureDraft{ID: "created", Name: "Created", Tool: "codex", Cwd: "/tmp/created", Group: "poc"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.ID != "created" || created.Target.Owner != target.Target || created.Token != target.Token || remote.lastGuard.ExpectedEnvironmentID != "env-remote" || remote.lastGuard.ExpectedOwnerInstanceID != "owner-remote" {
		t.Fatalf("bound create = %+v guard=%+v", created, remote.lastGuard)
	}
	if remote.inspectCalls != 1 {
		t.Fatalf("mutation rediscovered owner; inspect calls=%d", remote.inspectCalls)
	}
}

func TestRetargetDoesNotMisapplyAnAlreadyDispatchedRenameResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	remote := &fakeOwner{
		info:     ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead, CapabilityFixtureRename),
		sessions: []Session{fixture("one", "core")}, renameStarted: started, renameRelease: release,
	}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	row := remoteTarget(t, workspace, "lab").Sessions[0]
	done := make(chan struct {
		result MutationResult
		err    error
	}, 1)
	go func() {
		result, err := workspace.WorkspaceClient().RenameFixture(ctx, row.Target, row.Token, "renamed-old")
		done <- struct {
			result MutationResult
			err    error
		}{result: result, err: err}
	}()
	<-started
	if _, err := workspace.RetargetConnection(ctx, "lab", Endpoint{Profile: "new-route"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	completed := <-done
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	if completed.result.Target != row.Target || completed.result.Token != row.Token {
		t.Fatalf("mutation lost dispatch context: %+v", completed.result)
	}
	state, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Connections[0].Endpoint.Profile != "new-route" || state.Connections[0].Projection.RefreshSequence != 0 {
		t.Fatalf("old mutation reconciled into retargeted state: %+v", state.Connections[0])
	}
}

func TestStaleOfflineProjectionAllowsGuardedFixtureMetadataAttempt(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	remote := &fakeOwner{
		info:     ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead, CapabilityFixtureRename),
		sessions: []Session{fixture("one", "core")},
	}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	row := remoteTarget(t, workspace, "lab").Sessions[0]
	remote.mu.Lock()
	remote.listErr = NewError(CodeOffline, "read link down", nil)
	remote.mu.Unlock()
	if _, err := workspace.RefreshConnection(ctx, "lab"); CodeOf(err) != CodeOffline {
		t.Fatalf("refresh error = %v", err)
	}
	result, err := workspace.WorkspaceClient().RenameFixture(ctx, row.Target, row.Token, "metadata-only")
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Name != "metadata-only" {
		t.Fatalf("guarded stale metadata result = %+v", result)
	}
}

func remoteTarget(t *testing.T, workspace *Workspace, connectionID string) TargetProjection {
	t.Helper()
	inventory, err := workspace.WorkspaceClient().Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range inventory.Targets {
		if target.Target.ConnectionID == connectionID {
			return target
		}
	}
	t.Fatalf("connection %q missing from %+v", connectionID, inventory)
	return TargetProjection{}
}

func TestDeviceClientRejectsForgedConnectionMutation(t *testing.T) {
	local := &fakeOwner{info: ownerInfo("env-local", "owner-local", 3, CapabilitySessionsRead)}
	remote := &fakeOwner{
		info:     ownerInfo("env-remote", "owner-remote", 3, CapabilitySessionsRead, CapabilityFixtureRename),
		sessions: []Session{fixture("remote", "core")},
	}
	workspace, _, _ := newTestWorkspace(local, map[string]*fakeOwner{"remote": remote})
	ctx := context.Background()
	if _, err := workspace.Connect(ctx, NewConnection{ID: "lab", Label: "Lab", Endpoint: Endpoint{Profile: "remote"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RefreshConnection(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	row := remoteTarget(t, workspace, "lab").Sessions[0]
	if _, err := workspace.DeviceClient().RenameFixture(ctx, row.Target, row.Token, "forged"); CodeOf(err) != CodeScopeViolation {
		t.Fatalf("device forged mutation error = %v", err)
	}
	if remote.renameCalls != 0 {
		t.Fatalf("device client reached remote owner %d times", remote.renameCalls)
	}
}

func TestCodeOfPreservesTypedErrorsAndClassifiesUnknownFailures(t *testing.T) {
	if got := CodeOf(NewError(CodeOffline, "down", nil)); got != CodeOffline {
		t.Fatalf("typed code = %q", got)
	}
	if got := CodeOf(errors.New("boom")); got != CodeOperationFailed {
		t.Fatalf("unknown code = %q", got)
	}
}
