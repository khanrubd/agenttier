# AgentTier LangGraph Sandbox Image — Phase 10 reference image for mode: agent.
#
# Bundles Python 3.11 + LangGraph + LangChain + httpx + the mem0 client
# preinstalled so a user can drop an agent.py into /workspace and call
# /invoke without first running pip via /configure.
#
# Multi-arch build (linux/amd64, linux/arm64) — TARGETARCH is a buildx ARG.
# Base image is ECR Public to avoid Docker Hub rate-limiting from EKS nodes.
FROM public.ecr.aws/docker/library/python:3.11-slim

ARG TARGETARCH

# System packages: git for checkouts, curl + ca-certs for outbound API calls,
# build-essential because some agent libs ship sdist-only wheels for arm64.
RUN apt-get update && apt-get install -y --no-install-recommends \
        git \
        curl \
        ca-certificates \
        build-essential \
    && rm -rf /var/lib/apt/lists/*

# Pin every dep so this image rebuilds reproducibly. Bumping these is a
# deliberate decision (release notes, smoke test) — not an opportunistic
# Dependabot PR.
RUN pip install --no-cache-dir \
        langgraph==0.6.4 \
        langchain==0.3.27 \
        langchain-core==0.3.78 \
        httpx==0.28.1 \
        mem0ai==0.1.115 \
        pydantic==2.10.4

# Standard sandbox user / workspace conventions. AgentTier's NetworkPolicy +
# PVC + FSGroup all assume uid 1000 / gid 1000.
RUN useradd -m -u 1000 -s /bin/bash sandbox \
    && mkdir -p /workspace /opt/agenttier/examples \
    && chown -R sandbox:sandbox /workspace /opt/agenttier

# Sample agent — copied into /opt/agenttier/examples so users can:
#   cp /opt/agenttier/examples/agent.py /workspace/
# and immediately /invoke without writing any code.
COPY --chown=sandbox:sandbox agent.py /opt/agenttier/examples/agent.py
COPY --chown=sandbox:sandbox README.md /opt/agenttier/examples/README.md

USER sandbox
WORKDIR /workspace

# The container runs `sleep infinity` by default the same way the existing
# code-mode reference images do — agent code is invoked on demand via
# /invoke, not at container start.
CMD ["sleep", "infinity"]
