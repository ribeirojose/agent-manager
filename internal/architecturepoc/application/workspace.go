package application

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type Workspace struct {
	repository    WorkspaceRepository
	owners        OwnerResolver
	localEndpoint Endpoint
	now           func() time.Time
}

func NewWorkspace(repository WorkspaceRepository, owners OwnerResolver, localEndpoint Endpoint, now func() time.Time) *Workspace {
	if now == nil {
		now = time.Now
	}
	return &Workspace{repository: repository, owners: owners, localEndpoint: localEndpoint, now: now}
}

func (workspace *Workspace) DeviceClient() *Client {
	return &Client{workspace: workspace, scope: ScopeDevice}
}

func (workspace *Workspace) WorkspaceClient() *Client {
	return &Client{workspace: workspace, scope: ScopeWorkspace}
}

func (workspace *Workspace) Snapshot(ctx context.Context) (WorkspaceState, error) {
	var result WorkspaceState
	err := workspace.repository.View(ctx, func(state WorkspaceState) error {
		result = cloneState(state)
		return nil
	})
	return result, err
}

func (workspace *Workspace) Connect(ctx context.Context, candidate NewConnection) (Connection, error) {
	candidate.ID = normalized(candidate.ID)
	candidate.Label = normalized(candidate.Label)
	candidate.Endpoint.Profile = normalized(candidate.Endpoint.Profile)
	if candidate.ID == "" || candidate.Label == "" || candidate.Endpoint.Profile == "" {
		return Connection{}, NewError(CodeInvalidRequest, "connection id, label, and profile are required", nil)
	}
	owner, err := workspace.owners.Open(ctx, candidate.Endpoint)
	if err != nil {
		return Connection{}, err
	}
	info, err := owner.Inspect(ctx)
	if err != nil {
		return Connection{}, err
	}
	if err := validOwner(info); err != nil {
		return Connection{}, err
	}
	var connected Connection
	err = workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		if _, found := findConnection(*state, candidate.ID); found {
			return NewError(CodeAlreadyExists, fmt.Sprintf("connection %q already exists", candidate.ID), nil)
		}
		connected = Connection{
			ID: candidate.ID, Label: candidate.Label, Endpoint: candidate.Endpoint,
			Generation: takeGeneration(state), SavedEnvironmentID: info.EnvironmentID,
		}
		state.Connections = append(state.Connections, connected)
		sort.Slice(state.Connections, func(i, j int) bool { return state.Connections[i].ID < state.Connections[j].ID })
		return nil
	})
	return connected, err
}

func (workspace *Workspace) RenameConnection(ctx context.Context, id, label string) (Connection, error) {
	id, label = normalized(id), normalized(label)
	if label == "" {
		return Connection{}, NewError(CodeInvalidRequest, "connection label is required", nil)
	}
	var renamed Connection
	err := workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		index, found := findConnection(*state, id)
		if !found {
			return connectionMissing(id)
		}
		state.Connections[index].Label = label
		renamed = cloneState(*state).Connections[index]
		return nil
	})
	return renamed, err
}

func (workspace *Workspace) RetargetConnection(ctx context.Context, id string, endpoint Endpoint) (Connection, error) {
	id, endpoint.Profile = normalized(id), normalized(endpoint.Profile)
	if endpoint.Profile == "" {
		return Connection{}, NewError(CodeInvalidRequest, "connection profile is required", nil)
	}
	var updated Connection
	err := workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		index, found := findConnection(*state, id)
		if !found {
			return connectionMissing(id)
		}
		connection := &state.Connections[index]
		connection.Endpoint = endpoint
		connection.Generation = takeGeneration(state)
		connection.SavedEnvironmentID = ""
		connection.NextRefresh = 0
		connection.Projection = Projection{}
		updated = cloneState(*state).Connections[index]
		return nil
	})
	return updated, err
}

