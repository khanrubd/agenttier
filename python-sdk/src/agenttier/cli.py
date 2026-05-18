# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""AgentTier command-line interface.

Pure-Python CLI built on top of the AgentTier SDK. Installed as a
``console_scripts`` entry point named ``agenttier`` — see ``pyproject.toml``.

Why Python on top of the SDK and not a thin wrapper around the Go binary:
the Go CLI ships only ``configure`` and ``invoke`` as of v0.3.0. The Python
CLI exposes the full SDK surface (lifecycle, exec, files, port forwards,
templates, identity) so ``pip install agenttier`` is a one-stop install for
Python users. The Go CLI continues to ship as a native binary on GitHub
Releases for users who do not want a Python runtime.

Design notes:

* No third-party CLI framework — the CLI is a flat dispatch table over
  argparse subparsers. Keeps the wheel tiny (~25 KB added) and avoids a
  dependency on ``click`` / ``typer``.
* All commands work in two output modes: ``--output text`` (human
  default) and ``--output json`` (scriptable). The text formatter handles
  truncation; JSON is a verbatim dump of the SDK's pydantic models.
* SSE-streaming commands (``configure``, ``invoke``) reuse the SDK's
  generators directly, so any improvement to the streaming logic flows
  into the CLI for free.
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import sys
from pathlib import Path
from typing import Any, Callable, Optional, Sequence

from agenttier import AgentTierClient
from agenttier._version import __version__
from agenttier.exceptions import AgentTierError


# ----------------------------------------------------------------------
# Output helpers
# ----------------------------------------------------------------------


def _print_json(data: Any) -> None:
    """Dump ``data`` to stdout as pretty JSON, handling pydantic models."""

    def _encode(obj: Any) -> Any:
        if hasattr(obj, "model_dump"):
            return obj.model_dump(mode="json")
        if isinstance(obj, list):
            return [_encode(o) for o in obj]
        if isinstance(obj, dict):
            return {k: _encode(v) for k, v in obj.items()}
        return obj

    json.dump(_encode(data), sys.stdout, indent=2, default=str)
    sys.stdout.write("\n")


def _print_table(rows: Sequence[Sequence[str]], headers: Sequence[str]) -> None:
    """Render a simple aligned text table to stdout."""
    if not rows:
        for h in headers:
            sys.stdout.write(h + "  ")
        sys.stdout.write("\n(no results)\n")
        return
    widths = [len(h) for h in headers]
    for row in rows:
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], len(str(cell)))
    fmt = "  ".join("{:<" + str(w) + "}" for w in widths)
    sys.stdout.write(fmt.format(*headers).rstrip() + "\n")
    for row in rows:
        sys.stdout.write(fmt.format(*[str(c) for c in row]).rstrip() + "\n")


def _err(msg: str) -> None:
    sys.stderr.write(f"agenttier: {msg}\n")


# ----------------------------------------------------------------------
# Configuration: CLI flags → SDK env vars
# ----------------------------------------------------------------------


CONFIG_PATH = (
    Path(os.environ["AGENTTIER_CONFIG"])
    if os.environ.get("AGENTTIER_CONFIG")
    else Path.home() / ".config" / "agenttier" / "config.json"
)


def _load_config_file() -> dict[str, str]:
    if not CONFIG_PATH.exists():
        return {}
    try:
        data: dict[str, str] = json.loads(CONFIG_PATH.read_text())
        return data
    except (OSError, json.JSONDecodeError):
        return {}


def _save_config_file(config: dict[str, str]) -> None:
    CONFIG_PATH.parent.mkdir(parents=True, exist_ok=True)
    CONFIG_PATH.write_text(json.dumps(config, indent=2) + "\n")
    # Lock down permissions; the file may carry an API key.
    try:
        CONFIG_PATH.chmod(0o600)
    except OSError:
        pass


