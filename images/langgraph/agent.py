# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Minimal LangGraph agent that AgentTier's /invoke endpoint can drive.

Reads JSON or a single-shot prompt from stdin, runs a tiny LangGraph that
echoes the input through a model-free node, and prints the result on stdout.
Replace the build_graph() body with whatever real graph you want — the rest
of the harness stays the same.

The script is deliberately model-free so users can /invoke immediately
without provisioning Bedrock / OpenAI credentials. Wire in your model
provider client (boto3 + Bedrock, openai, etc.) inside agent_node().
"""

from __future__ import annotations

import json
import sys
from typing import TypedDict

from langgraph.graph import END, START, StateGraph


class State(TypedDict):
    """Graph state — minimal example. Add fields as your agent grows."""

    input: str
    output: str


def agent_node(state: State) -> State:
    """The model-free node. Replace the body to call Bedrock / OpenAI / etc."""
    state["output"] = f"echo: {state['input']}"
    return state


def build_graph() -> StateGraph:
    g = StateGraph(State)
    g.add_node("agent", agent_node)
    g.add_edge(START, "agent")
    g.add_edge("agent", END)
    return g.compile()


def read_input() -> str:
    """Pull the prompt from stdin. AgentTier feeds the request body or
    ?prompt=... in via stdin — accept either a raw string or a JSON object
    with a `prompt` field."""
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
    prompt = read_input()
    graph = build_graph()
    result = graph.invoke({"input": prompt, "output": ""})
    print(result["output"])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
