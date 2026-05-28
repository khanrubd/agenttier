#!/bin/sh
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# AgentTier sandbox-openclaw entrypoint. Same shape as
# sandbox-general / sandbox-claude-code: launch the in-pod runtime in
# the background, then exec into the user's CMD so PID 1 stays whatever
# the operator configured.
#
# Two extra jobs vs the other entrypoints:
#
# 1. Seed the OpenClaw config to ~/.openclaw/openclaw.json (the canonical
#    filename OpenClaw looks for) on first launch. The baked config
#    pre-enables the amazon-bedrock provider plugin, opts into discovery,
#    and pins the primary model to a current Bedrock-supported Claude
#    so `openclaw models list` and `openclaw agent --local` work
#    immediately against IRSA-injected credentials with no `openclaw onboard`
#    interactive flow.
#
# 2. Seed the amazon-bedrock provider plugin into ~/.openclaw/npm.
#    OpenClaw stores plugins under the user's home (writable PVC), not
#    in the global npm prefix. The plugin is installed at image build
#    time into /opt/openclaw-plugins, and the entrypoint copies it onto
#    the PVC if it isn't already there.
#
# We only seed on first launch (no overwrite on either) so anything the
# user customizes — additional providers, auth profiles, custom models,
# extra plugins — survives across stop/resume cycles.

set -e

OPENCLAW_DIR="${HOME:-/workspace}/.openclaw"
SEED_CONFIG=/etc/openclaw/openclaw.json
TARGET_CONFIG="${OPENCLAW_DIR}/openclaw.json"

if [ -r "${SEED_CONFIG}" ] && [ ! -e "${TARGET_CONFIG}" ]; then
    mkdir -p "${OPENCLAW_DIR}"
    cp "${SEED_CONFIG}" "${TARGET_CONFIG}"
fi

PLUGIN_SEED=/opt/openclaw-plugins
PLUGIN_TARGET="${OPENCLAW_DIR}/npm"

if [ -d "${PLUGIN_SEED}" ] && [ ! -d "${PLUGIN_TARGET}" ]; then
    mkdir -p "${PLUGIN_TARGET}"
    cp -r "${PLUGIN_SEED}/." "${PLUGIN_TARGET}/"
fi

RUNTIME_BIN=/usr/local/bin/agenttier-sandbox-runtime
if [ -x "${RUNTIME_BIN}" ]; then
    "${RUNTIME_BIN}" &
fi
exec "$@"
