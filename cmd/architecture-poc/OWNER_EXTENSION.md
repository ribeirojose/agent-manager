# Revision 3 owner extension

## Scope

Revision 3 extends the disposable owner with two metadata commands for the application-wide architecture slice. Both commands require the current environment ID and owner instance ID. They affect inventory-only fixtures. They do not launch, stop, rename, or send input to a real agent or tmux session.

## Two compatible shapes

### A. Add distinct guarded operations

Keep `fixture_session.create` unchanged in revision 2. Add `fixture_session.create_bound` and `fixture_session.rename` in revision 3. Each new argument type requires both guards. Revision 1 and revision 2 reject both new operations with `missing_capability`. Revision 3 rejects the legacy create operation with the same stable error and writes nothing.

This shape makes the workspace choose the guarded contract by operation name. A missing instance guard fails argument parsing. The owner checks both guards again when it executes the command. The workspace never retries through `fixture_session.create`.

### B. Overload the legacy create operation

Add an optional `expected_owner_instance_id` field to `fixture_session.create`. Revision 3 could require the field while revision 2 continued to accept the old form.

This shape puts two safety contracts behind one operation name. A caller must infer semantics from effective owner capabilities before constructing the same command. An adapter can also omit the new field or retry the old form after rejection. The old operation remains useful only as evidence of the lineage-only limitation, so overloading it hides the distinction that the POC needs to test.

## Decision

Use distinct operations. The operation name selects the complete guard contract, and revision 2 request meanings stay unchanged. Revision 3 preserves protocol-major-1 reads but removes the unsafe legacy mutation capability. A legacy create request receives `missing_capability` instead of mutating with a lineage-only guard. New workspace code must require `fixture_session.create_bound` and must not downgrade.

## Wire additions

Bound create uses the existing fixture fields:

```json
{
  "protocol_major": 1,
  "request_id": "bound-create-1",
  "operation": "fixture_session.create_bound",
  "arguments": {
    "expected_environment_id": "env-...",
    "expected_owner_instance_id": "owner-...",
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

Rename identifies one fixture and supplies its new display name:

```json
{
  "protocol_major": 1,
  "request_id": "rename-1",
  "operation": "fixture_session.rename",
  "arguments": {
    "expected_environment_id": "env-...",
    "expected_owner_instance_id": "owner-...",
    "session_id": "fixture-1",
    "name": "renamed-fixture"
  }
}
```

Both commands check the capability, parse the typed arguments, compare the environment ID, and compare the live owner instance ID before mutation. Rename then loads the session and requires `status=fixture` with no tmux socket. A stale guard fails after an owner restart even though the persisted environment ID stays the same.

This extension adds no durable receipt, automatic retry, input fencing, host identity, or protection from nonparticipating database writers.
