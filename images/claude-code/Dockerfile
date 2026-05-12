# AgentTier Claude Code Sandbox Image
# AI coding environment with Claude Code CLI and AWS Bedrock access.
# Credentials are injected via IRSA (IAM Roles for Service Accounts).
# Builds for linux/amd64 and linux/arm64 via buildx TARGETARCH.
#
# NOTE: Pinned to v2.1.81 because v2.1.83+ has a known regression where
# -p (print) mode produces zero output (github.com/anthropics/claude-code/issues/38760).

FROM public.ecr.aws/docker/library/node:20-bookworm

ARG TARGETARCH

# System dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash curl git unzip python3 python3-pip ca-certificates openssh-client sudo \
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

# Pre-create workspace
RUN mkdir -p /workspace && chown sandbox:sandbox /workspace

# Configure environment for Bedrock
ENV CLAUDE_CODE_USE_BEDROCK=1
ENV AWS_DEFAULT_REGION=us-east-1
ENV HOME=/workspace

USER sandbox
WORKDIR /workspace

CMD ["sleep", "infinity"]
