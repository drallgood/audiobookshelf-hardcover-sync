# Build stage
FROM golang:1.21-alpine as builder

ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN apk add --no-cache git ca-certificates tzdata && \
    go mod tidy && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
      -ldflags="-w -s -X main.version=$VERSION -extldflags '-static'" \
      -a -installsuffix cgo \
      -o main .

# Final stage
FROM alpine:3.20

ARG VERSION=dev
LABEL maintainer="patrice@brendamour.net" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.source="https://github.com/drallgood/audiobookshelf-hardcover-sync" \
      description="Audiobookshelf Hardcover Sync"

WORKDIR /app
EXPOSE 8080

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

COPY --from=builder --chown=appuser:appgroup /app/main .

USER appuser

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/app/main", "--health-check"] || exit 1

CMD ["/app/main"]
