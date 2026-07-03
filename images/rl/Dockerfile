# AgentTier sandbox-rl — reinforcement-learning reference image.
#
# Bundles a CPU-only PyTorch + Ray RLlib + Gymnasium + Stable-Baselines3
# stack so an enterprise running RL rollouts (RLHF / RLAIF / GRPO worker,
# self-improving agents, RL-on-tool-use loops) can stand up isolated
# rollout workers in seconds without rebuilding the base image.
#
# Why ship this as a reference: AgentTier's per-sandbox PVC + NetworkPolicy
# + governance + warm-pool + cleanup story is the exact same story RL
# workloads need. Rather than asking enterprises to roll their own image
# every time, we ship one canonical `rl-rollout` template they can `kubectl
# apply` straight into.
#
# CPU-only by design. GPU rollouts plug in once a future phase lands GPU
# resource overrides on the CRD. Pinned versions throughout — bumping is a
# deliberate decision (release notes, smoke test) rather than an
# opportunistic Dependabot PR.
#
# Build context: repo root (the runtime binary stage pulls cmd/sandbox-runtime
# and pkg/sandboxruntime). buildspec.yml + release.yml set context: . and
# dockerfile: images/rl/Dockerfile.

# ---- Stage 1: in-pod runtime binary ----------------------------------------
# Digest-pinned for supply-chain security (M5).
FROM public.ecr.aws/docker/library/golang:1.25.11-alpine@sha256:523c3effe300580ed375e43f43b1c9b091b68e935a7c3a92bfcc4e7ed55b18c2 AS runtime-build
RUN apk add --no-cache git ca-certificates
ARG TARGETARCH
# GOPROXY=direct bypasses proxy.golang.org on networks where it's blocked
# (corporate VPN / captive networks). Override at build time with
# --build-arg GOPROXY=https://proxy.golang.org,direct on a normal network.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
# GOTOOLCHAIN=local: never auto-download a toolchain; build with the image's
# Go (matches go.mod toolchain go1.25.11) so builds stay hermetic/offline (M5).
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/sandbox-runtime ./cmd/sandbox-runtime
COPY pkg/sandboxruntime ./pkg/sandboxruntime
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w' \
    -o /out/agenttier-sandbox-runtime ./cmd/sandbox-runtime

# ---- Stage 2: RL sandbox — digest-pinned for supply-chain security (M5).
# Multi-arch build (linux/amd64, linux/arm64). PyTorch ships official CPU
# wheels for both arches at the pinned version; Ray ships wheels for both
# arches via PyPI + nightly wheel hosts. Base image is ECR Public to dodge
# Docker Hub rate-limits on EKS nodes.
FROM public.ecr.aws/docker/library/python:3.11.13-slim@sha256:9bffe4353b925a1656688797ebc68f9c525e79b1d377a764d232182a519eeec4

ARG TARGETARCH

