# AgentTier LangGraph Sandbox Image — Phase 10 reference image for mode: agent.
#
# Bundles Python 3.11 + LangGraph + LangChain + httpx + the mem0 client
# preinstalled so a user can drop an agent.py into /workspace and call
# /invoke without first running pip via /configure.
#
# Build context: repository root (for cmd/sandbox-runtime). release.yml +
# ci.yml set context: . and dockerfile: images/langgraph/Dockerfile.

# ---- Stage 1: in-pod runtime binary ----------------------------------------
FROM public.ecr.aws/docker/library/golang:1.22 AS runtime-build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/sandbox-runtime ./cmd/sandbox-runtime
COPY pkg/sandboxruntime ./pkg/sandboxruntime
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w' \
    -o /out/agenttier-sandbox-runtime ./cmd/sandbox-runtime

# ---- Stage 2: langgraph sandbox --------------------------------------------
# Multi-arch build (linux/amd64, linux/arm64) — TARGETARCH is a buildx ARG.
# Base image is ECR Public to avoid Docker Hub rate-limiting from EKS nodes.
FROM public.ecr.aws/docker/library/python:3.11-slim

ARG TARGETARCH

# System packages: git for checkouts, curl + ca-certs for outbound API calls.
# build-essential is intentionally NOT installed — every dep we ship has a
# prebuilt wheel for linux/amd64 + linux/arm64 at the pinned versions, so the
# 350-MB toolchain is dead weight. Re-add when bumping a dep introduces a new
# sdist-only package.
RUN apt-get update && apt-get install -y --no-install-recommends \
        git \
        curl \
        ca-certificates \
        tmux \
    && rm -rf /var/lib/apt/lists/*

# Pin every dep so this image rebuilds reproducibly. Bumping these is a
# deliberate decision (release notes, smoke test) — not an opportunistic
# Dependabot PR.
#
# Split into multiple `pip install` layers so each individual image layer
# stays under ~80 MB. Single 200-MB+ layers occasionally hit ECR push
# timeouts on slower / proxied connections; multiple smaller layers retry
# more reliably.
RUN pip install --no-cache-dir \
        pydantic==2.10.4 \
        httpx==0.28.1
RUN pip install --no-cache-dir \
        langchain-core==0.3.78 \
        langchain==0.3.27
RUN pip install --no-cache-dir \
        langgraph==0.6.4
RUN pip install --no-cache-dir \
        mem0ai==0.1.115

# In-pod runtime binary + entrypoint that backgrounds it.
COPY --from=runtime-build /out/agenttier-sandbox-runtime /usr/local/bin/agenttier-sandbox-runtime
COPY images/langgraph/entrypoint.sh /usr/local/bin/agenttier-entrypoint
RUN chmod 0755 /usr/local/bin/agenttier-sandbox-runtime /usr/local/bin/agenttier-entrypoint

# Standard sandbox user / workspace conventions. AgentTier's NetworkPolicy +
# PVC + FSGroup all assume uid 1000 / gid 1000.
RUN useradd -m -u 1000 -s /bin/bash sandbox \
    && mkdir -p /workspace /opt/agenttier/examples \
    && chown -R sandbox:sandbox /workspace /opt/agenttier

# Sample agent — copied into /opt/agenttier/examples so users can:
#   cp /opt/agenttier/examples/agent.py /workspace/
# and immediately /invoke without writing any code.
COPY --chown=sandbox:sandbox images/langgraph/agent.py /opt/agenttier/examples/agent.py
COPY --chown=sandbox:sandbox images/langgraph/README.md /opt/agenttier/examples/README.md

USER sandbox
WORKDIR /workspace

# The container runs `sleep infinity` by default the same way the existing
# code-mode reference images do — agent code is invoked on demand via
# /invoke, not at container start.
ENTRYPOINT ["/usr/local/bin/agenttier-entrypoint"]
CMD ["sleep", "infinity"]
