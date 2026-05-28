# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Minimal Strands Agents script that AgentTier's /invoke endpoint can drive.

Reads a prompt from stdin (raw text or a JSON object with a `prompt`
field), runs a Strands Agent against the default Bedrock model, and
prints the response on stdout.

The Strands SDK uses the AWS SDK default credential chain by default,
so an EKS sandbox that has IRSA wired up (annotate the ServiceAccount
with `eks.amazonaws.com/role-arn=...`) needs no extra config. Bedrock
model access is required on the IAM role (`bedrock:InvokeModel`,
`bedrock:InvokeModelWithResponseStream`).

Replace the agent setup or the prompt processing with whatever real
agent you want — the rest of the harness (stdin → invoke → stdout)
stays the same.
"""

from __future__ import annotations

import json
import sys

from strands import Agent


def read_input() -> str:
    """Pull the prompt from stdin. AgentTier feeds the request body or
    ?prompt=... in via stdin — accept either a raw string or a JSON
    object with a `prompt` field."""
    raw = sys.stdin.read()
    if not raw:
        return ""
    try:
        data = json.loads(raw)
        if isinstance(data, dict) and "prompt" in data:
            return str(data["prompt"])
        return raw
    except json.JSONDecodeError:
        return raw


def main() -> int:
    prompt = read_input() or "Say hello in three words."
    # Default model is Bedrock Claude Sonnet (set by the Strands SDK).
    # Operators who want a different model build their own Agent with
    # an explicit BedrockModel(model_id=...) instance — see
    # https://strandsagents.com/docs/user-guide/concepts/model-providers/amazon-bedrock/
    #
    # callback_handler=None disables Strands' default streaming printer,
    # which would otherwise duplicate the response on stdout (it streams
    # tokens as they arrive, then we'd print(str(result)) the same text
    # again at the end).
    agent = Agent(callback_handler=None)
    result = agent(prompt)
    # AgentResult stringifies to the assistant's text response.
    print(str(result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
