#!/bin/sh
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# AgentTier sandbox-general entrypoint.
#
# Starts the in-pod runtime HTTP server in the background, then exec's the
# command the operator passed (defaulting to `sleep infinity` for
# interactive sandboxes). The runtime is opt-in for now: when
# AGENTTIER_RUNTIME_TOKEN is unset, the runtime still launches but logs a
# loud warning that it's accepting unauthenticated requests, and the
# Router won't dial it because Phase 3 only adds a NetworkPolicy when the
# token is provisioned.
#
# We deliberately keep the runtime as a background process rather than PID
# 1 so the user's process semantics are unchanged. PID 1 stays whatever
# the operator's CMD is — same shell history, same signal handling, same
# `kubectl exec` UX. If the runtime crashes for any reason, the sandbox
# keeps working over the legacy SPDY exec path. That's the foundation of
# the "do not break existing sandboxes" guarantee for this rollout.

set -e

# Path to the pre-built runtime binary baked in at /usr/local/bin during
# image build.
RUNTIME_BIN=/usr/local/bin/agenttier-sandbox-runtime

# Skip launching the runtime if it's missing. This shouldn't happen in our
# own builds but lets users base downstream images off this one without
# inheriting the runtime if they don't want it.
if [ -x "${RUNTIME_BIN}" ]; then
    # Background the runtime; redirect its logs to /var/log so they don't
    # mix with the user's stdout/stderr (which feed kubectl logs).
    "${RUNTIME_BIN}" > /var/log/agenttier-runtime.log 2>&1 &
fi

# Hand off to the configured command. `exec` replaces the shell so the
# user's process inherits PID 2 (or higher if there's anything else
# initd) — important for clean SIGTERM handling on `kubectl delete pod`.
exec "$@"
