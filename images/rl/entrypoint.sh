#!/bin/sh
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# AgentTier sandbox-rl entrypoint. Same shape as the rest of the reference
# images — background the in-pod runtime so /exec, /files, and /invoke
# route via HTTP, then exec the user's CMD (default: sleep infinity).
set -e
RUNTIME_BIN=/usr/local/bin/agenttier-sandbox-runtime
if [ -x "${RUNTIME_BIN}" ]; then
    "${RUNTIME_BIN}" &
fi
exec "$@"
