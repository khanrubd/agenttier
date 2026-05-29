# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Single-rollout evaluator. /invoke-shaped: reads JSON from stdin, runs
one episode of a Gymnasium env using a Stable-Baselines3 checkpoint, and
prints {episode_reward, episode_length} as JSON on stdout.

Request body:
    {
        "env": "CartPole-v1",                                  # gym id
        "checkpoint": "/workspace/.rl-cache/checkpoints/ppo_cartpole",
        "max_steps": 500                                        # optional
    }

If `checkpoint` is missing or doesn't exist, the agent runs a random
policy — useful for smoke-testing /invoke before any training has happened.

Default Sandbox.spec.entrypoint should be:
    ["python", "/opt/agenttier/examples/agent.py"]

Pair with the rl-rollout ClusterSandboxTemplate (default-templates.yaml).
"""

from __future__ import annotations

import json
import os
import pathlib
import sys

import gymnasium as gym


def read_request() -> dict:
    raw = sys.stdin.read()
    if not raw.strip():
        return {}
    try:
        data = json.loads(raw)
        return data if isinstance(data, dict) else {}
    except json.JSONDecodeError:
        return {}


def load_policy(checkpoint: str | None):
    """Return a `predict(obs) -> action` callable.

    When the checkpoint exists we load with Stable-Baselines3 PPO; when it
    doesn't, we fall back to a random policy so /invoke still works on a
    fresh sandbox before any training has happened.
    """
    if checkpoint and pathlib.Path(checkpoint + ".zip").exists():
        from stable_baselines3 import PPO  # heavy import — defer

        model = PPO.load(checkpoint)
        return lambda obs: model.predict(obs, deterministic=True)[0]

    # Random fallback. action_space.sample() ignores the obs.
    return None


def main() -> int:
    req = read_request()
    env_id = req.get("env", "CartPole-v1")
    checkpoint = req.get("checkpoint", "/workspace/.rl-cache/checkpoints/ppo_cartpole")
    max_steps = int(req.get("max_steps", 500))

    env = gym.make(env_id)
    policy = load_policy(checkpoint)

    obs, _ = env.reset(seed=0)
    total_reward = 0.0
    steps = 0
    for _ in range(max_steps):
        action = env.action_space.sample() if policy is None else policy(obs)
        obs, reward, terminated, truncated, _ = env.step(action)
        total_reward += float(reward)
        steps += 1
        if terminated or truncated:
            break

    print(
        json.dumps(
            {
                "env": env_id,
                "episode_reward": total_reward,
                "episode_length": steps,
                "policy": "random" if policy is None else "ppo-checkpoint",
                "checkpoint_loaded": policy is not None,
            }
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
