#!/bin/sh
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# AgentTier sandbox-claude-code entrypoint. Same shape as
# sandbox-general: launch the in-pod runtime in the background, then
# exec into the user's CMD so PID 1 stays whatever the operator
# configured. See images/general-coding/entrypoint.sh for design notes.

set -e
RUNTIME_BIN=/usr/local/bin/agenttier-sandbox-runtime
if [ -x "${RUNTIME_BIN}" ]; then
    "${RUNTIME_BIN}" &
fi
exec "$@"
