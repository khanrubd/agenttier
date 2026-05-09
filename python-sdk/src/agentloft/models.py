# Copyright 2024 AgentLoft Authors.
# SPDX-License-Identifier: Apache-2.0

"""Pydantic models for AgentLoft API responses."""

from __future__ import annotations

from datetime import datetime
from typing import Optional

from pydantic import BaseModel


class SandboxStatus(BaseModel):
    """Status of a sandbox."""

    phase: str  # Creating, Running, Stopped, Error, Deleting
    pod_name: Optional[str] = None
    pvc_name: Optional[str] = None
    resolved_template: Optional[str] = None
    started_at: Optional[datetime] = None
    last_activity_at: Optional[datetime] = None
    restart_count: int = 0
    message: Optional[str] = None


class SandboxSpec(BaseModel):
    """Sandbox specification."""

    sandbox_id: str
    name: str
    namespace: str
    template_ref: Optional[str] = None
    status: str
    created_by: Optional[dict[str, str]] = None
    created_at: Optional[datetime] = None


class CommandResult(BaseModel):
    """Result of a command execution."""

    stdout: str
    stderr: str
    exit_code: int


class FileInfo(BaseModel):
    """Information about a file in the sandbox."""

    name: str
    path: str
    size: int
    is_dir: bool
    modified: Optional[datetime] = None
    permissions: Optional[str] = None


class Template(BaseModel):
    """Sandbox template."""

    name: str
    namespace: Optional[str] = None
    description: Optional[str] = None
    image: Optional[str] = None
