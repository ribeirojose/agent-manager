#!/usr/bin/env python3
"""Run disposable application-wide examples against real POC binaries.

All child output is captured in application-examples.log. This program writes
one JSON document to stdout and never retries a mutation after dispatch.
"""

from __future__ import annotations

import argparse
import json
import os
import select
import shlex
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent
SSH_OPTIONS = [
    "-o", "ControlPath=none",
    "-o", "BatchMode=yes",
    "-o", "ForwardAgent=no",
    "-o", "ClearAllForwardings=yes",
    "-o", "ConnectTimeout=5",
]


class ProofFailure(RuntimeError):
    pass


class Evidence:
    def __init__(self, log_path: Path) -> None:
        self.log_path = log_path
        self.events: list[dict[str, Any]] = []
        log_path.parent.mkdir(parents=True, exist_ok=True)
        log_path.write_text("")

    def record(self, kind: str, name: str, **fields: Any) -> None:
        event = {"sequence": len(self.events) + 1, "kind": kind, "name": name, **fields}
        self.events.append(event)
        with self.log_path.open("a") as stream:
            stream.write(json.dumps(event, sort_keys=True) + "\n")

    def require(self, name: str, condition: bool, **details: Any) -> None:
        self.record("assertion", name, passed=bool(condition), **details)
        if not condition:
            raise ProofFailure(name)

    def run(
        self,
        name: str,
        argv: list[str],
        *,
        input_text: str | None = None,
        timeout: float = 20,
        expected_exit: int = 0,
    ) -> subprocess.CompletedProcess[str]:
        completed = subprocess.run(
            argv,
            input=input_text,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        self.record(
            "process",
            name,
            argv=argv,
            returncode=completed.returncode,
            stdin=input_text,
            stdout=completed.stdout,
            stderr=completed.stderr,
        )
        if completed.returncode != expected_exit:
            raise ProofFailure(
                f"{name}: exit {completed.returncode}, expected {expected_exit}: "
                f"{completed.stderr.strip()}"
            )
        return completed

    def run_json(
        self,
        name: str,
        argv: list[str],
        *,
        input_document: dict[str, Any] | None = None,
        timeout: float = 20,
    ) -> dict[str, Any]:
        input_text = None if input_document is None else json.dumps(input_document) + "\n"
        completed = self.run(name, argv, input_text=input_text, timeout=timeout)
        document = exact_json(completed.stdout, name)
        if not isinstance(document, dict):
            raise ProofFailure(f"{name}: stdout JSON is not an object")
        return document


def exact_json(text: str, name: str) -> Any:
    decoder = json.JSONDecoder()
    stripped = text.lstrip()
    try:
        document, offset = decoder.raw_decode(stripped)
    except json.JSONDecodeError as error:
        raise ProofFailure(f"{name}: stdout is not JSON: {error}") from error
    if stripped[offset:].strip():
        raise ProofFailure(f"{name}: stdout contains more than one JSON value")
    return document


class OwnerProcess:
    def __init__(self, evidence: Evidence, binary: Path, profile: Path, label: str) -> None:
        self.evidence = evidence
        self.binary = binary
        self.profile = profile
        self.label = label
        self.counter = 0
        self.process: subprocess.Popen[str] | None = None
        self.describe: dict[str, Any] | None = None

    def start(self) -> None:
        self.profile.mkdir(mode=0o700, parents=True, exist_ok=True)
        argv = [str(self.binary), "serve", "--dir", str(self.profile), "--revision", "3"]
        self.process = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.evidence.record("lifecycle", f"{self.label}.start", argv=argv, pid=self.process.pid)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                _, stderr = self.process.communicate()
                raise ProofFailure(f"{self.label}: owner exited during startup: {stderr.strip()}")
            try:
                response = self.rpc("owner.describe", {}, readiness=True)
                if response.get("ok"):
                    self.describe = response
                    return
            except (ProofFailure, subprocess.SubprocessError):
                pass
            time.sleep(0.05)
        raise ProofFailure(f"{self.label}: owner readiness timed out")

    def rpc(self, operation: str, arguments: dict[str, Any], *, readiness: bool = False) -> dict[str, Any]:
        self.counter += 1
        request = {
            "protocol_major": 1,
            "request_id": f"application-example-{self.label}-{self.counter}",
            "operation": operation,
            "arguments": arguments,
        }
        argv = [str(self.binary), "rpc", "--dir", str(self.profile)]
        if readiness:
            completed = subprocess.run(
                argv,
                input=json.dumps(request) + "\n",
                capture_output=True,
                text=True,
                timeout=2,
            )
            if completed.returncode != 0 or not completed.stdout.strip():
                raise ProofFailure(f"{self.label}: owner is not ready")
            response = exact_json(completed.stdout, f"{self.label}.readiness")
            if not isinstance(response, dict):
                raise ProofFailure(f"{self.label}: readiness response is not an object")
            self.evidence.record("owner_rpc", f"{self.label}.ready", request=request, response=response)
            return response
        return self.evidence.run_json(f"{self.label}.{operation}", argv, input_document=request)

    def seed(self, session_id: str, name: str, group: str) -> dict[str, Any]:
        if self.describe is None:
            raise ProofFailure(f"{self.label}: owner was not described")
        owner = self.describe["owner"]
        response = self.rpc(
            "fixture_session.create_bound",
            {
                "expected_environment_id": owner["environment_id"],
                "expected_owner_instance_id": owner["instance_id"],
                "session": {
                    "id": session_id,
                    "name": name,
                    "tool": "codex",
                    "cwd": str(self.profile / "fixture-worktree"),
                    "group": group,
                },
            },
        )
        self.evidence.require(f"{self.label}.bound_seed_succeeded", response.get("ok") is True)
        return response

    def stop(self) -> None:
        if self.process is None:
            return
        process, self.process = self.process, None
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
        try:
            stdout, stderr = process.communicate(timeout=5)
        except subprocess.TimeoutExpired as error:
            process.kill()
            stdout, stderr = process.communicate()
            self.evidence.record(
                "lifecycle", f"{self.label}.stop", returncode=process.returncode,
                stdout=stdout, stderr=stderr, graceful=False,
            )
            raise ProofFailure(f"{self.label}: graceful owner shutdown timed out") from error
        self.evidence.record(
            "lifecycle", f"{self.label}.stop", returncode=process.returncode,
            stdout=stdout, stderr=stderr, graceful=True,
        )


class WorkspaceCLI:
    def __init__(self, evidence: Evidence, workspace_binary: Path, owner_binary: Path) -> None:
        self.evidence = evidence
        self.workspace_binary = workspace_binary
        self.owner_binary = owner_binary

    def call(self, name: str, command: str, *arguments: str) -> dict[str, Any]:
        argv = [str(self.workspace_binary), command, *arguments]
        return self.evidence.run_json(name, argv)

    def inspect(self, name: str, config: Path, local_profile: Path, scope: str, group: str = "") -> dict[str, Any]:
        arguments = [
            "--config", str(config), "--owner-binary", str(self.owner_binary),
            "--local-profile", str(local_profile), "--scope", scope,
        ]
        if group:
            arguments += ["--group", group]
        return self.call(name, "inspect", *arguments)

    def connect_local(self, name: str, config: Path, connection_id: str, label: str, profile: Path) -> dict[str, Any]:
        return self.call(
            name, "connect", "--config", str(config), "--owner-binary", str(self.owner_binary),
            "--id", connection_id, "--label", label, "--profile", str(profile),
        )

    def refresh(self, name: str, config: Path, connection_id: str) -> dict[str, Any]:
        return self.call(
            name, "refresh", "--config", str(config), "--owner-binary", str(self.owner_binary),
            "--id", connection_id,
        )

    def rename_connection(self, name: str, config: Path, connection_id: str, label: str) -> dict[str, Any]:
        return self.call(
            name, "rename-connection", "--config", str(config), "--id", connection_id,
            "--label", label,
        )

    def rename_fixture(
        self,
        name: str,
        config: Path,
        local_profile: Path,
        scope: str,
        target_id: str,
        token: str,
        new_name: str,
    ) -> dict[str, Any]:
        return self.call(
            name, "fixture-rename", "--config", str(config),
            "--owner-binary", str(self.owner_binary), "--local-profile", str(local_profile),
            "--scope", scope, "--target", target_id, "--token", token, "--name", new_name,
        )


class MCPClient:
    def __init__(
        self,
        evidence: Evidence,
        workspace_binary: Path,
        owner_binary: Path,
        config: Path,
        local_profile: Path,
        scope: str,
    ) -> None:
        self.evidence = evidence
        self.label = f"mcp.{scope}"
        self.counter = 0
        self.argv = [
            str(workspace_binary), "mcp", "--config", str(config),
            "--owner-binary", str(owner_binary), "--local-profile", str(local_profile),
            "--scope", scope,
        ]
        self.process = subprocess.Popen(
            self.argv,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )
        evidence.record("lifecycle", f"{self.label}.start", argv=self.argv, pid=self.process.pid)

    def request(self, method: str, params: dict[str, Any]) -> dict[str, Any]:
        self.counter += 1
        request_id = self.counter
        request = {"jsonrpc": "2.0", "id": request_id, "method": method, "params": params}
        self._write(request)
        response = self._read(method)
        self.evidence.require(f"{self.label}.{method}.matching_id", response.get("id") == request_id, response=response)
        return response

    def notify(self, method: str, params: dict[str, Any]) -> None:
        document = {"jsonrpc": "2.0", "method": method, "params": params}
        self._write(document)
        self.evidence.record("mcp", f"{self.label}.{method}", direction="notification", request=document)

    def _write(self, document: dict[str, Any]) -> None:
        if self.process.stdin is None or self.process.poll() is not None:
            raise ProofFailure("MCP process is not writable")
        self.process.stdin.write(json.dumps(document, separators=(",", ":")) + "\n")
        self.process.stdin.flush()

    def _read(self, method: str) -> dict[str, Any]:
        if self.process.stdout is None:
            raise ProofFailure("MCP process has no stdout")
        ready, _, _ = select.select([self.process.stdout], [], [], 10)
        if not ready:
            raise ProofFailure(f"MCP {method}: response timed out")
        line = self.process.stdout.readline()
        if not line:
            raise ProofFailure(f"MCP {method}: process closed stdout")
        response = exact_json(line, f"MCP {method}")
        if not isinstance(response, dict):
            raise ProofFailure(f"MCP {method}: response is not an object")
        self.evidence.record("mcp", f"{self.label}.{method}", direction="request_response", response=response)
        return response

    def close(self) -> None:
        process = self.process
        if process.stdin is not None:
            process.stdin.close()
            process.stdin = None
        try:
            stdout, stderr = process.communicate(timeout=5)
        except subprocess.TimeoutExpired as error:
            process.send_signal(signal.SIGTERM)
            stdout, stderr = process.communicate(timeout=5)
            self.evidence.record(
                "lifecycle", f"{self.label}.stop", returncode=process.returncode,
                trailing_stdout=stdout, stderr=stderr, graceful=False,
            )
            raise ProofFailure("MCP did not stop on EOF") from error
        self.evidence.record(
            "lifecycle", f"{self.label}.stop", returncode=process.returncode,
            trailing_stdout=stdout, stderr=stderr, graceful=True,
        )
        if process.returncode != 0:
            raise ProofFailure(f"MCP exited {process.returncode}: {stderr.strip()}")


def target(document: dict[str, Any], target_id: str) -> dict[str, Any]:
    for current in document.get("targets", []):
        if current.get("target_id") == target_id:
            return current
    raise ProofFailure(f"inventory omitted target {target_id!r}")


def session_row(target_document: dict[str, Any], session_id: str) -> dict[str, Any]:
    for row in target_document.get("sessions", []):
        if row.get("session", {}).get("id") == session_id:
            return row
    raise ProofFailure(f"target {target_document.get('target_id')!r} omitted session {session_id!r}")


def tool_payload(response: dict[str, Any]) -> dict[str, Any]:
    result = response.get("result")
    if not isinstance(result, dict):
        raise ProofFailure(f"MCP tool response has no result object: {response}")
    structured = result.get("structuredContent")
    if isinstance(structured, dict):
        return structured
    for content in result.get("content", []):
        if isinstance(content, dict) and content.get("type") == "text":
            text = str(content.get("text", ""))
            try:
                parsed = exact_json(text, "MCP tool text")
            except ProofFailure:
                return {"message": text}
            if isinstance(parsed, dict):
                return parsed
    return result


def domain_error_code(response: dict[str, Any]) -> str:
    error = response.get("error")
    if isinstance(error, dict):
        data = error.get("data")
        if isinstance(data, dict) and isinstance(data.get("code"), str):
            return data["code"]
        if isinstance(error.get("code"), str):
            return error["code"]
    result = response.get("result")
    if isinstance(result, dict) and result.get("isError"):
        payload = tool_payload(response)
        payload_error = payload.get("error")
        if isinstance(payload_error, dict) and isinstance(payload_error.get("code"), str):
            return payload_error["code"]
    return ""


def tool_session(payload: dict[str, Any]) -> dict[str, Any]:
    session = payload.get("session")
    if isinstance(session, dict):
        return session
    mutation = payload.get("mutation")
    if isinstance(mutation, dict) and isinstance(mutation.get("session"), dict):
        return mutation["session"]
    raise ProofFailure(f"MCP rename output omitted session: {payload}")


def cli_examples(
    evidence: Evidence,
    cli: WorkspaceCLI,
    root: Path,
    local_profile: Path,
    standin_profile: Path,
) -> dict[str, Any]:
    config = root / "workspace-cli.json"
    connection_id = "standin"
    cli.connect_local("cli.connect_standin", config, connection_id, "Stand-in before", standin_profile)
    cli.refresh("cli.refresh_standin", config, connection_id)
    before = cli.inspect("cli.inspect_before_label", config, local_profile, "workspace")
    device = target(before, "device")
    connection = target(before, f"connection/{connection_id}")
    local_row = session_row(device, "shared-session")
    remote_row = session_row(connection, "shared-session")
    evidence.require(
        "cli.colliding_ids_are_namespaced",
        local_row["target_id"] != remote_row["target_id"] and local_row["token"] != remote_row["token"],
        local_target=local_row["target_id"], remote_target=remote_row["target_id"],
    )

    cli.rename_connection("cli.rename_connection", config, connection_id, "Stand-in renamed")
    renamed = cli.inspect("cli.inspect_after_label", config, local_profile, "workspace")
    renamed_target = target(renamed, f"connection/{connection_id}")
    evidence.require(
        "cli.connection_label_persisted",
        renamed_target.get("label") == "Stand-in renamed",
        observed_label=renamed_target.get("label"),
    )
    disk_document = exact_json(config.read_text(), "saved workspace config")
    disk_labels = {item["id"]: item["label"] for item in disk_document.get("connections", [])}
    evidence.require(
        "cli.connection_label_persisted_on_disk",
        disk_labels.get(connection_id) == "Stand-in renamed",
        disk_labels=disk_labels,
    )

    grouped = cli.inspect("cli.inspect_extension_group", config, local_profile, "workspace", "extension")
    grouped_rows = [row for item in grouped["targets"] for row in item.get("sessions", [])]
    evidence.require(
        "cli.extension_group_query",
        len(grouped_rows) == 2 and all(row["session"]["group"] == "extension" for row in grouped_rows),
        targets=[row["target_id"] for row in grouped_rows],
    )
    selected = session_row(target(grouped, f"connection/{connection_id}"), "shared-session")
    renamed_session = cli.rename_fixture(
        "cli.rename_fixture", config, local_profile, "workspace",
        selected["target_id"], selected["token"], "renamed-through-cli",
    )
    cli.refresh("cli.refresh_after_fixture_rename", config, connection_id)
    after = cli.inspect("cli.inspect_after_fixture_rename", config, local_profile, "workspace", "extension")
    persisted = session_row(target(after, f"connection/{connection_id}"), "shared-session")
    evidence.require(
        "cli.fixture_rename_persisted",
        persisted["session"]["name"] == "renamed-through-cli",
        session=persisted["session"],
    )
    return {
        "classification": "real application API and processes; second local profile simulates outbound placement",
        "connection_id": connection_id,
        "colliding_target_ids": [local_row["target_id"], remote_row["target_id"]],
        "persisted_label": renamed_target["label"],
        "group_row_count": len(grouped_rows),
        "rename_result": renamed_session,
        "persisted_session": persisted["session"],
    }


def concurrent_repository_example(
    evidence: Evidence,
    cli: WorkspaceCLI,
    root: Path,
    local_profile: Path,
    standin_profile: Path,
) -> dict[str, Any]:
    config = root / "workspace-concurrent.json"
    commands: list[tuple[str, list[str]]] = []
    for connection_id in ("parallel-a", "parallel-b"):
        commands.append((connection_id, [
            str(cli.workspace_binary), "connect", "--config", str(config),
            "--owner-binary", str(cli.owner_binary), "--id", connection_id,
            "--label", connection_id, "--profile", str(standin_profile),
        ]))
    processes = [
        (connection_id, argv, subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True))
        for connection_id, argv in commands
    ]
    outputs: dict[str, dict[str, Any]] = {}
    for connection_id, argv, process in processes:
        try:
            stdout, stderr = process.communicate(timeout=20)
        except subprocess.TimeoutExpired as error:
            process.kill()
            stdout, stderr = process.communicate()
            evidence.record(
                "process", f"repository.{connection_id}", argv=argv, returncode=process.returncode,
                stdout=stdout, stderr=stderr, concurrent=True,
            )
            raise ProofFailure(f"concurrent connect {connection_id} timed out") from error
        evidence.record(
            "process", f"repository.{connection_id}", argv=argv, returncode=process.returncode,
            stdout=stdout, stderr=stderr, concurrent=True,
        )
        if process.returncode != 0:
            raise ProofFailure(f"concurrent connect {connection_id}: {stderr.strip()}")
        document = exact_json(stdout, f"concurrent connect {connection_id}")
        if not isinstance(document, dict):
            raise ProofFailure(f"concurrent connect {connection_id}: output is not an object")
        outputs[connection_id] = document

    saved = exact_json(config.read_text(), "concurrent workspace config")
    connections = saved.get("connections", [])
    ids = [connection["id"] for connection in connections]
    generations = [connection["generation"] for connection in connections]
    evidence.require(
        "repository.concurrent_cli_saves_keep_both_connections",
        ids == ["parallel-a", "parallel-b"],
        connection_ids=ids,
    )
    evidence.require(
        "repository.concurrent_cli_saves_allocate_distinct_generations",
        len(set(generations)) == 2 and all(generation > 0 for generation in generations),
        generations=generations,
    )
    inspected = cli.inspect("repository.inspect_after_concurrent_saves", config, local_profile, "workspace")
    inspected_ids = [item["target_id"] for item in inspected["targets"]]
    evidence.require(
        "repository.concurrent_saves_reload_through_application",
        inspected_ids == ["device", "connection/parallel-a", "connection/parallel-b"],
        targets=inspected_ids,
    )
    return {
        "classification": "two real CLI processes saving one atomically locked file repository",
        "connection_ids": ids,
        "generations": generations,
        "cli_outputs": outputs,
        "reloaded_target_ids": inspected_ids,
    }


