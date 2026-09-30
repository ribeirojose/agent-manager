# Owner protocol sketches

This POC tests the owner and compatibility slice needed by the proposed connection feature. The domain boundaries can guide the whole product, but this artifact does not validate the full application or every item in issue 646.

## Decision

Build sketch A. One owner process holds the cooperating POC owner lock, owns the SQLite handle, and serves typed requests on a Unix socket. The `rpc` command is a transport bridge. A local subprocess and an SSH command can both invoke that bridge without changing the owner protocol.

## Shared wire shape

Each request has one protocol major, one request ID, one operation, and operation-specific arguments.

```json
{
  "protocol_major": 1,
  "request_id": "demo-create",
  "operation": "fixture_session.create",
  "arguments": {
    "expected_environment_id": "env-...",
    "session": {
      "id": "fixture-1",
      "name": "fixture-session",
      "tool": "codex",
      "cwd": "/tmp/profile",
      "group": "architecture-poc"
    }
  }
}
```

The owner creates every response envelope. A successful response contains `result`. A rejected response contains a stable error `code` and a message. Every response also contains owner metadata.

```json
{
  "protocol_major": 1,
  "request_id": "demo-create",
  "ok": false,
  "owner": {
    "instance_id": "owner-...",
    "environment_id": "env-...",
    "revision": 1,
    "capabilities": ["owner.describe", "sessions.read"]
  },
  "error": {
    "code": "missing_capability",
    "message": "owner revision 1 does not support fixture_session.create"
  }
}
```

Protocol major 1 accepts additive response fields. It rejects a different major. Revision 1 exposes only `owner.describe` and `sessions.list`. Revision 2 also exposes `fixture_session.create`. The create command requires the owner's exact environment ID. The command adds an inventory-only fixture through `store.CreateSession`. It does not launch an agent or touch tmux.

Revision 1 has no inventory mutation RPC. Owner startup can still run SQLite migrations, enable WAL, and persist a missing environment ID through `store.Open` and `store.SetSetting`.

## Sketch A: socket owner with a thin bridge

Usage:

```text
architecture-poc serve --dir PROFILE --revision 1|2
printf '%s\n' REQUEST | architecture-poc rpc --dir PROFILE
architecture-poc demo local
```

`serve` takes a nonblocking profile lock before it opens SQLite. It listens on a short socket path under the system temporary directory. A hash of the canonical profile path keeps sockets distinct and avoids the Unix path limit. The owner removes only its derived socket after it has acquired the lock.

The owner parses the external envelope into one of three command types. It validates protocol and operation arguments at that boundary. The dispatcher then receives typed values. Read handlers call only store read methods. The create handler checks the effective capability and environment ID before it calls `store.CreateSession`.

This shape has one public process contract and one authoritative writer among cooperating POC owners. It hides locking, capability policy, environment checks, SQLite access, and response metadata behind the three operations. Adding SSH requires one command runner that invokes `rpc`; it does not require another protocol implementation.

## Sketch B: atomic file mailbox

`serve` holds the same profile lock and polls `requests/`. The bridge writes one atomic JSON request file and waits for a matching response file. The owner validates, dispatches, and writes the response atomically. SSH copies the request and response files or invokes a small mailbox bridge.

This shape avoids a socket. It introduces request expiry, orphan cleanup, duplicate request handling, polling delay, file permissions, and uncertain outcomes when the response exists but the caller disconnects. Those states become part of the protocol and tests. The caller also needs more than one action to complete an operation.

## Why sketch A wins

Sketch A keeps one request and one response on a live connection. The profile lock proves exclusion among cooperating POC owners. The socket lifetime reports readiness directly. The bridge remains small enough for a subprocess or SSH adapter. Sketch B exposes persistence and retry rules that this proof does not need.

We accept that a socket request has no durable receipt in exchange for a smaller experiment. A production mutation protocol would need request idempotency and transport-outcome rules before it controlled real sessions.

The environment ID identifies the SQLite profile lineage. Copying the profile copies that ID, so it does not prove a host or deployment identity. The flock does not constrain the current TUI, MCP process, direct SQLite clients, or arbitrary shell access because those writers do not participate in this POC lock.
