# Application architecture experiment

This fork-only POC makes the application-wide proposal in upstream issue #646 reviewable. It adds a disposable vertical slice across workspace coordination, execution ownership, CLI, actual MCP, headless Bubble Tea feature models and local/SSH adapters. Existing production code and SQLite migrations are unchanged.

## Read the proposal

[Application-wide design](application-architecture.md) explains the responsibility boundaries and patterns. [Migration map](scope-and-migration.md) ties those boundaries to current packages and Yoan's plan. [Coverage](coverage.md) separates representative checks from production requirements. [Independent review](application-review.md) records resolved findings and remaining risks.

Use a workspace coordinator for connections/projections/scope and retain canonical execution use cases based on sessioncmd behind an owner. The fixture operations in this POC are not a second production lifecycle implementation.

The PR targets a frozen baseline branch at upstream commit `87569e49b62e4dcf9dfd8b754494f9b6ab1d93a8` so its diff contains only this experiment. It is intended for architectural review, not merging into production.

## Run from the repository root

```sh
go build -o /tmp/am-poc-owner ./cmd/architecture-poc
go build -o /tmp/am-poc-workspace ./cmd/architecture-workspace-poc
/tmp/am-poc-workspace demo --owner-binary /tmp/am-poc-owner
```

The demo starts two real owner processes with temporary SQLite profiles. A second local profile models outward placement. It proves namespaced colliding IDs, persisted labels, token preservation across label rename and guarded fixture rename. It emits one JSON document and stops its owners. Fixture operations do not launch agents or touch tmux.

## Verify clients and extensions

```sh
env -u TMUX GORACE=atexit_sleep_ms=0 go test -race ./internal/architecturepoc/... ./cmd/architecture-poc ./cmd/architecture-workspace-poc
go vet ./internal/architecturepoc/... ./cmd/architecture-poc ./cmd/architecture-workspace-poc
python3 docs/architecture-poc/application_examples.py --workspace-binary /tmp/am-poc-workspace --owner-binary /tmp/am-poc-owner
```

The two example extensions are a filtered inventory query and guarded fixture metadata rename. Actual CLI and SDK stdio MCP use the same application services. Tests exercise async Bubble Tea Update/command behavior without an interactive terminal.

For the optional saved real SSH route, cross-build and supply your destination:

```sh
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/am-poc-owner-linux ./cmd/architecture-poc
python3 docs/architecture-poc/application_examples.py --workspace-binary /tmp/am-poc-workspace --owner-binary /tmp/am-poc-owner --ssh-host user@host --linux-owner-binary /tmp/am-poc-owner-linux
```

The SSH test uses isolated temporary profiles, disables forwarding and verifies process identity/exit before cleanup. Remote Python pidfd support is required. The original full run passed 35 assertions across 91 events, including concurrent CLI saves, two long-lived MCP scopes, scope forgery denial, cross-client rename visibility and a real saved SSH route. Local-only runs skip the network case. Machine-specific execution logs are not included in this PR; the runner generates its own evidence.

## Proof limits

Owner locks exclude cooperating POC processes only. Production needs legacy writer upgrade/quiesce/isolation and real sessioncmd/TUI lifecycle parity. Safe reads remain available across synthetic revisions; newest owners reject older unguarded writes. This is not released-binary/schema interoperability evidence.

A copied SQLite profile preserves lineage identity; owner-instance guards reject stale instances, while verified endpoint/deployment identity remains a production requirement. Retarget blocks future stale dispatches but cannot revoke an already-sent command. Result context and UI generations prevent stale reconciliation.

Actual maintenance/queue delivery, automatic headless bootstrap, full focus/review/mouse/layout behavior, durable receipts, agent/tmux lifecycle and same-pane input fencing remain outside this proof. The POC is macOS/Linux scoped.