def mcp_examples(
    evidence: Evidence,
    cli: WorkspaceCLI,
    workspace_binary: Path,
    owner_binary: Path,
    root: Path,
    local_profile: Path,
    standin_profile: Path,
) -> dict[str, Any]:
    config = root / "workspace-mcp.json"
    device_client = MCPClient(evidence, workspace_binary, owner_binary, config, local_profile, "device")
    workspace_client = MCPClient(evidence, workspace_binary, owner_binary, config, local_profile, "workspace")
    try:
        initialize_params = {
            "protocolVersion": "2025-06-18",
            "capabilities": {},
            "clientInfo": {"name": "application-examples", "version": "1"},
        }
        initialized = device_client.request("initialize", initialize_params)
        evidence.require("mcp.initialize_succeeded", "result" in initialized and "error" not in initialized)
        device_client.notify("notifications/initialized", {})
        workspace_initialized = workspace_client.request("initialize", initialize_params)
        evidence.require(
            "mcp.workspace_initialize_succeeded",
            "result" in workspace_initialized and "error" not in workspace_initialized,
        )
        workspace_client.notify("notifications/initialized", {})
        listed = device_client.request("tools/list", {})
        tools = listed.get("result", {}).get("tools", [])
        tool_names = {item.get("name") for item in tools}
        evidence.require(
            "mcp.tools_list_exposes_examples",
            {"inventory", "rename_fixture"}.issubset(tool_names),
            tools=sorted(name for name in tool_names if isinstance(name, str)),
        )

        first_call = device_client.request(
            "tools/call", {"name": "inventory", "arguments": {"group": "extension"}},
        )
        first_inventory = tool_payload(first_call)["inventory"]
        evidence.require(
            "mcp.device_inventory_initially_local",
            [item["target_id"] for item in first_inventory["targets"]] == ["device"],
            targets=[item["target_id"] for item in first_inventory["targets"]],
        )
        workspace_first_call = workspace_client.request(
            "tools/call", {"name": "inventory", "arguments": {"group": "extension"}},
        )
        workspace_first_inventory = tool_payload(workspace_first_call)["inventory"]
        evidence.require(
            "mcp.workspace_inventory_before_connection_is_local",
            [item["target_id"] for item in workspace_first_inventory["targets"]] == ["device"],
            targets=[item["target_id"] for item in workspace_first_inventory["targets"]],
        )

        cli.connect_local("mcp.concurrent_connect", config, "later", "Added after MCP start", standin_profile)
        cli.refresh("mcp.concurrent_refresh", config, "later")
        workspace_inventory = cli.inspect("mcp.workspace_inspect", config, local_profile, "workspace")
        forged_row = session_row(target(workspace_inventory, "connection/later"), "shared-session")

        second_call = device_client.request(
            "tools/call", {"name": "inventory", "arguments": {"group": "extension"}},
        )
        second_inventory = tool_payload(second_call)["inventory"]
        evidence.require(
            "mcp.long_lived_device_scope_remains_local",
            [item["target_id"] for item in second_inventory["targets"]] == ["device"],
            targets=[item["target_id"] for item in second_inventory["targets"]],
        )
        workspace_second_call = workspace_client.request(
            "tools/call", {"name": "inventory", "arguments": {"group": "extension"}},
        )
        workspace_second_inventory = tool_payload(workspace_second_call)["inventory"]
        workspace_target_ids = [item["target_id"] for item in workspace_second_inventory["targets"]]
        evidence.require(
            "mcp.long_lived_workspace_scope_reloads_cli_save",
            workspace_target_ids == ["device", "connection/later"],
            targets=workspace_target_ids,
        )

        forged = device_client.request(
            "tools/call",
            {
                "name": "rename_fixture",
                "arguments": {
                    "target_id": forged_row["target_id"],
                    "token": forged_row["token"],
                    "name": "forged-remote-name",
                },
            },
        )
        evidence.require(
            "mcp.forged_remote_rename_denied",
            domain_error_code(forged) == "scope_violation",
            response=forged,
        )
        cli.refresh("mcp.refresh_after_forgery", config, "later")
        after_forgery = cli.inspect("mcp.inspect_after_forgery", config, local_profile, "workspace")
        untouched = session_row(target(after_forgery, "connection/later"), "shared-session")
        evidence.require(
            "mcp.denial_did_not_mutate_remote",
            untouched["session"]["name"] == "renamed-through-cli",
            session=untouched["session"],
        )

        local_row = session_row(target(second_inventory, "device"), "shared-session")
        renamed = device_client.request(
            "tools/call",
            {
                "name": "rename_fixture",
                "arguments": {
                    "target_id": local_row["target_id"],
                    "token": local_row["token"],
                    "name": "renamed-through-mcp",
                },
            },
        )
        renamed_payload = tool_payload(renamed)
        renamed_session = tool_session(renamed_payload)
        evidence.require(
            "mcp.local_fixture_rename_succeeded",
            renamed_session.get("name") == "renamed-through-mcp",
            response=renamed,
        )
        cli_seen = cli.inspect("mcp.cli_observes_mcp_rename", config, local_profile, "device")
        observed = session_row(target(cli_seen, "device"), "shared-session")
        evidence.require(
            "mcp.and_cli_share_workspace_api",
            observed["session"]["name"] == "renamed-through-mcp",
            session=observed["session"],
        )
        return {
            "classification": "real long-lived stdio MCP and real CLI over one saved workspace",
            "initialize": initialized,
            "workspace_initialize": workspace_initialized,
            "tools": sorted(name for name in tool_names if isinstance(name, str)),
            "device_targets_before_connection": [item["target_id"] for item in first_inventory["targets"]],
            "device_targets_after_connection": [item["target_id"] for item in second_inventory["targets"]],
            "workspace_targets_before_connection": [item["target_id"] for item in workspace_first_inventory["targets"]],
            "workspace_targets_after_connection": workspace_target_ids,
            "forged_remote_error": domain_error_code(forged),
            "local_rename": renamed_session,
            "cli_observed_name": observed["session"]["name"],
        }
    finally:
        try:
            workspace_client.close()
        finally:
            device_client.close()


