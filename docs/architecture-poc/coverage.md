# What the architecture experiment must show

The target architecture covers the whole application. This proof selects representative flows. It is not a feature-complete rewrite or acceptance evidence for the running delivery branch.

| Concern from the conversation | POC evidence required | Production proof still required |
|---|---|---|
| Yoan's feature UI types, smaller root, pure views and shared lifecycle | Two Bubble Tea feature types use shared typed application operations; slow I/O is returned as a command; late replies cannot overwrite newer state | Existing focus/review/mouse/geometry/preferences seams, real UI driving, all supported terminal and platform behavior |
| Saved app-managed connections, headless operation and projections | Persisted labeled routes, colliding IDs, local and remote owner processes without TUI, stale/offline projections, label rename preserving identity | Canonical store/socket ownership, existing headless delivery, real bootstrap and long-lived legacy-writer cutover |
| Local MCP outward coordination with remote device scope | Actual SDK/stdin tools use the same services as CLI; device facade cannot list or mutate remote targets even when outbound connections exist | Existing long-lived production MCP clients and scope-policy upgrades, explicit external actor defaults |
| DDD and patterns that accommodate extensions | Workspace and execution state remain separate; JSON and process/SSH handling stay in adapters; filtered query and guarded rename compose through narrow ports | Consolidate existing sessioncmd/TUI use cases with cleanup, rollback, worktree, hook and label parity |
| Multiple client versions, reconnect and replacement | Safe reads across synthetic revisions; guarded writes unavailable on old owners; newest owner rejects old unguarded writes; generation/instance mismatch rejects stale effects | Actual release binary/schema matrix, independent endpoint/deployment identity, durable mutation receipts, uncertain-outcome recovery and same-pane input fencing |

Use the old owner-only proof as a component. Do not describe local profiles used as remote stand-ins as real network evidence. The transport runner separately exercises real SSH/Linux owners. The application runner must explicitly identify whether its saved route uses that SSH adapter.

Examples of domain extensions are illustrative. A filtered view and fixture rename are not substitutes for real queue, reservation, task or terminal features. Their value is whether new behavior fits without a generic command bus or transport branches spreading into application code.
