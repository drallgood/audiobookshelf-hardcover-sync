# Build stage
FROM golang:1.22-alpine as builder

ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN apk add --no-cache git ca-certificates tzdata && \
    go mod tidy && \
    CGO_ENABLED=0 go build \
      -ldflags="-w -s -X main.version=$VERSION -extldflags '-static'" \
      -a -installsuffix cgo \
      -o main .

# Final stage
FROM scratch

ARG VERSION=dev
LABEL maintainer="patrice@brendamour.net" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.source="https://github.com/drallgood/audiobookshelf-hardcover-sync" \
      description="Audiobookshelf Hardcover Sync"

WORKDIR /app
EXPOSE 8080

COPY --from=builder /app/main .

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/app/main", "--health-check"] || exit 1

CMD ["/app/main"]