REMOTE_STOP = r'''import os, select, signal, sys
from pathlib import Path
pid_file = Path(sys.argv[1])
if not pid_file.exists():
    sys.exit(0)
pid, token = pid_file.read_text().split()
pid = int(pid)
try:
    fd = os.pidfd_open(pid)
except ProcessLookupError:
    sys.exit(0)
try:
    actual_token = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()[19]
    actual_exe = os.readlink(f'/proc/{pid}/exe')
    if actual_token != token or actual_exe != sys.argv[2]:
        raise RuntimeError('fixture owner identity changed; refusing signal or cleanup')
    signal.pidfd_send_signal(fd, signal.SIGTERM)
    poller = select.poll()
    poller.register(fd, select.POLLIN)
    if not poller.poll(5000):
        raise RuntimeError('fixture owner did not exit; preserving scratch directory')
finally:
    os.close(fd)
'''


class RemoteOwner:
    def __init__(self, evidence: Evidence, host: str, linux_binary: Path) -> None:
        self.evidence = evidence
        self.host = host
        self.linux_binary = linux_binary
        self.ssh = ["ssh", *SSH_OPTIONS, host]
        self.root = ""
        self.binary = ""
        self.profile = ""
        self.server: subprocess.Popen[str] | None = None
        self.counter = 0
        self.describe: dict[str, Any] | None = None
        self.shutdown_verified = False

    def remote(self, argv: list[str]) -> list[str]:
        return [*self.ssh, shlex.join(argv)]

    def start(self) -> None:
        created = self.evidence.run(
            "ssh.mktemp", self.remote(["mktemp", "-d", "/tmp/am-application-examples.XXXXXX"]),
        )
        self.root = created.stdout.strip()
        if not self.root.startswith("/tmp/am-application-examples.") or "/" in self.root[len("/tmp/"):]:
            raise ProofFailure(f"SSH returned unsafe scratch path {self.root!r}")
        self.binary = self.root + "/architecture-poc"
        self.profile = self.root + "/profile"
        self.evidence.run(
            "ssh.copy_owner",
            ["scp", *SSH_OPTIONS, str(self.linux_binary), f"{self.host}:{self.binary}"],
        )
        self.evidence.run("ssh.protect_owner", self.remote(["chmod", "700", self.binary]))
        wrapper = (
            "set -eu; root=$1; binary=$2; profile=$3; "
            "printf '%s ' \"$$\" > \"$root/owner.pid\"; "
            "awk '{print $22}' /proc/$$/stat >> \"$root/owner.pid\"; "
            "exec \"$binary\" serve --dir \"$profile\" --revision 3"
        )
        argv = self.remote(["sh", "-c", wrapper, "owner", self.root, self.binary, self.profile])
        self.server = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.evidence.record("lifecycle", "ssh.owner_start", argv=argv, pid=self.server.pid)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if self.server.poll() is not None:
                _, stderr = self.server.communicate()
                raise ProofFailure(f"remote owner exited during startup: {stderr.strip()}")
            try:
                response = self.rpc("owner.describe", {}, readiness=True)
                if response.get("ok"):
                    self.describe = response
                    return
            except (ProofFailure, subprocess.SubprocessError):
                pass
            time.sleep(0.1)
        raise ProofFailure("remote owner readiness timed out")

    def rpc(self, operation: str, arguments: dict[str, Any], *, readiness: bool = False) -> dict[str, Any]:
        self.counter += 1
        request = {
            "protocol_major": 1,
            "request_id": f"application-example-ssh-{self.counter}",
            "operation": operation,
            "arguments": arguments,
        }
        argv = self.remote([self.binary, "rpc", "--dir", self.profile])
        if readiness:
            completed = subprocess.run(
                argv, input=json.dumps(request) + "\n", capture_output=True, text=True, timeout=5,
            )
            if completed.returncode != 0 or not completed.stdout.strip():
                raise ProofFailure("remote owner is not ready")
            response = exact_json(completed.stdout, "ssh owner readiness")
            if not isinstance(response, dict):
                raise ProofFailure("remote readiness response is not an object")
            self.evidence.record("owner_rpc", "ssh.owner_ready", request=request, response=response)
            return response
        return self.evidence.run_json("ssh." + operation, argv, input_document=request)

    def seed(self) -> dict[str, Any]:
        if self.describe is None:
            raise ProofFailure("remote owner was not described")
        owner = self.describe["owner"]
        response = self.rpc(
            "fixture_session.create_bound",
            {
                "expected_environment_id": owner["environment_id"],
                "expected_owner_instance_id": owner["instance_id"],
                "session": {
                    "id": "shared-session", "name": "ssh-origin", "tool": "codex",
                    "cwd": self.root + "/fixture-worktree", "group": "extension",
                },
            },
        )
        self.evidence.require("ssh.bound_seed_succeeded", response.get("ok") is True)
        return response

    def stop(self) -> None:
        if not self.root:
            return
        stopped = self.evidence.run(
            "ssh.stop_owner",
            self.remote(["python3", "-c", REMOTE_STOP, self.root + "/owner.pid", self.binary]),
        )
        self.shutdown_verified = stopped.returncode == 0
        if self.server is not None:
            try:
                stdout, stderr = self.server.communicate(timeout=5)
            except subprocess.TimeoutExpired as error:
                raise ProofFailure("verified remote shutdown did not close SSH process") from error
            self.evidence.record(
                "lifecycle", "ssh.owner_stop", returncode=self.server.returncode,
                stdout=stdout, stderr=stderr, graceful=True,
            )
        if self.shutdown_verified:
            self.evidence.run("ssh.remove_scratch", self.remote(["rm", "-rf", "--", self.root]))
        else:
            raise ProofFailure(f"remote shutdown was not verified; preserved {self.root}")


