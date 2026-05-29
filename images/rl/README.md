# sandbox-rl

Reference image for reinforcement-learning workloads on AgentTier.

## What's preinstalled

| Package | Version | Why |
|---|---|---|
| `torch` (CPU) | 2.5.1 | DL backend for both Ray RLlib and SB3 |
| `ray[default,rllib,tune]` | 2.40.0 | distributed RL trainer + tuner |
| `gymnasium` | 1.0.0 | the open-RL environment standard |
| `stable-baselines3` | 2.4.0 | dropin-easy RL algorithms |
| `numpy` / `scipy` / `pandas` | 2.1.3 / 1.14.1 / 2.2.3 | numerics |
| `matplotlib` / `tensorboard` | 3.9.2 / 2.18.0 | quick visualization |

CPU-only by design. GPU support arrives in a future phase along with
GPU resource overrides on the Sandbox CRD.

## Examples

Two ready-to-run scripts live at `/opt/agenttier/examples/`:

- `train.py` — self-contained PPO loop on CartPole-v1. Runs end-to-end
  in <2 minutes on 4 CPU cores and writes a checkpoint to
  `/workspace/.rl-cache/checkpoints/`. Ideal for verifying a fresh
  sandbox is healthy.
- `agent.py` — `/invoke`-shaped wrapper. Reads JSON on stdin, runs one
  episode of the named Gym env using an SB3 checkpoint, prints
  `{episode_reward, episode_length}` JSON on stdout. Falls back to a
  random policy when no checkpoint is present so `/invoke` works on a
  fresh sandbox before training.

Drop your own `agent.py` into `/workspace/` and AgentTier picks it up
on the next `/invoke`.

## Canonical patterns

### Pattern A — RL rollout worker driven by an external Ray head

Spin up N sandboxes, each with `entrypoint:
["python", "/workspace/agent.py"]`. The Ray head node lives outside
AgentTier; for each rollout it `POST /invoke`s a sandbox with
hyperparameters / curriculum updates on stdin and reads back the
episode reward.

Per-sandbox PVC means each worker has durable replay-buffer storage
that survives stop/resume — important for long curriculum runs.

### Pattern B — self-contained PPO loop inside one sandbox

A single sandbox runs `train.py` (or your variant) under the
sandbox-runtime, periodically checkpointing to
`/workspace/.rl-cache/checkpoints/`. The PVC is the durable training
state. NetworkPolicy keeps the trainer offline-only or limited to a
specific experiment-tracking endpoint.

### Pattern C — RL-driven autonomous agent

A long-running agent uses RL inside the sandbox to optimize its own
behavior over time, taking observations from a sandboxed task
environment (file system, headless browser, scripted API). The PVC
is the agent's growing memory + checkpoint store.

## Cache and persistence

Everything that should survive stop/resume goes under `/workspace/`.
The image points the standard PyTorch / Ray cache vars at
`/workspace/.rl-cache`:

```
XDG_CACHE_HOME=/workspace/.rl-cache
TORCH_HOME=/workspace/.rl-cache/torch
RAY_TMPDIR=/workspace/.rl-cache/ray-tmp
```

## CPU thread limits

`OMP_NUM_THREADS=2` and `MKL_NUM_THREADS=2` are set so a single
sandbox doesn't accidentally saturate the node when scheduled
alongside other RL workers. Override per-sandbox via
`spec.env` if you want more threads.

## Limits

- CPU-only. GPU rollouts are a future phase.
- Image size is ~3 GB compressed (PyTorch + Ray + SB3 each carry their
  own native libs). Expect ~30s pulls on a fresh node before the
  warm-pool hides it.
- Some SB3 algorithms (HER, RecurrentPPO from sb3-contrib) are not
  preinstalled — `pip install sb3-contrib` at `/configure` time if
  you need them.

## Versioning

Pinned for reproducibility. Bumping any of these is a deliberate
release decision; do not opportunistically Dependabot them.
