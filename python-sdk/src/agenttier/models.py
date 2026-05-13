# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Pydantic v2 models for AgentTier REST responses.

The Router serializes fields in camelCase; we surface them to Python users as
snake_case. Both names are accepted when parsing so downstream code can be
written either way.
"""

from __future__ import annotations

from datetime import datetime
from enum import Enum
from typing import Any, Optional

from pydantic import BaseModel, ConfigDict, Field


def _to_camel(s: str) -> str:
    head, *tail = s.split("_")
    return head + "".join(p.capitalize() for p in tail)


class _Model(BaseModel):
    """Internal base with camelCase serialization and permissive extras."""

    model_config = ConfigDict(
        alias_generator=_to_camel,
        populate_by_name=True,
        # Router responses may grow new fields; don't break old SDK versions.
        extra="ignore",
    )


class SandboxPhase(str, Enum):
    """Lifecycle phases for a sandbox."""

    CREATING = "Creating"
    RUNNING = "Running"
    STOPPED = "Stopped"
    ERROR = "Error"
    DELETING = "Deleting"
    UNKNOWN = "Unknown"


class CreatedBy(_Model):
    """Identity of the user that created a sandbox."""

    email: Optional[str] = None
    display_name: Optional[str] = None


class SandboxSummary(_Model):
    """Sandbox as returned by list/get endpoints."""

    sandbox_id: str = Field(alias="sandboxId")
    name: str
    namespace: str
    status: str
    pod_name: Optional[str] = Field(default=None, alias="podName")
    pvc_name: Optional[str] = Field(default=None, alias="pvcName")
    template_ref: Optional[str] = Field(default=None, alias="templateRef")
    created_at: Optional[str] = Field(default=None, alias="createdAt")
    last_activity_at: Optional[str] = Field(default=None, alias="lastActivityAt")
    created_by: Optional[CreatedBy] = Field(default=None, alias="createdBy")
    message: Optional[str] = None

    @property
    def phase(self) -> SandboxPhase:
        """Typed view of ``status``; returns ``UNKNOWN`` for unexpected values."""
        try:
            return SandboxPhase(self.status)
        except ValueError:
            return SandboxPhase.UNKNOWN


class CommandResult(_Model):
    """Output of a non-interactive command run inside a sandbox."""

    stdout: str
    stderr: str
    exit_code: int = Field(alias="exitCode")


class Template(_Model):
    """Sandbox template as exposed by the Router templates endpoints."""

    name: str
    description: Optional[str] = None
    image: Optional[str] = None
    resource_version: Optional[str] = Field(default=None, alias="resourceVersion")
    # ``spec`` is the full SandboxTemplateSpec — kept as a free-form dict so the
    # SDK does not have to track every field as it evolves.
    spec: Optional[dict[str, Any]] = None


class ForwardedPort(_Model):
    """A port exposed from the sandbox via the Router."""

    port: int
    protocol: str
    preview_url: Optional[str] = Field(default=None, alias="previewUrl")
    internal_url: Optional[str] = Field(default=None, alias="internalUrl")


class CurrentUser(_Model):
    """The authenticated identity as seen by the Router."""

    sub: str
    email: Optional[str] = None
    name: Optional[str] = None
    groups: list[str] = Field(default_factory=list)
    is_admin: bool = Field(default=False, alias="isAdmin")


class UsageAnalytics(_Model):
    """Fleet-wide usage summary returned by /analytics/usage."""

    total_sandboxes: int = Field(alias="total_sandboxes")
    status_breakdown: dict[str, int] = Field(alias="status_breakdown")
    template_breakdown: dict[str, int] = Field(alias="template_breakdown")
    avg_startup_ms: int = Field(alias="avg_startup_ms")
    startup_sample_count: int = Field(alias="startup_sample_count")


class AuditEvent(_Model):
    """One entry from the activity log."""

    timestamp: Optional[datetime] = None
    event_type: Optional[str] = Field(default=None, alias="eventType")
    sandbox_id: Optional[str] = Field(default=None, alias="sandboxId")
    sandbox_name: Optional[str] = Field(default=None, alias="sandboxName")
    namespace: Optional[str] = None
    user_email: Optional[str] = Field(default=None, alias="userEmail")
    details: Optional[dict[str, Any]] = None


class FileEntry(_Model):
    """One entry returned by the files list endpoint.

    ``modified_at`` is seconds since epoch from the sandbox ``ls -la --time-style=+%s``
    output; kept as an int so callers can decide what timezone to render in.
    """

    name: str
    size: int = 0
    is_dir: bool = Field(default=False, alias="isDir")
    mode: Optional[str] = None
    modified_at: Optional[int] = Field(default=None, alias="modifiedAt")
