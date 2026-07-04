# syntax=docker/dockerfile:1.7
# --- Build Stage ---
# Use the official Go image to build the application
FROM golang:1.26.4-alpine AS builder

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
# Copy pwsh binary from Microsoft's official image (avoids curl download timeouts in CI)
FROM mcr.microsoft.com/powershell:7.4-alpine-3.17 AS powershell

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
RUN apk add --no-cache iputils ca-certificates traceroute nodejs \
    icu-libs lttng-ust \
    && adduser -D -g '' appuser

# Copy PowerShell from the official image
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