def ssh_example(
    evidence: Evidence,
    cli: WorkspaceCLI,
    root: Path,
    local_profile: Path,
    host: str,
    linux_owner_binary: Path,
) -> dict[str, Any]:
    remote = RemoteOwner(evidence, host, linux_owner_binary)
    try:
        remote.start()
        remote.seed()
        config = root / "workspace-ssh.json"
        cli.call(
            "ssh.application_connect", "connect",
            "--config", str(config), "--owner-binary", str(cli.owner_binary),
            "--id", "ssh-lab", "--label", "Real SSH owner", "--profile", remote.profile,
            "--ssh-host", host, "--remote-owner-binary", remote.binary,
        )
        cli.refresh("ssh.application_refresh", config, "ssh-lab")
        inventory = cli.inspect("ssh.application_inspect", config, local_profile, "workspace", "extension")
        row = session_row(target(inventory, "connection/ssh-lab"), "shared-session")
        cli.rename_fixture(
            "ssh.application_rename", config, local_profile, "workspace",
            row["target_id"], row["token"], "renamed-over-real-ssh",
        )
        cli.refresh("ssh.application_refresh_after_rename", config, "ssh-lab")
        after = cli.inspect("ssh.application_verify", config, local_profile, "workspace", "extension")
        persisted = session_row(target(after, "connection/ssh-lab"), "shared-session")
        evidence.require(
            "ssh.full_application_route_persisted_rename",
            persisted["session"]["name"] == "renamed-over-real-ssh",
            session=persisted["session"],
        )
        return {
            "executed": True,
            "classification": "real SSH transport, disposable Linux owner, and real application API",
            "host": host,
            "target_id": persisted["target_id"],
            "persisted_session": persisted["session"],
        }
    finally:
        remote.stop()