def _resolve_endpoint(args: argparse.Namespace) -> tuple[str, dict[str, str]]:
    """Return ``(api_url, env_overrides)`` for the SDK client.

    Precedence: CLI flag > env var > saved config file.
    Saved config file values are turned into env vars so the SDK's
    ``auto_detect_auth`` picks them up.
    """
    config = _load_config_file()
    env: dict[str, str] = {}

    api_url = args.api_url or os.environ.get("AGENTTIER_API_URL") or config.get("api_url", "")
    if not api_url:
        _err(
            "no API URL configured. Pass --api-url, set AGENTTIER_API_URL, "
            "or run `agenttier login --api-url <URL>`."
        )
        sys.exit(2)

    api_key = (
        getattr(args, "api_key", None)
        or os.environ.get("AGENTTIER_API_KEY")
        or config.get("api_key", "")
    )
    token = (
        getattr(args, "token", None)
        or os.environ.get("AGENTTIER_TOKEN")
        or config.get("token", "")
    )
    if api_key:
        env["AGENTTIER_API_KEY"] = api_key
    if token:
        env["AGENTTIER_TOKEN"] = token
    return api_url, env


def _client(args: argparse.Namespace) -> AgentTierClient:
    api_url, overrides = _resolve_endpoint(args)
    # Apply env overrides for the SDK's auto-detect.
    for k, v in overrides.items():
        os.environ[k] = v
    return AgentTierClient(api_url=api_url)


# ----------------------------------------------------------------------
# Top-level commands
# ----------------------------------------------------------------------


def cmd_version(args: argparse.Namespace) -> int:
    if args.output == "json":
        _print_json({"version": __version__})
    else:
        sys.stdout.write(f"agenttier {__version__}\n")
    return 0


def cmd_login(args: argparse.Namespace) -> int:
    if not args.api_url:
        _err("login requires --api-url")
        return 2
    config = _load_config_file()
    config["api_url"] = args.api_url
    if args.api_key:
        config["api_key"] = args.api_key
    if args.token:
        config["token"] = args.token
    _save_config_file(config)
    sys.stdout.write(f"Saved config to {CONFIG_PATH}\n")
    # Verify by hitting /user/me.
    try:
        with _client(args) as client:
            who = client.current_user()
            label = who.email or who.name or who.sub
            sys.stdout.write(f"Authenticated as: {label}\n")
    except AgentTierError as e:
        _err(f"saved config but auth check failed: {e}")
        return 1
    return 0


def cmd_whoami(args: argparse.Namespace) -> int:
    with _client(args) as client:
        who = client.current_user()
    if args.output == "json":
        _print_json(who)
    else:
        label = who.email or who.name or who.sub
        sys.stdout.write(label)
        if who.is_admin:
            sys.stdout.write(" (admin)")
        sys.stdout.write("\n")
    return 0


# ----------------------------------------------------------------------
# Sandbox commands
# ----------------------------------------------------------------------


