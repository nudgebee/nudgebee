# syntax=docker/dockerfile:1.7
# --- Build Stage ---
# Use the official Go image to build the application
FROM golang:1.26.5-alpine AS builder

# Use the builder image's bundled Go toolchain, never an auto-downloaded one.
# Pins the compile to the base's Go and gives the build layer a distinct cache
# key so a stale toolchain can't slip back in via edge.yaml's registry cache.
ENV GOTOOLCHAIN=local

# Set the working directory
WORKDIR /app

# Copy the go.mod and go.sum files to download dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy the rest of the application source code
COPY . .

# Build the application. CGO_ENABLED=0 is important for static linking.
# -o /server builds the binary and places it at the root.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /server ./cmd

# --- PowerShell Stage ---
# Install PowerShell LTS from the official GitHub release tarball instead of
# copying from mcr.microsoft.com/powershell. Microsoft's container images are
# frozen (the alpine 7.4 line stopped at 7.4.6 / .NET 8.0.10 and carries 6 HIGH
# + 1 MEDIUM fixable .NET CVEs — CVE-2025-21172/-21176/-30399/-55248,
# CVE-2026-26171/-33116, CVE-2024-38095 — that no published image tag clears),
# while the GitHub release channel is serviced monthly: 7.4.17 bundles the
# patched .NET 8 servicing (Crypto.Xml 8.0.3, Asn1 8.0.2) and its extracted
# tree scans 0 fixable C/H/M. Same LTS major we already ran, so runbook
# behaviour is unchanged. The tarball is pinned by version + sha256 (verified
# against the hash published in the GitHub release notes); curl retries cover
# the transient-download concern that originally motivated the image copy.
FROM alpine:3.22 AS powershell
ARG PWSH_VERSION=7.4.17
ARG PWSH_SHA256=143a1de65ea320c36a0b4bd1808fe65561e5ab12fd66d5f63f78b0b3d66b4397
RUN apk add --no-cache curl \
    && curl -fsSL --retry 5 --retry-delay 5 --retry-all-errors \
      "https://github.com/PowerShell/PowerShell/releases/download/v${PWSH_VERSION}/powershell-${PWSH_VERSION}-linux-musl-x64.tar.gz" \
      -o /tmp/powershell.tar.gz \
    && echo "${PWSH_SHA256}  /tmp/powershell.tar.gz" | sha256sum -c - \
    && mkdir -p /opt/microsoft/powershell/7 \
    && tar -xzf /tmp/powershell.tar.gz -C /opt/microsoft/powershell/7 \
    && chmod +x /opt/microsoft/powershell/7/pwsh \
    && rm /tmp/powershell.tar.gz

# --- Final Stage ---
# Minimal runtime. Previously this stage was `golang:1.26-alpine`, which shipped
# the entire Go SDK (compiler, stdlib sources) in the runtime image — a large,
# needless attack surface. A plain alpine base carries only what the service
# actually needs at runtime.
FROM alpine:3.22

# Install runtime dependencies:
# - iputils: Provides standard 'ping'
# - ca-certificates: Required for SSL/TLS checks and HTTPS requests
# - traceroute: For path analysis
# - nodejs: For local script execution (javascript language)
# - icu-libs, lttng-ust: Required by PowerShell runtime
# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's
# cache key so BuildKit re-runs the apk install and pulls current Alpine security
# patches instead of a stale cached layer. Committed (not a build-arg) so each
# refresh is a reviewable, revertable diff; bump when the Trivy scan flags a stale
# package (e.g. c-ares CVE-2026-33630).
ARG OS_PKG_EPOCH=2026-W28
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && apk upgrade --no-cache \
    && apk add --no-cache iputils ca-certificates traceroute nodejs \
    icu-libs lttng-ust \
    && adduser -D -g '' appuser

# Copy PowerShell from the release-tarball stage (same layout/symlink as before)
COPY --from=powershell /opt/microsoft/powershell /opt/microsoft/powershell
RUN ln -s /opt/microsoft/powershell/7/pwsh /usr/bin/pwsh

# Copy the compiled binary from the builder stage
COPY --from=builder /server /

# Run as non-root
USER appuser

# Expose the port the API server runs on
EXPOSE 8000

# Set the entrypoint for the container
CMD ["/server"]
