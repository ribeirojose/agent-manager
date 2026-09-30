# Reorganize the application around responsibilities

The proposal covers Agent Manager as a product. Its implementation should remain incremental. The application POC samples representative boundaries; it does not port every existing behavior or replace the running delivery branch.

## Keep useful existing modules

At the frozen upstream revision, `internal/ui` contains 23,151 production Go lines, about 53 percent of the application's 43,560. The largest remaining packages are `store` at 2,703 lines, `sessioncmd` at 2,677, and `tmux` at 1,454. These are physical lines with comments and blanks. The counts were measured at that frozen revision. File size explains review cost but does not determine ownership.

| Existing area | Proposed responsibility | Refactor decision |
|---|---|---|
| `ui`, CLI and `mcpserver` | Presentation and caller adaptation into application operations | Move feature state as Yoan suggests; retain adapter-specific defaults and messages; remove direct lifecycle authority incrementally |
| `sessioncmd`, poller lifecycle and mailbox handling | Execution-profile use cases and owner maintenance | Consolidate duplicated behavior behind the owner; separate inventory/preview queries from mutating maintenance |
| Connections, remote projections and workspace aggregation | Controller workspace coordination | Stable connection identity, generation-bound results and explicit workspace versus device scope |
| `store`, `tmux`, hooks and `agentsession` | Persistence and execution adapters | Keep existing implementation libraries and append-only migrations; add consumer-defined ports only where they eliminate concrete coupling |
| Config, status, git/review, notices and platform utilities | Configuration, domain policy or supporting adapters according to their actual role | Mechanical splits can proceed; do not force each file or UI dialog into a DDD bounded context |

## Change authority, not only file placement

The current TUI owns a poller. `ui.Model.StartPoller` starts it, viewer selection and archive flags enter its refresh, and `poller.refreshOnce` reads panes, derives status, writes metadata, applies mailbox changes and handles pending delivery. The frozen connections draft adds a headless service that calls shared runtime logic. Sharing that logic leaves the TUI and headless process capable of running it independently.

The target is an execution owner that alone schedules maintenance for its declared profile and execution bindings. TUI, CLI and MCP use its query or command contract. Preview selection can change a query; it cannot change maintenance scheduling. The actual canonical ownership key must account for the store and driver/socket bindings. The POC uses one disposable profile and no tmux socket, so it cannot resolve that production key by itself.

`sessioncmd` already provides shared CLI and MCP behavior. Extend this precedent instead of creating a second competing lifecycle framework. Explicit use cases should own their target validation and effects. They should not borrow a remote session to impersonate an external controller or infer a remote working directory from the local caller.

## Use DDD and patterns selectively

The two consequential models are workspace coordination and authoritative execution state. A remote projection is not the local aggregate. A label is not an identity. An owner instance is not a protocol version. Model these distinctions with small Go types and explicit states.

Ports and adapters separate execution, persistence, transport and entry points. The command/query distinction separates effects from views. A translating adapter isolates compatible old wire representations from domain code. Feature composition separates UI state and dispatch. These patterns answer demonstrated problems; a generic plugin framework, event sourcing or an interface for each helper would add work without addressing those problems.

The owner boundary must not become one huge serialized profile aggregate. Independent sessions can operate concurrently, while operations that conflict on one session or pane need their own atomic decisions. Transport capabilities establish whether a behavior is available, not whether a caller is authorized or an old token is still valid.

## Preserve Yoan's plan where its scope is sufficient

Mechanical splits, preference grouping, error reporting helpers, layout-before-View, narrow feature hosts and source-adjacent tests are useful. Root field count and file length are guardrails, not architectural acceptance tests. A `services` struct can still expose unrestricted SQLite and tmux access, and a small `handleMsg` can still apply a stale remote response.

Lifecycle consolidation is the step that requires a stronger contract. Decide owner/viewer responsibilities before cementing the poller split. Preserve frame geometry, diff generations, preview hold behavior, mouse capture and persistent preferences with existing integration tests. The new POC tests async feature state, but does not validate those five existing UI seams or every real terminal/platform behavior.

## Deliver small units with compatibility gates

Finish the active device-scope delivery case without redirecting that run. Then publish the smallest owner/viewer contract change that proves headless maintenance and TUI observation. Continue with saved connection inspection/recovery and one guarded owner command. Port feature UI state in reviewable changes alongside those boundaries, not as one rewrite of the entire application.

Protocol semantics, running-owner capabilities, MCP contracts and SQLite writer compatibility are separate release contracts. Test real supported client and owner binaries before promising mixed-version support. Existing TUI/MCP processes that directly mutate shared state must be upgraded, quiesced or isolated. A new capability response cannot retroactively constrain them.

Connection replacement and owner restart need separate guards. A copied database preserves profile lineage; use independently verified endpoint/deployment identity in production. Async generations prevent a late read from replacing a newer connection state. Safe mutation retries need durable outcomes; raw pane input must not be replayed merely because SSH disconnected. Shared viewing can precede same-pane writer claims and fencing.
