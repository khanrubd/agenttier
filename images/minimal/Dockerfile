# AgentTier Minimal Sandbox Image
# Minimal shell environment with bash, git, and curl.
#
# Build context: repository root (for cmd/sandbox-runtime).

# ---- Stage 1: in-pod runtime binary — digest-pinned for supply-chain security (M5). ----
FROM public.ecr.aws/docker/library/golang:1.25.11-alpine@sha256:523c3effe300580ed375e43f43b1c9b091b68e935a7c3a92bfcc4e7ed55b18c2 AS runtime-build
ARG TARGETARCH
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

# ---- Stage 2: minimal sandbox — digest-pinned for supply-chain security (M5).
FROM public.ecr.aws/docker/library/alpine:3.20.6@sha256:de4fe7064d8f98419ea6b49190df1abbf43450c1702eeb864fe9ced453c1cc5f

RUN apk add --no-cache \
    bash \
    git \
    curl \
    openssh-client \
    ca-certificates \
    tmux \
    && rm -rf /var/cache/apk/*

# In-pod runtime binary + entrypoint that backgrounds it.
COPY --from=runtime-build /out/agenttier-sandbox-runtime /usr/local/bin/agenttier-sandbox-runtime
COPY images/minimal/entrypoint.sh /usr/local/bin/agenttier-entrypoint
RUN chmod 0755 /usr/local/bin/agenttier-sandbox-runtime /usr/local/bin/agenttier-entrypoint

# Create non-root user (UID 1000 matches SecurityContext)
RUN adduser -D -u 1000 -h /workspace sandbox

# Create workspace directory
RUN mkdir -p /workspace && chown sandbox:sandbox /workspace

USER sandbox
WORKDIR /workspace

# Keep container running for exec access
ENTRYPOINT ["/usr/local/bin/agenttier-entrypoint"]
CMD ["sleep", "infinity"]
