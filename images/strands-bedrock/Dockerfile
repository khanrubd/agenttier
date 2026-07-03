# AgentTier Strands Agents Sandbox Image
#
# Reference image for Strands Agents (https://strandsagents.com/), the
# AWS-native Python SDK for building model-driven AI agents. Strands
# defaults to the Amazon Bedrock model provider and uses the AWS SDK
# default credential chain — so IRSA-injected credentials Just Work
# without runtime config the way they do for Claude Code and OpenClaw.
#
# What this image is for:
#   * `mode: code` — interactive shell with strands-agents preinstalled
#     so a developer can drop into the sandbox terminal and write/run
#     agent code against Bedrock.
#   * `mode: agent` — drop in your own /workspace/agent.py and call
#     /invoke; the bundled example at /opt/agenttier/examples/agent.py
#     ships ready-to-run.
#
# Why we install Strands at image build time instead of via /configure:
# the same reason langgraph and openclaw do — pip-install on every cold
# start adds 30-60s to the first /invoke and burns network egress
# unnecessarily. A baked image is reproducible, cosign-signable, and
# the standard AgentTier release-discipline shape.
#
# Build context: repository root (so the Dockerfile can pull in
# cmd/sandbox-runtime). release.yml + ci.yml + ci/buildspec.yml all set
# context: . and dockerfile: images/strands-bedrock/Dockerfile.

# ---- Stage 1: in-pod runtime binary ----------------------------------------
# See pkg/sandboxruntime for design rationale.
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

# ---- Stage 2: strands sandbox — digest-pinned for supply-chain security (M5).
# Multi-arch build (linux/amd64, linux/arm64) — TARGETARCH is a buildx ARG.
# Base image is ECR Public to avoid Docker Hub rate-limiting from EKS nodes.
FROM public.ecr.aws/docker/library/python:3.11.13-slim@sha256:9bffe4353b925a1656688797ebc68f9c525e79b1d377a764d232182a519eeec4

ARG TARGETARCH

# System packages: git for checkouts, curl + ca-certs for outbound API
# calls, AWS CLI v2 so operators can run `aws sts get-caller-identity`
# to confirm IRSA wired up correctly before invoking the agent.
# build-essential is intentionally NOT installed — every dep we ship
# has a prebuilt wheel for linux/amd64 + linux/arm64 at the pinned
# versions, so the 350-MB toolchain is dead weight. Re-add when bumping
# a dep introduces a new sdist-only package.
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash git curl unzip ca-certificates tmux jq sudo \
    && rm -rf /var/lib/apt/lists/*

# AWS CLI v2 — select the right bundle per architecture.
RUN case "${TARGETARCH}" in \
      amd64) AWSARCH=x86_64 ;; \
      arm64) AWSARCH=aarch64 ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -s "https://awscli.amazonaws.com/awscli-exe-linux-${AWSARCH}.zip" -o "/tmp/awscliv2.zip" \
    && unzip -q /tmp/awscliv2.zip -d /tmp \
    && /tmp/aws/install \
    && rm -rf /tmp/aws /tmp/awscliv2.zip

# Pin every dep so this image rebuilds reproducibly. Bumping these is a
# deliberate decision (release notes, smoke test) — not an opportunistic
# Dependabot PR.
#
# strands-agents (the SDK) + strands-agents-tools (community tools) +
# boto3 (so a user can import boto3 directly without a second install).
# Split into multiple `pip install` layers so each individual image
# layer stays under ~80 MB.
RUN pip install --no-cache-dir \
        boto3==1.43.16 \
        botocore==1.43.16
RUN pip install --no-cache-dir \
        strands-agents==1.41.0
RUN pip install --no-cache-dir \
        strands-agents-tools==0.6.0

# In-pod runtime binary + entrypoint that backgrounds it.
COPY --from=runtime-build /out/agenttier-sandbox-runtime /usr/local/bin/agenttier-sandbox-runtime
COPY images/strands-bedrock/entrypoint.sh /usr/local/bin/agenttier-entrypoint
RUN chmod 0755 /usr/local/bin/agenttier-sandbox-runtime /usr/local/bin/agenttier-entrypoint

# Standard sandbox user / workspace conventions. AgentTier's
# NetworkPolicy + PVC + FSGroup all assume uid 1000 / gid 1000.
RUN useradd -m -u 1000 -s /bin/bash sandbox \
    && mkdir -p /workspace /opt/agenttier/examples \
    && chown -R sandbox:sandbox /workspace /opt/agenttier \
    && echo "sandbox ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# Sample agent — copied into /opt/agenttier/examples so users can:
#   cp /opt/agenttier/examples/agent.py /workspace/
# and immediately /invoke without writing any code.
COPY --chown=sandbox:sandbox images/strands-bedrock/agent.py /opt/agenttier/examples/agent.py
COPY --chown=sandbox:sandbox images/strands-bedrock/README.md /opt/agenttier/examples/README.md

# Region default — Strands picks this up via boto3's default chain.
# Operators override via the SandboxTemplate `env:` block.
ENV AWS_DEFAULT_REGION=us-east-1
ENV HOME=/workspace

USER sandbox
WORKDIR /workspace

# The container runs `sleep infinity` by default the same way the
# existing reference images do — agent code is invoked on demand via
# /invoke (or run interactively via the web terminal).
ENTRYPOINT ["/usr/local/bin/agenttier-entrypoint"]
CMD ["sleep", "infinity"]
