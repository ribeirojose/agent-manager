package application

import (
	"context"
	"fmt"
)

type Client struct {
	workspace *Workspace
	scope     Scope
}

func (client *Client) Scope() Scope { return client.scope }

func (client *Client) Inventory(ctx context.Context) (Inventory, error) {
	return client.workspace.inventory(ctx, client.scope)
}

func (client *Client) CreateFixture(ctx context.Context, target OwnerTarget, token ReaderToken, draft FixtureDraft) (MutationResult, error) {
	endpoint, guard, err := client.resolveMutation(ctx, target, token, CapabilityFixtureCreateBound)
	if err != nil {
		return MutationResult{}, err
	}
	owner, err := client.workspace.owners.Open(ctx, endpoint)
	if err != nil {
		return MutationResult{}, err
	}
	mutation, err := owner.CreateBound(ctx, guard, draft)
	if err != nil {
		return MutationResult{}, err
	}
	if err := verifyMutationOwner(mutation.Owner, token); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{
		Target: SessionTarget{Owner: target, SessionID: mutation.Session.ID}, Token: token,
		Session: mutation.Session, Notice: mutation.Notice,
	}, nil
}

func (client *Client) RenameFixture(ctx context.Context, target SessionTarget, token ReaderToken, name string) (MutationResult, error) {
	name = normalized(name)
	if normalized(target.SessionID) == "" || name == "" {
		return MutationResult{}, NewError(CodeInvalidRequest, "session target and name are required", nil)
	}
	endpoint, guard, err := client.resolveMutation(ctx, target.Owner, token, CapabilityFixtureRename)
	if err != nil {
		return MutationResult{}, err
	}
	owner, err := client.workspace.owners.Open(ctx, endpoint)
	if err != nil {
		return MutationResult{}, err
	}
	mutation, err := owner.Rename(ctx, guard, target.SessionID, name)
	if err != nil {
		return MutationResult{}, err
	}
	if err := verifyMutationOwner(mutation.Owner, token); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{Target: target, Token: token, Session: mutation.Session, Notice: mutation.Notice}, nil
}

func (client *Client) resolveMutation(ctx context.Context, target OwnerTarget, token ReaderToken, capability string) (Endpoint, MutationGuard, error) {
	if target != token.Target || token.EnvironmentID == "" || token.OwnerInstanceID == "" {
		return Endpoint{}, MutationGuard{}, staleTarget("reader token does not identify the selected target")
	}
	if client.scope == ScopeDevice && target.Kind != TargetDevice {
		return Endpoint{}, MutationGuard{}, NewError(CodeScopeViolation, "device client cannot traverse a saved outbound connection", nil)
	}
	if capability == CapabilityFixtureCreateBound && !token.CanCreateBoundFixture {
		return Endpoint{}, MutationGuard{}, NewError(CodeMissingCapability, "selected owner does not advertise bound fixture creation", nil)
	}
	if capability == CapabilityFixtureRename && !token.CanRenameFixture {
		return Endpoint{}, MutationGuard{}, NewError(CodeMissingCapability, "selected owner does not advertise fixture rename", nil)
	}
	guard := MutationGuard{ExpectedEnvironmentID: token.EnvironmentID, ExpectedOwnerInstanceID: token.OwnerInstanceID}
	if target.Kind == TargetDevice {
		return client.workspace.localEndpoint, guard, nil
	}
	if target.Kind != TargetConnection || normalized(target.ConnectionID) == "" {
		return Endpoint{}, MutationGuard{}, NewError(CodeInvalidRequest, "unknown mutation target", nil)
	}
	var endpoint Endpoint
	err := client.workspace.repository.View(ctx, func(state WorkspaceState) error {
		index, found := findConnection(state, target.ConnectionID)
		if !found {
			return staleTarget("selected connection was removed")
		}
		connection := state.Connections[index]
		projection := connection.Projection
		if token.Generation != connection.Generation || token.RefreshSequence != projection.RefreshSequence ||
			token.EnvironmentID != connection.SavedEnvironmentID || token.OwnerInstanceID != projection.Owner.InstanceID {
			return staleTarget("selected projection is no longer current")
		}
		endpoint = connection.Endpoint
		return nil
	})
	return endpoint, guard, err
}

func verifyMutationOwner(owner OwnerInfo, token ReaderToken) error {
	if owner.EnvironmentID != token.EnvironmentID {
		return NewError(CodeEnvironmentMismatch,
			fmt.Sprintf("mutation response environment %q does not match token %q", owner.EnvironmentID, token.EnvironmentID), nil)
	}
	if owner.InstanceID != token.OwnerInstanceID {
		return NewError(CodeOwnerInstanceMismatch,
			fmt.Sprintf("mutation response owner %q does not match token %q", owner.InstanceID, token.OwnerInstanceID), nil)
	}
	return nil
}
