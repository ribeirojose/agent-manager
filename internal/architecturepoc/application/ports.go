package application

import "context"

type WorkspaceRepository interface {
	View(context.Context, func(WorkspaceState) error) error
	Update(context.Context, func(*WorkspaceState) error) error
}

type OwnerResolver interface {
	Open(context.Context, Endpoint) (OwnerClient, error)
}

type OwnerClient interface {
	Inspect(context.Context) (OwnerInfo, error)
	List(context.Context) (OwnerInventory, error)
	CreateBound(context.Context, MutationGuard, FixtureDraft) (OwnerMutation, error)
	Rename(context.Context, MutationGuard, string, string) (OwnerMutation, error)
}
