# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Minimal end-to-end PPO training loop on CartPole-v1.

Self-contained: imports nothing from the network, runs in <2 minutes on
4 CPU cores, writes a checkpoint to /workspace/.rl-cache/checkpoints/.
Ideal for verifying a fresh sandbox-rl image works as expected.

Run interactively from the sandbox terminal:

    python /opt/agenttier/examples/train.py

Or via /invoke (the entrypoint reads no stdin so you can pass an empty
body):

    curl -X POST .../sandboxes/<id>/invoke -d '{}'
"""

from __future__ import annotations

import os
import pathlib

import gymnasium as gym
from stable_baselines3 import PPO


def main() -> int:
    cache = pathlib.Path(os.environ.get("XDG_CACHE_HOME", "/workspace/.rl-cache"))
    out = cache / "checkpoints" / "ppo_cartpole"
    out.parent.mkdir(parents=True, exist_ok=True)

    env = gym.make("CartPole-v1")
    model = PPO("MlpPolicy", env, verbose=1, n_steps=512)
    model.learn(total_timesteps=10_000)
    model.save(str(out))
    print(f"checkpoint saved: {out}.zip")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