func (workspace *Workspace) Reconnect(ctx context.Context, id string) (Connection, error) {
	id = normalized(id)
	var observed Connection
	if err := workspace.repository.View(ctx, func(state WorkspaceState) error {
		index, found := findConnection(state, id)
		if !found {
			return connectionMissing(id)
		}
		observed = cloneState(state).Connections[index]
		return nil
	}); err != nil {
		return Connection{}, err
	}
	owner, err := workspace.owners.Open(ctx, observed.Endpoint)
	if err != nil {
		return Connection{}, err
	}
	info, err := owner.Inspect(ctx)
	if err != nil {
		return Connection{}, err
	}
	if err := validOwner(info); err != nil {
		return Connection{}, err
	}
	var reconnected Connection
	err = workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		index, found := findConnection(*state, id)
		if !found || state.Connections[index].Generation != observed.Generation || state.Connections[index].Endpoint != observed.Endpoint {
			return staleTarget("connection changed while reconnecting")
		}
		connection := &state.Connections[index]
		connection.Generation = takeGeneration(state)
		connection.SavedEnvironmentID = info.EnvironmentID
		connection.NextRefresh = 0
		connection.Projection = Projection{}
		reconnected = cloneState(*state).Connections[index]
		return nil
	})
	return reconnected, err
}

func (workspace *Workspace) RemoveConnection(ctx context.Context, id string) error {
	id = normalized(id)
	return workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		index, found := findConnection(*state, id)
		if !found {
			return connectionMissing(id)
		}
		state.Connections = append(state.Connections[:index], state.Connections[index+1:]...)
		return nil
	})
}

func (workspace *Workspace) RefreshConnection(ctx context.Context, id string) (TargetProjection, error) {
	id = normalized(id)
	var dispatched Connection
	err := workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		index, found := findConnection(*state, id)
		if !found {
			return connectionMissing(id)
		}
		connection := &state.Connections[index]
		if connection.SavedEnvironmentID == "" {
			return NewError(CodeUnboundTarget, "retargeted connection must be reconnected before refresh", nil)
		}
		connection.NextRefresh++
		dispatched = cloneState(*state).Connections[index]
		return nil
	})
	if err != nil {
		return TargetProjection{}, err
	}
	owner, err := workspace.owners.Open(ctx, dispatched.Endpoint)
	if err != nil {
		return TargetProjection{}, workspace.recordRefreshFailure(ctx, dispatched, err)
	}
	inventory, err := owner.List(ctx)
	if err != nil {
		return TargetProjection{}, workspace.recordRefreshFailure(ctx, dispatched, err)
	}
	if err := validOwner(inventory.Owner); err != nil {
		return TargetProjection{}, workspace.recordRefreshFailure(ctx, dispatched, err)
	}
	if inventory.Owner.EnvironmentID != dispatched.SavedEnvironmentID {
		mismatch := NewError(CodeEnvironmentMismatch,
			fmt.Sprintf("saved environment %q does not match observed environment %q", dispatched.SavedEnvironmentID, inventory.Owner.EnvironmentID), nil)
		return TargetProjection{}, workspace.recordRefreshFailure(ctx, dispatched, mismatch)
	}
	now := workspace.now()
	var connection Connection
	err = workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		index, found := findConnection(*state, id)
		if !found || state.Connections[index].Generation != dispatched.Generation || state.Connections[index].NextRefresh != dispatched.NextRefresh {
			return staleTarget("connection changed while refresh was in flight")
		}
		current := &state.Connections[index]
		current.Projection = Projection{
			RefreshSequence: dispatched.NextRefresh, Owner: cloneOwner(inventory.Owner),
			Sessions: append([]Session(nil), inventory.Sessions...), RefreshedAt: now, AttemptedAt: now,
		}
		connection = cloneState(*state).Connections[index]
		return nil
	})
	if err != nil {
		return TargetProjection{}, err
	}
	return projectConnection(connection), nil
}

func (workspace *Workspace) recordRefreshFailure(ctx context.Context, dispatched Connection, cause error) error {
	now := workspace.now()
	err := workspace.repository.Update(ctx, func(state *WorkspaceState) error {
		index, found := findConnection(*state, dispatched.ID)
		if !found || state.Connections[index].Generation != dispatched.Generation || state.Connections[index].NextRefresh != dispatched.NextRefresh {
			return staleTarget("connection changed while failed refresh was in flight")
		}
		state.Connections[index].Projection.AttemptedAt = now
		state.Connections[index].Projection.Failure = failureOf(cause)
		return nil
	})
	if err != nil {
		return err
	}
	return cause
}

