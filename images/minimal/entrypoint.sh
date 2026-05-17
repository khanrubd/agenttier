#!/bin/sh
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# AgentTier sandbox-minimal entrypoint. Same shape as sandbox-general.
set -e
RUNTIME_BIN=/usr/local/bin/agenttier-sandbox-runtime
if [ -x "${RUNTIME_BIN}" ]; then
    "${RUNTIME_BIN}" &
fi
exec "$@"
