# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Smoke tests: make sure the public API imports cleanly and is well-formed."""

from __future__ import annotations


def test_import_public_api() -> None:
    import agenttier

    assert agenttier.AgentTierClient is not None
    assert agenttier.AsyncAgentTierClient is not None
    assert agenttier.Sandbox is not None
    assert agenttier.AsyncSandbox is not None


def test_version_is_valid_semver() -> None:
    import re

    import agenttier

    assert isinstance(agenttier.__version__, str)
    assert re.match(r"^\d+\.\d+\.\d+(?:[-+.].+)?$", agenttier.__version__)


def test_all_exports_are_defined() -> None:
    """Every name in ``__all__`` must be importable from the top-level module."""
    import agenttier

    for name in agenttier.__all__:
        assert hasattr(agenttier, name), f"agenttier.{name} missing from module"


def test_py_typed_marker_installed() -> None:
    """``py.typed`` must be inside the installed package for mypy consumers."""
    import importlib.resources

    files = list(importlib.resources.files("agenttier").iterdir())
    names = {f.name for f in files}
    assert "py.typed" in names, "py.typed marker is missing; typed consumers won't see type hints"
