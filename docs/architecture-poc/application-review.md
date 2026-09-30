# Independent application architecture review

Reviewed against upstream commit `87569e49b62e4dcf9dfd8b754494f9b6ab1d93a8`, the frozen #646 packet, the owner POC, the application slice under `internal/architecturepoc`, `cmd/architecture-workspace-poc`, and the final application evidence.

## Verdict

Approve the application POC as evidence for the proposed direction. It demonstrates a small workspace model, a separate authoritative owner port, fixed client scopes, typed targets, guarded fixture mutations, generation-bound projections, two narrow extensions, nonblocking Bubble Tea features, atomic cooperating JSON writers, real stdio MCP clients, and a real saved SSH route. The original private validation run passed 35 assertions across 91 events. Machine-specific receipts are excluded from this PR; `application_examples.py` generates fresh results and a log when replayed.

Within that declared fixture-only scope, I found no unresolved POC-blocking defect in the final source. Earlier review findings were corrected: revision 3 rejects the legacy environment-only create, the file repository uses a process-released sidecar `flock`, owner JSON is decoded from stdout without SSH diagnostics, and the TUI nonblocking test no longer uses a wall-clock threshold.

This does not validate the full application refactor or a production owner rollout. The production blockers are legacy writers, real lifecycle parity, historical binary/schema compatibility, and command semantics for effects stronger than fixture metadata. Issue #646 remains sound as a UI maintainability proposal. Its file moves, feature types, root dependency grouping, and smaller handlers are orthogonal to process authority and compatibility.

## Five strongest findings and migration risks

### 1. Production blocker: establish one execution authority and cut over old writers

**Frozen evidence.** Current `sessioncmd` operations construct writable stores and drivers in the caller process (`internal/sessioncmd/terminal.go:63-108`; `internal/sessioncmd/session.go:298-390,483-535,648-830`). The TUI independently performs lifecycle effects (`internal/ui/lifecycle.go:642-679`; `internal/ui/rename.go:233-322`). Its poller combines heartbeat, pruning, mailbox mutation, delivery, status writes, inventory and selected preview work (`internal/ui/poller.go:378-683`), while `runMu` is only in-process. `store.Open` enables WAL and runs migrations even for a nominal reader (`internal/store/store.go:96-111,118-239`). These are existing boundary facts, not regressions introduced by #646 or by the POC.

**Concrete shipping scenario.** A new headless owner holds its cooperating lock while an old TUI or long-lived MCP process opens the same profile, runs migrations, writes a session directly, or starts maintenance. Both processes can be internally correct while the advertised single-owner guarantee is false.

**Minimal remedy.** Define the canonical owner key from the authoritative store plus execution binding such as the tmux socket. Move one existing maintenance path and one real lifecycle use case behind that owner. Upgrade, quiesce, reject, or isolate every incompatible writer before enabling the guarantee. Reuse `sessioncmd` behavior rather than building a second lifecycle implementation.

**Required proof.** Start the supported old TUI/MCP/CLI and new owner against one disposable real profile. Show that exactly one maintenance counter advances, old writers are read-only/rejected/isolated, and the shared command has identical durable, tmux, hook, worktree, label, cleanup, and partial-failure behavior through TUI, CLI, and MCP.

### 2. Production blocker: keep the mixed-version policy small and test the five version domains

**POC evidence.** The final owner policy is sound for the synthetic contract: revision 2 alone advertises legacy create, revision 3 advertises the two instance-guarded mutations, and revision 3 rejects legacy create without writing (`cmd/architecture-poc/protocol.go:145-153,204-223`; `owner_extension_test.go:64-120`). The bridge uses operation-specific DTOs, accepts additive response fields, validates protocol major/request ID/result kind, and classifies a lost mutation response as uncertain (`internal/architecturepoc/adapters/owner_bridge.go:134-257`). Workspace JSON requires exact schema version 1 and refuses an unsupported document before overwrite (`file_repository.go:91-116`; `file_repository_test.go:102-120`).

**Future risk, not a reproduced POC failure.** Controller build, command binary on disk, already-running owner, long-lived MCP server, and SQLite schema can change independently. The POC uses synthetic revisions from one source tree and current schemas. A new command endpoint can therefore encounter a historical owner or schema whose effective behavior is not represented by the POC capability list.

**Minimal remedy.** Support reads only for explicitly compatible wire meanings. Enable a mutation only when the invoked command endpoint and running owner both implement its mandatory guards. A new owner must reject legacy requests that omit them; clients must never downgrade. Treat incompatible workspace or SQLite schemas as a controlled upgrade/quiesce decision. Do not add a general negotiation framework or promise N-1 by convention.

**Required proof.** Use pinned released binaries and schemas for each supported upgrade direction: new controller/old endpoint, new endpoint/old owner, old controller/new owner, old long-lived MCP/new policy, and old writer/new schema. Missing capability or guard must produce a stable denial and zero mutation.

### 3. Accept the workspace/application-service shape, but migrate through existing use cases

**Implemented evidence.** The selected shape separates saved connection/projection state from an `OwnerClient` port (`internal/architecturepoc/application/ports.go:5-18`). Targets distinguish the local device, a connection, and a session; reader tokens carry connection generation, refresh sequence, environment and owner instance (`types.go:47-97`). The application keeps wire, JSON and process DTOs in adapters. Filtered inventory and fixture rename depend on narrow consumer-owned interfaces rather than a registry or command bus (`internal/architecturepoc/extensions/filter.go:10-43`; `rename.go:9-29`).

