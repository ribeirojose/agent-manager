package extensions

import (
	"context"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

type FixtureRenamer interface {
	RenameFixture(context.Context, application.SessionTarget, application.ReaderToken, string) (application.MutationResult, error)
}

type RenameRequest struct {
	Target application.SessionTarget
	Token  application.ReaderToken
	Name   string
}

type FixtureRename struct {
	renamer FixtureRenamer
}

func NewFixtureRename(renamer FixtureRenamer) *FixtureRename {
	return &FixtureRename{renamer: renamer}
}

func (command *FixtureRename) Execute(ctx context.Context, request RenameRequest) (application.MutationResult, error) {
	return command.renamer.RenameFixture(ctx, request.Target, request.Token, request.Name)
}
