# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Model parsing tests, especially camelCase handling."""

from __future__ import annotations

from agenttier.models import (
    CommandResult,
    ForwardedPort,
    SandboxPhase,
    SandboxSummary,
)


def test_sandbox_summary_parses_camel_case_from_router() -> None:
    payload = {
        "sandboxId": "sbx-1",
        "name": "sbx-1",
        "namespace": "default",
        "status": "Running",
        "podName": "sbx-1-pod",
        "pvcName": "sbx-1-pvc",
        "templateRef": "general-coding",
        "createdAt": "2026-01-01T00:00:00Z",
        "lastActivityAt": "2026-01-01T00:01:00Z",
        "createdBy": {"email": "a@b.c", "displayName": "A B"},
        "message": "",
    }
    summary = SandboxSummary.model_validate(payload)
    assert summary.sandbox_id == "sbx-1"
    assert summary.pod_name == "sbx-1-pod"
    assert summary.template_ref == "general-coding"
    assert summary.created_by is not None
    assert summary.created_by.email == "a@b.c"
    assert summary.phase is SandboxPhase.RUNNING


def test_sandbox_summary_tolerates_unknown_phase() -> None:
    summary = SandboxSummary.model_validate(
        {"sandboxId": "x", "name": "x", "namespace": "d", "status": "Weird"}
    )
    assert summary.phase is SandboxPhase.UNKNOWN


def test_sandbox_summary_tolerates_extra_fields() -> None:
    # Forward-compat: new server fields must not break old SDK.
    summary = SandboxSummary.model_validate(
        {"sandboxId": "x", "name": "x", "namespace": "d", "status": "Stopped", "newField": 42}
    )
    assert summary.phase is SandboxPhase.STOPPED


def test_command_result_camel_exit_code() -> None:
    r = CommandResult.model_validate({"stdout": "hi\n", "stderr": "", "exitCode": 0})
    assert r.exit_code == 0


def test_forwarded_port_aliases() -> None:
    p = ForwardedPort.model_validate(
        {
            "port": 8080,
            "protocol": "http",
            "previewUrl": "https://sandbox-x-8080.preview.example/",
            "internalUrl": "http://pf-x-8080.default.svc.cluster.local:8080",
        }
    )
    assert p.preview_url == "https://sandbox-x-8080.preview.example/"
    assert p.internal_url.startswith("http://pf-")
