# AgentTier Minimal Sandbox Image
# Minimal shell environment with bash, git, and curl.

FROM public.ecr.aws/docker/library/alpine:3.20

RUN apk add --no-cache \
    bash \
    git \
    curl \
    openssh-client \
    ca-certificates \
    && rm -rf /var/cache/apk/*

# Create non-root user (UID 1000 matches SecurityContext)
RUN adduser -D -u 1000 -h /workspace sandbox

# Create workspace directory
RUN mkdir -p /workspace && chown sandbox:sandbox /workspace

USER sandbox
WORKDIR /workspace

# Keep container running for exec access
CMD ["sleep", "infinity"]