**Migration risk.** If this fixture model becomes a parallel implementation of create, kill, revive, archive, enqueue, wait, hook cleanup, worktree cleanup and label handling, the TUI and `sessioncmd` will drift behind similarly named interfaces. The POC deliberately does not prove those production behaviors. Its `Access` route descriptor also contains SSH host/binary data; that is tolerable for the saved-route experiment, but credentials, SSH policy and protocol branches must stay in adapters rather than expand through application use cases.

**Minimal remedy.** Keep two meaningful domain boundaries: controller workspace coordination and execution-profile authority. Extend or extract the existing `sessioncmd` seam one use case at a time, then route TUI/CLI/MCP through it. Keep presentation reconciliation and connection projections outside the authoritative lifecycle operation.

**Required proof.** Run one real use case through deterministic fake adapters and the concrete store/tmux/hook adapters, then invoke the same use case from all three fronts. Compare durable state and driver effects, including rollback and partial failure. This is the next reviewable unit; a 43,560-line rewrite is neither required nor supported by this POC.

### 4. Carry the POC's explicit async and mutation semantics into stronger commands

**Proven behavior.** Refresh dispatch increments a per-connection sequence and accepts a result only if connection generation and sequence still match (`internal/architecturepoc/application/workspace.go:169-239`). The Bubble Tea features keep independent request generations and discard invalidated replies (`internal/architecturepoc/adapters/tui.go:37-65,84-114,149-162`). Mutation resolution checks the current projection, then releases the repository lock before the owner RPC (`application/client.go:64-100`). The returned `MutationResult` retains its dispatch target and token (`client.go:19-61`). The deterministic test proves that retargeting does not reconcile the late reply into the new projection (`workspace_test.go:381-428`).

**Defined limitation.** A retarget cannot revoke a command already accepted by the old owner. The old fixture can still be renamed; presentation only refuses to apply that reply to the new target. An offline refresh also preserves the cached token, and the fixture-only policy deliberately allows an identity-guarded metadata attempt (`workspace_test.go:430-458`; `DESIGN_SKETCHES.md:43-49`). This is documented behavior, not a claim of cross-system atomic revocation.

**Concrete shipping scenario.** A user retargets while kill, raw input, or launch is already in flight. The UI discards the late result, but the old endpoint has performed the effect. A lost response then tempts an automatic retry against a different endpoint.

**Minimal remedy.** State the linearization point as dispatch precheck for operations that can tolerate it. Require fresh projections where destructive policy needs freshness. Before launch, queue delivery, kill, or pane input, add operation-specific durable receipts/idempotency and owner-side fencing; do not infer those properties from a reader token or connection generation.

**Required proof.** Deterministically race dispatch with retarget/removal/reconnect and lose the response after each owner-side commit point. Verify the documented effect, stable uncertain outcome, no automatic replay, and stale UI rejection. Pane input additionally needs two-client takeover and stale writer/resize rejection.

### 5. Adapter and client-scope evidence is strong, with a bounded external-actor claim

**Implemented evidence.** Scope is fixed when constructing `DeviceClient` or `WorkspaceClient`, not selected by an MCP tool argument (`application/workspace.go:24-30`; `adapters/mcp.go:44-94`). A device client rejects a forged connection target before calling the remote owner (`workspace_test.go:475-496`). The repository holds a private sidecar `flock` across reload, mutation callback and atomic replacement (`adapters/file_repository.go:29-89,119-148`). The final harness proves two concurrent CLI writers preserve both connections and distinct generations, a long-lived workspace MCP reloads a CLI save, the simultaneous device MCP remains local, and a forged remote rename returns `scope_violation`. It also performs connect, refresh, inventory, guarded rename and verification through the saved real SSH/Linux route (the original private validation run; replay with `application_examples.py`).

**Bounded claim.** This proves cooperating application clients and prevents an ordinary MCP caller from changing scope through tool arguments. It is not authentication against another process with the same OS account, which can launch a workspace-scoped process or bypass the application entirely. No general authentication framework is warranted unless the deployment threat model adds a distinct principal boundary.

**Representative-only limits.** The TUI proof uses two small feature types and a shared application client; it does not cover focus, review, mouse capture, geometry, preview hold, drafts, or preferences. The SSH proof uses the disposable current owner binary. The JSON lock coordinates participating application processes only and makes no power-loss durability or legacy-writer claim.

**Required production proof.** Derive device/workspace scope from trusted process composition, keep scope absent from tool arguments, and run existing long-lived MCP clients through the upgrade path. Preserve the current real-SSH scenario, then add pinned historical endpoint/owner binaries only for combinations the project intends to support.

## Final recommendation

Ship this artifact as a representative architecture proof and use it to constrain the next increment. First publish the owner/compatibility contract and old-writer cutover rule. Then move one real `sessioncmd` lifecycle operation and one maintenance path behind the owner, with parity and mixed-version evidence. Continue #646's presentation decomposition alongside those increments; do not use structural UI acceptance criteria as evidence that execution ownership, scope, or compatibility has been solved.