def cmd_sandbox_list(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandboxes = client.list_sandboxes(namespace=args.namespace, status=args.status)
    if args.output == "json":
        _print_json(sandboxes)
        return 0
    rows = [
        (
            s.sandbox_id,
            s.name,
            s.status,
            s.template_ref or "",
            s.namespace,
        )
        for s in sandboxes
    ]
    _print_table(rows, headers=["ID", "NAME", "STATUS", "TEMPLATE", "NAMESPACE"])
    return 0


def cmd_sandbox_get(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        status = sandbox.status()
    if args.output == "json":
        _print_json(status)
    else:
        sys.stdout.write(f"id:        {status.sandbox_id}\n")
        sys.stdout.write(f"name:      {status.name}\n")
        sys.stdout.write(f"status:    {status.status}\n")
        sys.stdout.write(f"template:  {status.template_ref or '-'}\n")
        sys.stdout.write(f"namespace: {status.namespace}\n")
        if status.pod_name:
            sys.stdout.write(f"pod:       {status.pod_name}\n")
    return 0


def cmd_sandbox_create(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.create_sandbox(
            template=args.template,
            name=args.name,
            namespace=args.namespace,
            timeout=args.timeout,
            idle_timeout=args.idle_timeout,
            storage_size=args.storage_size,
        )
        if args.wait:
            sandbox.wait_until_running(timeout=args.wait_timeout)
        status = sandbox.status()
    if args.output == "json":
        _print_json(status)
    else:
        sys.stdout.write(f"created {status.sandbox_id} (status: {status.status})\n")
    return 0


def cmd_sandbox_stop(args: argparse.Namespace) -> int:
    with _client(args) as client:
        client.get_sandbox(args.sandbox_id).stop()
    sys.stdout.write(f"stopped {args.sandbox_id}\n")
    return 0


def cmd_sandbox_resume(args: argparse.Namespace) -> int:
    with _client(args) as client:
        client.get_sandbox(args.sandbox_id).resume()
    sys.stdout.write(f"resumed {args.sandbox_id}\n")
    return 0


def cmd_sandbox_delete(args: argparse.Namespace) -> int:
    with _client(args) as client:
        client.get_sandbox(args.sandbox_id).terminate()
    sys.stdout.write(f"deleted {args.sandbox_id}\n")
    return 0


def cmd_sandbox_exec(args: argparse.Namespace) -> int:
    if not args.command:
        _err("exec requires a command after `--`")
        return 2
    cmd = " ".join(shlex.quote(c) for c in args.command)
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        result = sandbox.exec(cmd, timeout=args.timeout)
    if args.output == "json":
        _print_json(result)
        return result.exit_code
    sys.stdout.write(result.stdout)
    if not result.stdout.endswith("\n") and result.stdout:
        sys.stdout.write("\n")
    if result.stderr:
        sys.stderr.write(result.stderr)
        if not result.stderr.endswith("\n"):
            sys.stderr.write("\n")
    return result.exit_code


def cmd_sandbox_wait(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        sandbox.wait_until_running(timeout=args.timeout)
    sys.stdout.write(f"{args.sandbox_id} is running\n")
    return 0


# ----------------------------------------------------------------------
# Files
# ----------------------------------------------------------------------


def cmd_files_ls(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        entries = sandbox.files.list(args.path)
    if args.output == "json":
        _print_json(entries)
        return 0
    rows = [
        (
            e.name + ("/" if e.is_dir else ""),
            "dir" if e.is_dir else "file",
            str(e.size),
            str(e.modified_at) if e.modified_at else "",
        )
        for e in entries
    ]
    _print_table(rows, headers=["NAME", "TYPE", "SIZE", "MODIFIED"])
    return 0


def cmd_files_cat(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        data = sandbox.files.read(args.path)
    sys.stdout.buffer.write(data)
    return 0


def cmd_files_upload(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        n = sandbox.files.upload(args.remote, args.local)
    sys.stdout.write(f"uploaded {n} bytes to {args.remote}\n")
    return 0


def cmd_files_download(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        n = sandbox.files.download(args.remote, args.local)
    sys.stdout.write(f"downloaded {n} bytes to {args.local}\n")
    return 0


def cmd_files_archive(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        n = sandbox.files.archive(args.output, args.path)
    sys.stdout.write(f"archived {args.path} to {args.output} ({n} bytes)\n")
    return 0


def cmd_files_write(args: argparse.Namespace) -> int:
    if args.data is not None:
        payload: bytes | str = args.data
    elif args.file == "-":
        payload = sys.stdin.buffer.read()
    elif args.file:
        payload = Path(args.file).read_bytes()
    else:
        _err("write requires --data or --file")
        return 2
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        sandbox.files.write(args.path, payload)
    sys.stdout.write(f"wrote {args.path}\n")
    return 0


# ----------------------------------------------------------------------
# Ports
# ----------------------------------------------------------------------


def cmd_ports_list(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        ports = sandbox.list_ports()
    if args.output == "json":
        _print_json(ports)
        return 0
    rows = [
        (str(p.port), p.protocol or "http", p.preview_url or p.internal_url or "")
        for p in ports
    ]
    _print_table(rows, headers=["PORT", "PROTOCOL", "URL"])
    return 0


def cmd_ports_forward(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        port = sandbox.forward_port(args.port, protocol=args.protocol)
    if args.output == "json":
        _print_json(port)
    else:
        target = port.preview_url or port.internal_url or "(no URL)"
        sys.stdout.write(f"forwarded port {port.port} → {target}\n")
    return 0


def cmd_ports_remove(args: argparse.Namespace) -> int:
    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        sandbox.remove_port(args.port)
    sys.stdout.write(f"removed forward for port {args.port}\n")
    return 0


# ----------------------------------------------------------------------
# Templates
# ----------------------------------------------------------------------


def cmd_template_list(args: argparse.Namespace) -> int:
    with _client(args) as client:
        templates = client.list_templates()
    if args.output == "json":
        _print_json(templates)
        return 0
    rows = [
        (t.name, getattr(t, "image", "") or "-", getattr(t, "description", "") or "")
        for t in templates
    ]
    _print_table(rows, headers=["NAME", "IMAGE", "DESCRIPTION"])
    return 0


def cmd_template_get(args: argparse.Namespace) -> int:
    with _client(args) as client:
        template = client.get_template(args.name)
    _print_json(template)
    return 0


# ----------------------------------------------------------------------
# Agent: configure + invoke + cancel (mirrors Go CLI)
# ----------------------------------------------------------------------


def cmd_configure(args: argparse.Namespace) -> int:
    files: list[dict[str, Any] | tuple[str, str | Path | bytes]] = []
    for f in args.file or []:
        if "=" not in f:
            _err(f"--file {f!r} must be path=local-path")
            return 2
        remote, local = f.split("=", 1)
        if not remote or not local:
            _err(f"--file {f!r} must be path=local-path")
            return 2
        files.append((remote, local))

    install = shlex.split(args.install) if args.install else None
    entrypoint = shlex.split(args.entrypoint) if args.entrypoint else None

    if not files and install is None and entrypoint is None:
        _err("nothing to do; pass at least one of --file, --install, --entrypoint")
        return 2

    def on_log(stream: str, line: str) -> None:
        out = sys.stderr if stream == "stderr" else sys.stdout
        out.write(line)
        if not line.endswith("\n"):
            out.write("\n")

    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        result = sandbox.agent.configure(
            files=files or None,
            install_command=install,
            entrypoint=entrypoint,
            on_log=on_log,
        )
    if args.output == "json":
        _print_json(result)
    else:
        sys.stdout.write(
            f"configure: install exit {result.install_exit_code}, "
            f"hash={result.install_command_hash}, skipped={result.skipped}\n"
        )
    return 0 if result.install_exit_code == 0 else 1


def cmd_invoke(args: argparse.Namespace) -> int:
    if args.cancel:
        with _client(args) as client:
            sandbox = client.get_sandbox(args.sandbox_id)
            sandbox.agent.invoke_cancel(args.cancel)
        sys.stdout.write(f"canceled {args.cancel}\n")
        return 0

    payload: Any = None
    if args.input == "-":
        payload = sys.stdin.buffer.read()
    elif args.input and args.input.startswith("@"):
        payload = Path(args.input[1:]).read_bytes()
    elif args.input:
        payload = args.input

    with _client(args) as client:
        sandbox = client.get_sandbox(args.sandbox_id)
        exit_code = 0
        for event in sandbox.agent.invoke_stream(
            payload,
            prompt=args.prompt,
            invoke_timeout=args.timeout,
        ):
            if event.event == "start":
                invoke_id = event.data.get("invokeId", "")
                if invoke_id:
                    sys.stderr.write(f"invoke started: {invoke_id}\n")
            elif event.event == "log":
                stream = event.data.get("stream", "stdout")
                line = event.data.get("data", "")
                out = sys.stderr if stream == "stderr" else sys.stdout
                out.write(line)
                if line and not line.endswith("\n"):
                    out.write("\n")
            elif event.event == "exit":
                exit_code = int(event.data.get("exitCode", 0))
                duration = event.data.get("durationMs", 0)
                reason = event.data.get("reason", "")
                sys.stderr.write(
                    f"invoke {reason}: exit {exit_code} (duration {duration}ms)\n"
                )
            elif event.event == "error":
                sys.stderr.write(f"invoke error: {event.data.get('message', '')}\n")
                exit_code = 1
    return exit_code


# ----------------------------------------------------------------------
# Argparse wiring
# ----------------------------------------------------------------------


def _add_global_flags(p: argparse.ArgumentParser) -> None:
    p.add_argument("--api-url", help="Router base URL (env: AGENTTIER_API_URL)")
    p.add_argument("--api-key", help="API key (env: AGENTTIER_API_KEY)")
    p.add_argument("--token", help="Bearer token / OIDC JWT (env: AGENTTIER_TOKEN)")
    p.add_argument(
        "--output",
        choices=["text", "json"],
        default="text",
        help="Output format. Default: text.",
    )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="agenttier",
        description="Control AgentTier sandboxes from the command line.",
    )
    parser.add_argument("--version", action="version", version=f"agenttier {__version__}")
    sub = parser.add_subparsers(dest="command", required=True, metavar="<command>")

    # version
    p_version = sub.add_parser("version", help="Print the CLI version")
    _add_global_flags(p_version)
    p_version.set_defaults(func=cmd_version)

    # login
    p_login = sub.add_parser("login", help="Save API URL and credentials to ~/.config/agenttier/config.json")
    _add_global_flags(p_login)
    p_login.set_defaults(func=cmd_login)

    # whoami
    p_whoami = sub.add_parser("whoami", help="Print the server's view of the caller's identity")
    _add_global_flags(p_whoami)
    p_whoami.set_defaults(func=cmd_whoami)

    # sandbox <subcommand>
    p_sandbox = sub.add_parser("sandbox", help="Manage sandboxes")
    sub_sandbox = p_sandbox.add_subparsers(dest="subcommand", required=True, metavar="<subcommand>")

    p_list = sub_sandbox.add_parser("list", help="List sandboxes")
    _add_global_flags(p_list)
    p_list.add_argument("--namespace", help="Filter by namespace")
    p_list.add_argument("--status", help="Filter by status (Running, Stopped, ...)")
    p_list.set_defaults(func=cmd_sandbox_list)

    p_get = sub_sandbox.add_parser("get", help="Show one sandbox")
    _add_global_flags(p_get)
    p_get.add_argument("sandbox_id")
    p_get.set_defaults(func=cmd_sandbox_get)

    p_create = sub_sandbox.add_parser("create", help="Create a sandbox from a template")
    _add_global_flags(p_create)
    p_create.add_argument("name", help="Sandbox name (DNS-friendly)")
    p_create.add_argument("--template", required=True, help="ClusterSandboxTemplate name")
    p_create.add_argument("--namespace", default="default")
    p_create.add_argument("--timeout", help='Max-runtime duration (e.g. "8h")')
    p_create.add_argument("--idle-timeout", dest="idle_timeout", help='Idle timeout (e.g. "30m")')
    p_create.add_argument("--storage-size", dest="storage_size", help='PVC size (e.g. "10Gi")')
    p_create.add_argument("--wait", action="store_true", help="Block until Running")
    p_create.add_argument(
        "--wait-timeout",
        dest="wait_timeout",
        type=int,
        default=180,
        help="Wait timeout in seconds (default: 180)",
    )
    p_create.set_defaults(func=cmd_sandbox_create)

    p_stop = sub_sandbox.add_parser("stop", help="Stop a sandbox (preserve PVC)")
    _add_global_flags(p_stop)
    p_stop.add_argument("sandbox_id")
    p_stop.set_defaults(func=cmd_sandbox_stop)

    p_resume = sub_sandbox.add_parser("resume", help="Resume a stopped sandbox")
    _add_global_flags(p_resume)
    p_resume.add_argument("sandbox_id")
    p_resume.set_defaults(func=cmd_sandbox_resume)

    p_delete = sub_sandbox.add_parser("delete", help="Delete a sandbox (permanent)")
    _add_global_flags(p_delete)
    p_delete.add_argument("sandbox_id")
    p_delete.set_defaults(func=cmd_sandbox_delete)

    p_exec = sub_sandbox.add_parser(
        "exec",
        help="Run a one-shot command in a sandbox",
    )
    _add_global_flags(p_exec)
    p_exec.add_argument("sandbox_id")
    p_exec.add_argument("--timeout", type=int, default=30, help="Command timeout (seconds)")
    p_exec.add_argument("command", nargs=argparse.REMAINDER, help="Command and args (after --)")
    p_exec.set_defaults(func=cmd_sandbox_exec)

    p_wait = sub_sandbox.add_parser("wait", help="Block until sandbox is Running")
    _add_global_flags(p_wait)
    p_wait.add_argument("sandbox_id")
    p_wait.add_argument("--timeout", type=int, default=180)
    p_wait.set_defaults(func=cmd_sandbox_wait)

    # sandbox files
    p_files = sub_sandbox.add_parser("files", help="File operations on a sandbox")
    sub_files = p_files.add_subparsers(dest="files_subcommand", required=True, metavar="<subcommand>")

    p_ls = sub_files.add_parser("ls", help="List files in a sandbox directory")
    _add_global_flags(p_ls)
    p_ls.add_argument("sandbox_id")
    p_ls.add_argument("--path", default="/workspace")
    p_ls.set_defaults(func=cmd_files_ls)

    p_cat = sub_files.add_parser("cat", help="Stream a sandbox file to stdout")
    _add_global_flags(p_cat)
    p_cat.add_argument("sandbox_id")
    p_cat.add_argument("path")
    p_cat.set_defaults(func=cmd_files_cat)

    p_up = sub_files.add_parser("upload", help="Upload a local file into a sandbox")
    _add_global_flags(p_up)
    p_up.add_argument("sandbox_id")
    p_up.add_argument("local", help="Local path to upload")
    p_up.add_argument("remote", help="Sandbox destination path (e.g. /workspace/script.py)")
    p_up.set_defaults(func=cmd_files_upload)

    p_dn = sub_files.add_parser("download", help="Download a sandbox file to a local path")
    _add_global_flags(p_dn)
    p_dn.add_argument("sandbox_id")
    p_dn.add_argument("remote", help="Sandbox source path")
    p_dn.add_argument("local", help="Local destination path")
    p_dn.set_defaults(func=cmd_files_download)

    p_arc = sub_files.add_parser(
        "archive",
        help="Download a sandbox directory as a streamed .zip (default /workspace)",
    )
    _add_global_flags(p_arc)
    p_arc.add_argument("sandbox_id")
    p_arc.add_argument(
        "-o", "--output", required=True, help="Local path for the .zip output"
    )
    p_arc.add_argument(
        "--path",
        default="/workspace",
        help="Sandbox directory to archive (must live under /workspace)",
    )
    p_arc.set_defaults(func=cmd_files_archive)

    p_wr = sub_files.add_parser("write", help="Write inline content into a sandbox file")
    _add_global_flags(p_wr)
    p_wr.add_argument("sandbox_id")
    p_wr.add_argument("path", help="Sandbox destination path")
    p_wr.add_argument("--data", help="Inline content (utf-8)")
    p_wr.add_argument("--file", help='Local file path; "-" reads stdin')
    p_wr.set_defaults(func=cmd_files_write)

    # sandbox ports
    p_ports = sub_sandbox.add_parser("ports", help="Port forwarding")
    sub_ports = p_ports.add_subparsers(dest="ports_subcommand", required=True, metavar="<subcommand>")

    p_pl = sub_ports.add_parser("list", help="List forwarded ports")
    _add_global_flags(p_pl)
    p_pl.add_argument("sandbox_id")
    p_pl.set_defaults(func=cmd_ports_list)

    p_pf = sub_ports.add_parser("forward", help="Forward a port out of the sandbox")
    _add_global_flags(p_pf)
    p_pf.add_argument("sandbox_id")
    p_pf.add_argument("--port", type=int, required=True)
    p_pf.add_argument("--protocol", default="http", choices=["http", "tcp"])
    p_pf.set_defaults(func=cmd_ports_forward)

    p_pr = sub_ports.add_parser("remove", help="Remove a port forward")
    _add_global_flags(p_pr)
    p_pr.add_argument("sandbox_id")
    p_pr.add_argument("--port", type=int, required=True)
    p_pr.set_defaults(func=cmd_ports_remove)

    # template <subcommand>
    p_tmpl = sub.add_parser("template", help="Manage sandbox templates")
    sub_tmpl = p_tmpl.add_subparsers(dest="subcommand", required=True, metavar="<subcommand>")

    p_tl = sub_tmpl.add_parser("list", help="List ClusterSandboxTemplates")
    _add_global_flags(p_tl)
    p_tl.set_defaults(func=cmd_template_list)

    p_tg = sub_tmpl.add_parser("get", help="Show one ClusterSandboxTemplate")
    _add_global_flags(p_tg)
    p_tg.add_argument("name")
    p_tg.set_defaults(func=cmd_template_get)

    # configure / invoke (top-level so they mirror the Go CLI)
    p_cfg = sub.add_parser(
        "configure",
        help="Configure an agent-mode sandbox (upload files, run install, set entrypoint)",
    )
    _add_global_flags(p_cfg)
    p_cfg.add_argument("sandbox_id")
    p_cfg.add_argument(
        "--file",
        action="append",
        help='Upload a file: "remote=local". Repeatable.',
    )
    p_cfg.add_argument(
        "--install",
        help='Install command (e.g. "pip install -r requirements.txt")',
    )
    p_cfg.add_argument(
        "--entrypoint",
        help='Entrypoint command (e.g. "python /workspace/agent.py")',
    )
    p_cfg.set_defaults(func=cmd_configure)

    p_inv = sub.add_parser(
        "invoke",
        help="Invoke an agent-mode sandbox and stream output",
    )
    _add_global_flags(p_inv)
    p_inv.add_argument("sandbox_id")
    p_inv.add_argument("--prompt", help="Convenience prompt forwarded as --prompt query param")
    p_inv.add_argument(
        "--input",
        help='Body forwarded to entrypoint stdin. Inline string, "@/path/to/file", or "-" for stdin.',
    )
    p_inv.add_argument(
        "--timeout",
        help='Server-side per-invoke timeout (e.g. "5m"). Defaults to template setting.',
    )
    p_inv.add_argument("--cancel", help="Cancel an in-flight invoke by ID")
    p_inv.set_defaults(func=cmd_invoke)

    return parser


# ----------------------------------------------------------------------
# Entry point
# ----------------------------------------------------------------------


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    func: Callable[[argparse.Namespace], int] = args.func
    try:
        return func(args)
    except KeyboardInterrupt:
        sys.stderr.write("\nagenttier: interrupted\n")
        return 130
    except AgentTierError as e:
        _err(str(e))
        return 1


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
