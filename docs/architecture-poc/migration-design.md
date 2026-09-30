# First production-code migration

## Problem

The first POC demonstrated new boundaries alongside the application. This increment must remove orchestration from real callers without changing existing behavior. CLI and MCP already share `sessioncmd.Sessions.Archive`; that operation leaves a live pane running and restores only the archive flag. TUI archive deliberately snapshots and kills a selection, while restore can revive it. They are different use cases. The existing sessioncmd extraction in upstream #310 intentionally consolidated CLI/MCP behavior; the socket ownership work in #412 prevents a manager on the wrong server from stamping sessions dead. Both constraints are preserved. Delivered-inbox retention is a separate bounded maintenance operation; poller heartbeats cannot move to an owner that does not deliver messages.

## Usage

```go
commands := cli.CommandsWithArchiveOwner(version, archiveOwner)
server := mcpserver.NewServerWithArchiveOwner(profile, callerID, version, archiveOwner)
model := ui.NewWithInboxOwner(cfg, state, driver, engine, hooks, version, inboxOwner)
```

An explicitly supplied owner is authoritative for these two routed operations. Its failure returns to the caller without opening a local write path. Existing default compositions retain local behavior during this experiment.

## Shape

`ArchiveOwner.Archive(ArchiveRequest) (Session, error)` owns caller/target validation, the self-archive rule, snapshot capture and persistence, archive state and result construction. `Sessions.Archive` becomes a facade selecting its already-bound owner, or opening the existing local runtime and using the canonical borrowed `SessionOwner`. The request carries the existing caller vocabulary so error guidance stays compatible; the transport maps only known CLI/MCP frontends.

`InboxOwner.MaintainInbox() error` owns the 24-hour delivered-message retention policy and its clock. The poller retains its refresh cadence and actual delivery heartbeat. The process owner composes the two canonical handlers with its store and explicitly named tmux driver; serialization, profile lock, capabilities and observed identity guards protect routed operations.

These narrow interfaces hide the lifecycle and retention decisions, rather than exposing store mutation stages. The adapters alone know protocol DTOs. No generic command bus, second archive implementation, or production bootstrap is introduced.

## Synthesis decision

Choose the explicit composition design over automatically bootstrapping a child daemon for all existing entrypoints. A daemon default would require lifecycle supervision, version replacement and writer cutover beyond this slice. The design comparison also retains the existing separation between UI archive and agent-command archive, and leaves `ClaimPoller` with the delivery loop. Provider diversity is reduced: the native design lane compared both alternatives and a separate source-level reviewer accepted the chosen boundary; external Fable, Opus and Grok lanes were unavailable in the preceding architecture run and were not substituted.

## Tradeoffs accepted

- Accept explicit experiment composition in exchange for avoiding an unproven production cutover.
- Accept a local owner per current default caller in exchange for preserving current startup and test behavior. This is not exclusive process ownership.
- Accept migration of two effects in exchange for a small reviewable example; every other writer remains outside the ownership guarantee.

## Alternatives considered

An automatically spawned production child owner would hide startup behind the archive client, but also introduce bootstrap races, restart/version policy and old-client coexistence. It is a deeper operational boundary than we can validate by migrating two operations.

Moving TUI archive onto agent-command archive would reduce code at the expense of its kill, nested-session, group and revival behavior. It is rejected as a behavior regression.

## Open questions and risks

Which historical writers must be upgraded, quiesced, rejected or isolated before a production owner becomes exclusive? Which real lifecycle and maintenance operation should move next after this slice? These questions do not prevent explicitly routed POC clients from being tested. Maintenance errors abort that poll before status/delivery and leave its tick unchanged, so later polls remain gated on the bound owner. Its delivery heartbeat may already have been stamped. Uninterrupted delivery during owner outage is not demonstrated; restarted owners require an explicitly rebound client, not automatic mutation retargeting.

## Next implementation step

Extract the existing archive body and retention policy into canonical handlers, pin their behavior with tests, then compose the production frontends against guarded owner RPC.
