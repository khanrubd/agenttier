# AgentTier Claude Code Sandbox Image
# AI coding environment with Claude Code CLI and AWS Bedrock access.
# Credentials are injected via IRSA (IAM Roles for Service Accounts).
# Builds for linux/amd64 and linux/arm64 via buildx TARGETARCH.
#
# NOTE: Pinned to v2.1.81 because v2.1.83+ has a known regression where
# -p (print) mode produces zero output (github.com/anthropics/claude-code/issues/38760).
#
# Build context: repository root (so the Dockerfile can pull in
# cmd/sandbox-runtime). release.yml + ci.yml set context: . and dockerfile:
# images/claude-code/Dockerfile for this reason.

# ---- Stage 1: in-pod runtime binary ----------------------------------------
# See pkg/sandboxruntime for design rationale.
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

# ---- Stage 2: claude-code sandbox ------------------------------------------
FROM public.ecr.aws/docker/library/node:20-bookworm

ARG TARGETARCH

# System dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash curl git unzip python3 python3-pip ca-certificates openssh-client sudo tmux \
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

# The node:20 image has user "node" at UID 1000. Rename to "sandbox".
RUN usermod -l sandbox -d /home/sandbox -m node \
    && groupmod -n sandbox node \
    && echo "sandbox ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# Install Claude Code CLI (pinned to 2.1.81 — see NOTE above)
RUN npm install -g @anthropic-ai/claude-code@2.1.81

# In-pod runtime binary + entrypoint that backgrounds it.
COPY --from=runtime-build /out/agenttier-sandbox-runtime /usr/local/bin/agenttier-sandbox-runtime
COPY images/claude-code/entrypoint.sh /usr/local/bin/agenttier-entrypoint
RUN chmod 0755 /usr/local/bin/agenttier-sandbox-runtime /usr/local/bin/agenttier-entrypoint

# Pre-create workspace
RUN mkdir -p /workspace && chown sandbox:sandbox /workspace

# Configure environment for Bedrock
ENV CLAUDE_CODE_USE_BEDROCK=1
ENV AWS_DEFAULT_REGION=us-east-1
ENV HOME=/workspace

USER sandbox
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/agenttier-entrypoint"]
CMD ["sleep", "infinity"]
