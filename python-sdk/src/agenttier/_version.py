# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Single source of truth for the SDK version string.

Kept in a separate module so ``pyproject.toml`` and :mod:`agenttier` can both
import it without circular dependencies. Release tooling reads this file when
bumping versions; keep the identifier named ``__version__``.
"""

__version__ = "0.4.0"