def execute(args: argparse.Namespace, evidence: Evidence) -> dict[str, Any]:
    workspace_binary = Path(args.workspace_binary).resolve()
    owner_binary = Path(args.owner_binary).resolve()
    evidence.require("input.workspace_binary_exists", workspace_binary.is_file(), path=str(workspace_binary))
    evidence.require("input.owner_binary_exists", owner_binary.is_file(), path=str(owner_binary))
    if bool(args.ssh_host) != bool(args.linux_owner_binary):
        raise ProofFailure("--ssh-host and --linux-owner-binary must be supplied together")

    with tempfile.TemporaryDirectory(prefix="am-application-examples-") as temporary:
        scratch = Path(temporary)
        os.chmod(scratch, 0o700)
        local = OwnerProcess(evidence, owner_binary, scratch / "device", "device-owner")
        standin = OwnerProcess(evidence, owner_binary, scratch / "standin", "standin-owner")
        try:
            local.start()
            try:
                standin.start()
                local.seed("shared-session", "local-origin", "extension")
                standin.seed("shared-session", "standin-origin", "extension")
                cli = WorkspaceCLI(evidence, workspace_binary, owner_binary)
                cli_result = cli_examples(evidence, cli, scratch, local.profile, standin.profile)
                repository_result = concurrent_repository_example(
                    evidence, cli, scratch, local.profile, standin.profile,
                )
                mcp_result = mcp_examples(
                    evidence, cli, workspace_binary, owner_binary, scratch,
                    local.profile, standin.profile,
                )
                if args.ssh_host:
                    linux_binary = Path(args.linux_owner_binary).resolve()
                    evidence.require("input.linux_owner_binary_exists", linux_binary.is_file(), path=str(linux_binary))
                    ssh_result = ssh_example(
                        evidence, cli, scratch, local.profile, args.ssh_host, linux_binary,
                    )
                else:
                    ssh_result = {
                        "executed": False,
                        "classification": "not run; no SSH host and Linux owner binary supplied",
                    }
                return {
                    "status": "pass",
                    "real_processes": {
                        "local_revision_3_owners": 2,
                        "bound_fixture_seeds": 2,
                        "cli": cli_result,
                        "cross_process_repository": repository_result,
                        "mcp": mcp_result,
                        "ssh": ssh_result,
                    },
                    "simulation_boundary": (
                        "The saved standin connection uses a second local owner profile to model placement. "
                        "Its owner processes, protocol calls, persistence, CLI, MCP, guards, and mutations are real."
                    ),
                    "not_claimed": [
                        "agent launch or tmux ownership",
                        "durable mutation receipts or retry after an uncertain response",
                        "same-pane input fencing",
                        "full production application validation",
                    ],
                    "assertions": sum(event["kind"] == "assertion" for event in evidence.events),
                    "event_count": len(evidence.events),
                }
            finally:
                standin.stop()
        finally:
            local.stop()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace-binary", required=True)
    parser.add_argument("--owner-binary", required=True)
    parser.add_argument("--ssh-host")
    parser.add_argument("--linux-owner-binary")
    parser.add_argument("--output", default=str(ROOT / "application-results.json"))
    parser.add_argument("--log", default=str(ROOT / "application-examples.log"))
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    output = Path(args.output).resolve()
    evidence = Evidence(Path(args.log).resolve())
    try:
        result = execute(args, evidence)
        result["assertions"] = sum(event["kind"] == "assertion" for event in evidence.events)
        result["event_count"] = len(evidence.events)
        exit_code = 0
    except Exception as error:  # Preserve the exact failure as runnable evidence.
        evidence.record("failure", "application_examples", error_type=type(error).__name__, message=str(error))
        result = {
            "status": "fail",
            "error": {"type": type(error).__name__, "message": str(error)},
            "assertions": sum(event["kind"] == "assertion" for event in evidence.events),
            "event_count": len(evidence.events),
        }
        exit_code = 1
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps(result, sort_keys=True))
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
