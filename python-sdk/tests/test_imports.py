# Copyright 2024 AgentTier Authors.
#
# SPDX-License-Identifier: Apache-2.0

"""Smoke tests ensuring the public API imports cleanly."""


def test_import_client() -> None:
    from agenttier import AgentTierClient

    assert AgentTierClient is not None


def test_import_async_client() -> None:
    from agenttier.async_client import AsyncAgentTierClient

    assert AsyncAgentTierClient is not None


def test_import_models() -> None:
    from agenttier.models import CommandResult, FileInfo, SandboxStatus

    assert CommandResult is not None
    assert FileInfo is not None
    assert SandboxStatus is not None


def test_version_exported() -> None:
    import agenttier

    assert isinstance(agenttier.__version__, str)
    assert agenttier.__version__ != ""
