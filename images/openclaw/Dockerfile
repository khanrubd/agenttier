# AgentTier OpenClaw Sandbox Image
#
# AI coding environment with the OpenClaw CLI (https://github.com/openclaw/openclaw)
# preinstalled, configured to talk to AWS Bedrock via IRSA-injected credentials.
#
# Why OpenClaw needs Node 22 (and we ship Node 22 here, vs Node 20 in
# sandbox-claude-code): OpenClaw's runtime makes heavy use of features
# introduced in Node 22.16 — older Node versions hit unpredictable
# `SyntaxError`s in the Gateway boot path. Node 24 is the upstream
# recommendation; we run on Node 22 to keep the Debian Bookworm package
# story simple while still meeting the floor.
#
# Why we bake a baseline OpenClaw config (vs running `openclaw onboard`
# at runtime): the onboarding wizard is interactive and assumes an
# operator at a TTY. For a hands-free sandbox we ship a JSON config that
# pre-enables the Bedrock provider + discovery, then the entrypoint
# copies it to the writable PVC on first launch (so per-sandbox
# customizations persist across stop/resume).
#
# Why we explicitly set `plugins.entries.amazon-bedrock.config.discovery.enabled = true`:
# OpenClaw's auto-detection only activates the implicit Bedrock provider
# when it sees one of these env markers: AWS_BEARER_TOKEN_BEDROCK,
# AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY, or AWS_PROFILE. IRSA sets
# AWS_ROLE_ARN + AWS_WEB_IDENTITY_TOKEN_FILE, neither of which is on
# OpenClaw's marker list, so without `discovery.enabled = true` the
# provider stays dormant and `openclaw models list` returns nothing.
# The actual runtime auth path still uses the AWS SDK default chain,
# which picks up IRSA via web identity automatically.
#
# Builds for linux/amd64 and linux/arm64 via buildx TARGETARCH.
#
# Build context: repository root (so the Dockerfile can pull in
# cmd/sandbox-runtime). release.yml + ci.yml + buildspec.yml all set
# context: . and dockerfile: images/openclaw/Dockerfile for this reason.

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

# ---- Stage 2: openclaw sandbox ---------------------------------------------
FROM public.ecr.aws/docker/library/node:22-bookworm

ARG TARGETARCH

# System dependencies — same shell + git + tmux toolkit operators expect
# from the other coding sandboxes. ca-certificates for outbound HTTPS
# (Bedrock Converse, model discovery, etc.).
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash curl git unzip python3 python3-pip ca-certificates openssh-client sudo tmux jq \
    && rm -rf /var/lib/apt/lists/*

# AWS CLI v2 — operators on the sandbox often want `aws sts get-caller-identity`
# and friends to confirm IRSA wired up correctly before invoking openclaw.
# Select the right bundle per architecture.
RUN case "${TARGETARCH}" in \
      amd64) AWSARCH=x86_64 ;; \
      arm64) AWSARCH=aarch64 ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -s "https://awscli.amazonaws.com/awscli-exe-linux-${AWSARCH}.zip" -o "/tmp/awscliv2.zip" \
    && unzip -q /tmp/awscliv2.zip -d /tmp \
    && /tmp/aws/install \
    && rm -rf /tmp/aws /tmp/awscliv2.zip

# The node:22 image has user "node" at UID 1000. Rename to "sandbox" so
# the file ownership / NetworkPolicy / FSGroup story matches every other
# AgentTier reference image.
RUN usermod -l sandbox -d /home/sandbox -m node \
    && groupmod -n sandbox node \
    && echo "sandbox ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# Install OpenClaw CLI globally. Pin to a specific version so the image
# rebuilds reproducibly and the auto-update path can never silently flip
# the runtime under a sandbox. Bumping this is a deliberate decision
# tied to a release cut, same as @anthropic-ai/claude-code in
# images/claude-code/Dockerfile.
#
# 2026.5.19 = latest May 2026 stable on the public npm registry
# (date-versioned upstream, requires Node >=22.19.0). Bump the pin in
# lockstep with smoke-tested upgrades.
RUN npm install -g openclaw@2026.5.19

# Pre-install the amazon-bedrock provider plugin into a staging directory.
# OpenClaw stores plugins under the user's home (writable PVC), not in
# the global npm prefix. The entrypoint copies this staging tree to
# /workspace/.openclaw/npm on first launch so a fresh sandbox can
# reach Bedrock without a runtime `openclaw plugins install` call.
#
# We use a temporary HOME so `openclaw plugins install` lays the
# plugin out in the directory layout OpenClaw expects, then copy it
# to /opt/openclaw-plugins for the entrypoint to seed onto the PVC.
RUN mkdir -p /tmp/openclaw-plugin-build/.openclaw \
    && HOME=/tmp/openclaw-plugin-build openclaw plugins install @openclaw/amazon-bedrock-provider \
    && mkdir -p /opt/openclaw-plugins \
    && cp -r /tmp/openclaw-plugin-build/.openclaw/npm/. /opt/openclaw-plugins/ \
    && chmod -R a+rX /opt/openclaw-plugins \
    && rm -rf /tmp/openclaw-plugin-build

# Baseline OpenClaw config + entrypoint that seeds it to /workspace.
# The seed path lets per-sandbox customizations (additional providers,
# auth profiles, model overrides) persist on the PVC while the image
# stays deterministic.
COPY images/openclaw/openclaw-config.json /etc/openclaw/openclaw.json
COPY --from=runtime-build /out/agenttier-sandbox-runtime /usr/local/bin/agenttier-sandbox-runtime
COPY images/openclaw/entrypoint.sh /usr/local/bin/agenttier-entrypoint
RUN chmod 0755 /usr/local/bin/agenttier-sandbox-runtime /usr/local/bin/agenttier-entrypoint \
    && chmod 0644 /etc/openclaw/openclaw.json

# Pre-create workspace
RUN mkdir -p /workspace && chown sandbox:sandbox /workspace

# Configure environment for Bedrock. AWS_DEFAULT_REGION can be overridden
# by the SandboxTemplate `env:` block; the defaults here match the
# default-templates.yaml `openclaw-bedrock` template's env so an operator
# who builds straight from the image without a template still gets a
# working setup.
#
# HOME=/workspace mirrors the claude-code reference image so any
# OpenClaw state (auth profiles, custom models, gateway config) lands on
# the writable PVC and survives stop/resume.
ENV AWS_DEFAULT_REGION=us-east-1
ENV HOME=/workspace

# Disable telemetry + version-check pings — good hygiene for hardened
# sandboxes with deny-by-default egress.
ENV OPENCLAW_TELEMETRY_DISABLED=1
ENV OPENCLAW_DISABLE_AUTOUPDATE=1

USER sandbox
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/agenttier-entrypoint"]
CMD ["sleep", "infinity"]