func (workspace *Workspace) inventory(ctx context.Context, scope Scope) (Inventory, error) {
	result := Inventory{Scope: scope}
	local := TargetProjection{Target: DeviceTarget(), Label: "local"}
	if owner, err := workspace.owners.Open(ctx, workspace.localEndpoint); err != nil {
		local.Freshness = Freshness{State: FreshnessUnread, AttemptedAt: workspace.now(), Failure: failureOf(err)}
	} else if listed, err := owner.List(ctx); err != nil {
		local.Freshness = Freshness{State: FreshnessUnread, AttemptedAt: workspace.now(), Failure: failureOf(err)}
	} else if err := validOwner(listed.Owner); err != nil {
		local.Freshness = Freshness{State: FreshnessUnread, AttemptedAt: workspace.now(), Failure: failureOf(err)}
	} else {
		now := workspace.now()
		local.Owner = cloneOwner(listed.Owner)
		local.Token = tokenFor(local.Target, 0, 0, listed.Owner)
		local.Freshness = Freshness{State: FreshnessFresh, RefreshedAt: now, AttemptedAt: now}
		local.Sessions = projectSessions(local.Target, local.Token, listed.Sessions)
	}
	if err := workspace.repository.View(ctx, func(state WorkspaceState) error {
		if state.DeviceID != "" {
			local.Label = state.DeviceID
		}
		if scope == ScopeWorkspace {
			connections := cloneState(state).Connections
			sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })
			for _, connection := range connections {
				result.Targets = append(result.Targets, projectConnection(connection))
			}
		}
		return nil
	}); err != nil {
		return Inventory{}, err
	}
	result.Targets = append([]TargetProjection{local}, result.Targets...)
	return result, nil
}

func projectConnection(connection Connection) TargetProjection {
	target := ConnectionTarget(connection.ID)
	projection := TargetProjection{Target: target, Label: connection.Label, Owner: cloneOwner(connection.Projection.Owner)}
	if connection.Projection.RefreshSequence == 0 {
		projection.Freshness = Freshness{State: FreshnessUnread, AttemptedAt: connection.Projection.AttemptedAt, Failure: connection.Projection.Failure}
		return projection
	}
	projection.Token = tokenFor(target, connection.Generation, connection.Projection.RefreshSequence, connection.Projection.Owner)
	state := FreshnessFresh
	if connection.Projection.Failure != nil {
		state = FreshnessStale
	}
	projection.Freshness = Freshness{
		State: state, RefreshedAt: connection.Projection.RefreshedAt, AttemptedAt: connection.Projection.AttemptedAt,
		Failure: connection.Projection.Failure,
	}
	projection.Sessions = projectSessions(target, projection.Token, connection.Projection.Sessions)
	return projection
}

func projectSessions(owner OwnerTarget, token ReaderToken, sessions []Session) []SessionProjection {
	result := make([]SessionProjection, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, SessionProjection{Target: SessionTarget{Owner: owner, SessionID: session.ID}, Token: token, Session: session})
	}
	return result
}

func validOwner(info OwnerInfo) error {
	if normalized(info.EnvironmentID) == "" || normalized(info.InstanceID) == "" {
		return NewError(CodeOperationFailed, "owner omitted environment or instance identity", nil)
	}
	return nil
}

func takeGeneration(state *WorkspaceState) uint64 {
	if state.NextGeneration == 0 {
		state.NextGeneration = 1
	}
	generation := state.NextGeneration
	state.NextGeneration++
	return generation
}

func findConnection(state WorkspaceState, id string) (int, bool) {
	for index := range state.Connections {
		if state.Connections[index].ID == id {
			return index, true
		}
	}
	return 0, false
}

func connectionMissing(id string) error {
	return NewError(CodeNotFound, fmt.Sprintf("connection %q does not exist", id), nil)
}

func staleTarget(message string) error { return NewError(CodeStaleTarget, message, nil) }
