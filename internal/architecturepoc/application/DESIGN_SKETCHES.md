# Application workspace sketches

## Usage first

Every front composes the same typed application object. Inventory produces a target and reader token. A later mutation must return that exact token; it never discovers a replacement owner and silently grants the command fresh authority.

```go
client := workspace.WorkspaceClient() // immutable scope for this front
inventory, _ := client.Inventory(ctx)
row := inventory.Targets[1].Sessions[0]
renamed, err := client.RenameFixture(ctx, row.Target, row.Token, "new-name")
```

The owner port is consumer-defined and operation-specific:

```go
type OwnerClient interface {
	Inspect(context.Context) (OwnerInfo, error)
	List(context.Context) (OwnerInventory, error)
	CreateBound(context.Context, MutationGuard, FixtureDraft) (OwnerMutation, error)
	Rename(context.Context, MutationGuard, string, string) (OwnerMutation, error)
}
```

## Alternative A: extend the existing shared command layer

The existing `sessioncmd` layer could gain typed targets and an owner resolver while a separate connection repository supplies routes and cached reads. This reuses a battle-tested parity boundary and avoids introducing a parallel lifecycle implementation. It is a credible incremental production direction.

The drawback for this POC is that workspace identity, projection freshness, immutable client scope, and connection generations would remain split between the new repository and a layer whose current defaults assume local calling sessions. Each front would still need composition rules for cached remotes and device-only scope. Extending it safely first requires extracting those assumptions from real lifecycle behavior, which the fixture slice must not pretend to replace.

## Alternative B: workspace application services

`Workspace` owns connection identity, saved environment lineage, generation and refresh fencing, cached projections, scope, and mutation preconditions. It depends on one aggregate repository and a resolver that returns the narrow owner port above. JSON, process execution, SSH, owner envelopes, CLI flags, MCP schemas, and token encoding remain adapters.

Filtered inventory and fixture rename are example extensions with narrow consumer interfaces. They compose into CLI, MCP, and two Bubble Tea feature types. The root TUI model only routes messages; each feature owns its loading, result, error, and request generation.

## Decision

Build alternative B. Its public surface hides route dispatch, persistence shape, wire compatibility, and stale-result rules while keeping the domain small. The cost is a few explicit types and ports. Those types eliminate invalid combinations that alternative A would repeatedly check.

No generic service locator, plugin registry, event stream, command-string owner API, or framework layer is introduced. The SSH distinction stays in the command-runner adapter. Capabilities report operation availability; guarded owner operations check live capability and identity preconditions. They do not authenticate a security principal sharing the same OS account.

## Known proof boundary

The slice uses inventory-only fixture creation and rename. It proves client composition, read projection semantics, transport translation, and identity fencing. It does not prove tmux ownership, real agent launch, queue delivery, same-pane input claims, review flows, or compatibility with historical production binaries and schemas.

A retarget prevents future dispatches with an old token. It cannot revoke a command already accepted by the old owner. Mutation results therefore retain the immutable target and token used at dispatch; presentation features discard a reply after their selection generation changes. The POC has no receipt or retry claim.

An offline read leaves a stale projection selectable. This fixture-metadata example permits a guarded command attempt from that token; the live owner still checks environment and instance identity. That narrow choice is not a policy for destructive lifecycle or pane-input operations.
