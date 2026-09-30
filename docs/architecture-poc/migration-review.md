# Migration review and validation

Independent source review found no blocking defect in the canonical extraction, explicit caller composition, guarded process adapter, or test-process cleanup. Fresh comment review found no new Go comments requiring deletion.

The archive body retains the original caller/agent/self checks, best-effort capture, snapshot write ordering, archive flag write and result construction. The owner borrows its store/driver. Existing defaults still open and close their own runtime. An injected owner returns before local config/store opening, including on error.

Retention still runs every 300 poll ticks, deletes by delivery time and preserves pending/recently delivered messages. Its 24-hour policy and clock now live in `InboxOwner`. `ClaimPoller` remains with actual delivery. Maintenance failure aborts the rest of that poll and leaves its tick unchanged; the heartbeat may already be stamped. Continuous delivery during owner outage is not claimed.

The process test passes actual CLI archive, actual stdio MCP restore, real production TUI rendering and remote pruning, environment/instance refusal, long-lived MCP replacement refusal, and production-socket rejection. It uses disposable `cat` panes rather than launching supported provider CLIs. Owner logs confirm the two archive effects and maintenance request executed in that process. Focused race tests also pass old-owner capability rejection and uncertain outcomes without retry or local fallback.

Formatting, vet, build and secret scans pass. The full race run passes every package except UI. Three UI failures had already reproduced on the untouched baseline: `TestFocusWatchHonorsHiddenCursor`, `TestRestartLaunchesAFreshConversation`, and `TestRestartEndsALiveAgentFirst`. This full run also failed `TestFormLastWorktreeYieldsToGroupDefault` and `TestTerminalKeyIgnoresAutorepeat`; both passed targeted race reruns on the migration branch and untouched baseline. The full suite is not green, and those additional intermittent failures are not claimed to have a diagnosed cause.

This is a real two-operation code migration, not exclusive production ownership. Other commands, UI lifecycle, status/delivery writers, historical binaries, automatic bootstrap, every-provider/platform coverage and durable receipts still require separate migration and rollout evidence.
