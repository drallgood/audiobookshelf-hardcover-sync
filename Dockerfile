# Build stage
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set the working directory
WORKDIR /src

# Enable Go modules
ARG TARGETOS TARGETARCH
ENV GO111MODULE=on \
    GOPROXY=https://proxy.golang.org,direct \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH

# Copy module files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application (CGO disabled for cross-platform compatibility)
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 go build -trimpath -ldflags="-w -s -X 'main.version=${VERSION:-dev}'" \
    -o /app/audiobookshelf-hardcover-sync ./cmd/audiobookshelf-hardcover-sync

# Final stage
FROM alpine:3.18

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    && addgroup -S app \
    && adduser -S -G app app

# Set the working directory
WORKDIR /app

# Copy the binary from builder
COPY --from=builder --chown=app:app /app/audiobookshelf-hardcover-sync /app/

# Copy entrypoint script
COPY --chown=app:app scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Create necessary directories
RUN mkdir -p /app/config /app/data \
    && chown -R app:app /app

# Copy default config if it doesn't exist
COPY --chown=app:app config.example.yaml /app/config/config.example.yaml

# Switch to non-root user
USER app

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Expose the default HTTP port
EXPOSE 8080

# Set the entrypoint to use the script
ENTRYPOINT ["/app/entrypoint.sh"]

# Default command (can be overridden)
CMD ["/app/audiobookshelf-hardcover-sync", "--config", "/app/config/config.yaml"]
