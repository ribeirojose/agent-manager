# Application architecture experiment

This fork-only POC makes the application-wide proposal in upstream issue #646 reviewable. It adds a disposable vertical slice across workspace coordination, execution ownership, CLI, actual MCP, headless Bubble Tea feature models and local/SSH adapters. The first commit adds the isolated experiment. The next commit migrates real production archive and inbox-retention code behind canonical owner ports; SQLite migrations remain unchanged.

## Read the proposal

[Application-wide design](application-architecture.md) explains the responsibility boundaries and patterns. [Migration map](scope-and-migration.md) ties those boundaries to current packages and Yoan's plan. [Coverage](coverage.md) separates representative checks from production requirements. [Independent review](application-review.md) records resolved findings and remaining risks.

Use a workspace coordinator for connections/projections/scope and retain canonical execution use cases based on sessioncmd behind an owner. The fixture operations in this POC are not a second production lifecycle implementation. [The first production-code migration](migration-design.md) moves existing agent-command archive and delivered-inbox retention into canonical handlers and wires real frontends against owner RPC.

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

## Replay the real production-code migration

```sh
mkdir -p /tmp/am-migration-tests
env -u TMUX TMUX_TMPDIR=/tmp/am-migration-tests go test -race -v ./cmd/architecture-poc -run '^TestMigra' -count=1
```

This test builds the POC binary and creates disposable SQLite, owner processes, named tmux servers, and a throwaway TUI home. It drives the actual CLI `archive`, actual stdio MCP `archive_session`, and rendered production TUI polling. It verifies live-pane survival and snapshot capture, flag-only restore, invalid caller/self/terminal rejection, retention by delivery time, stale owner-instance rejection, and refusal of the production tmux socket. Owner logs confirm the two archive effects and TUI maintenance ran in the owner process. Cleanup targets only the test's named panes and owner processes.

The migration server is `serve-migration --dir PATH --tmux-socket am-poc-NAME` (synthetic revision 4). `migration-cli --dir PATH --caller ID -- archive TARGET [--restore] [--json]`, `migration-mcp --dir PATH --caller ID`, and `migration-tui --dir PATH --tmux-socket am-poc-NAME` compose existing production frontends. These commands are for disposable profiles. The test handles setup automatically.

Default production entrypoints still use local canonical handlers. The explicit factories bind RPC owners without local fallback after an error. This is a real extraction and caller migration, not a default daemon cutover. TUI archive's kill/revive/group use case and delivery heartbeat remain separate. [Migration review and validation](migration-review.md) records the checks and current full-suite failures.

## Proof limits

Owner locks exclude cooperating POC processes only. Production needs legacy writer upgrade/quiesce/isolation and real sessioncmd/TUI lifecycle parity. Safe reads remain available across synthetic revisions; newest owners reject older unguarded writes. This is not released-binary/schema interoperability evidence.

A copied SQLite profile preserves lineage identity; owner-instance guards reject stale instances, while verified endpoint/deployment identity remains a production requirement. Retarget blocks future stale dispatches but cannot revoke an already-sent command. Result context and UI generations prevent stale reconciliation.

Delivered-inbox retention is now proved through the real owner process. Queue delivery and other maintenance, automatic headless bootstrap, full focus/review/mouse/layout parity, durable receipts, agent launch/kill/revive and same-pane input fencing remain outside this proof. The POC is macOS/Linux scoped.