# System packages: build-essential is required at runtime — Ray's rllib
# imports compile a couple of small Cython modules on first use, and
# stable-baselines3 occasionally builds C extensions when an env exposes
# its observation space dynamically. git for cloning experiment repos at
# /configure time. tmux for the terminal harness. curl + ca-certs for
# outbound model-server / replay-buffer calls.
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        git \
        curl \
        ca-certificates \
        tmux \
        libgomp1 \
    && rm -rf /var/lib/apt/lists/*

# Pin numpy + scipy first — they're transitive deps of every other package
# and pinning here avoids "no compatible wheel" surprises during the deeper
# layers. Numpy 2.x is required by Ray 2.40+ and torch 2.5+.
RUN pip install --no-cache-dir \
        "numpy==2.1.3" \
        "scipy==1.14.1" \
        "pandas==2.2.3"

# PyTorch CPU build. The +cpu wheels live on download.pytorch.org/whl/cpu,
# not PyPI. We pin to a single torch version with both linux/amd64 and
# linux/arm64 wheels available. 2.5.1 is the latest with arm64 CPU support.
RUN pip install --no-cache-dir \
        --index-url https://download.pytorch.org/whl/cpu \
        "torch==2.5.1"

# Ray + RLlib. Ray 2.40+ requires Python 3.11+ (we have 3.11) and pulls
# its own consistent set of pyarrow / aiohttp / grpcio. Installing rllib
# explicitly drags in the rllib-specific extras (gymnasium adapter,
# tune dashboards, etc).
RUN pip install --no-cache-dir \
        "ray[default,rllib,tune]==2.40.0"

# Gymnasium standalone — RLlib pins it but operators often pin a different
# Gymnasium version in their experiment requirements.txt and we want a
# baseline that imports cleanly. Stable-Baselines3 is the second
# RL framework most enterprises run; ship both so operators can pick.
RUN pip install --no-cache-dir \
        "gymnasium==1.0.0" \
        "stable-baselines3==2.4.0"

# Visualization + experiment tracking — small wins for a reference image
# that's expected to be poked interactively.
RUN pip install --no-cache-dir \
        "matplotlib==3.9.2" \
        "tensorboard==2.18.0"

# In-pod runtime binary + entrypoint that backgrounds it.
COPY --from=runtime-build /out/agenttier-sandbox-runtime /usr/local/bin/agenttier-sandbox-runtime
COPY images/rl/entrypoint.sh /usr/local/bin/agenttier-entrypoint
RUN chmod 0755 /usr/local/bin/agenttier-sandbox-runtime /usr/local/bin/agenttier-entrypoint

# Standard sandbox user / workspace conventions. AgentTier's NetworkPolicy +
# PVC + FSGroup all assume uid 1000 / gid 1000.
RUN useradd -m -u 1000 -s /bin/bash sandbox \
    && mkdir -p /workspace /opt/agenttier/examples \
    && chown -R sandbox:sandbox /workspace /opt/agenttier

# Ship two ready-to-run examples + a README that explains the canonical
# patterns:
#   train.py — self-contained PPO loop on CartPole-v1, runs end-to-end
#              inside the sandbox without any external dependencies.
#   agent.py — a /invoke-shaped wrapper that loads a checkpoint and
#              evaluates a single rollout against a Gym env, taking the
#              env name + checkpoint path as JSON on stdin.
COPY --chown=sandbox:sandbox images/rl/train.py /opt/agenttier/examples/train.py
COPY --chown=sandbox:sandbox images/rl/agent.py /opt/agenttier/examples/agent.py
COPY --chown=sandbox:sandbox images/rl/README.md /opt/agenttier/examples/README.md

# Pre-create the model + checkpoint directories the examples write to,
# under the sandbox's writable workspace so they survive stop/resume.
RUN mkdir -p /workspace/.rl-cache \
    && chown sandbox:sandbox /workspace/.rl-cache

USER sandbox
WORKDIR /workspace

# RL frameworks default to writing their cache (Ray's session logs, torch
# hub) to $HOME/.cache and $HOME/.ray. Sandbox $HOME is /home/sandbox which
# is rootfs (RO) at runtime. Re-point to /workspace/.rl-cache so cache
# writes land on the writable PVC and persist across stop/resume.
ENV HOME=/home/sandbox \
    XDG_CACHE_HOME=/workspace/.rl-cache \
    TORCH_HOME=/workspace/.rl-cache/torch \
    RAY_TMPDIR=/workspace/.rl-cache/ray-tmp \
    OMP_NUM_THREADS=2 \
    MKL_NUM_THREADS=2

# Container runs `sleep infinity` by default; rl code is invoked on demand
# via /invoke or interactively via the terminal. Same shape as every other
# reference image so AgentTier's lifecycle controller stays uniform.
ENTRYPOINT ["/usr/local/bin/agenttier-entrypoint"]
CMD ["sleep", "infinity"]
