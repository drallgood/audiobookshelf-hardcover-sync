# syntax=docker/dockerfile:1.4

# Build stage
FROM golang:1.24.2-alpine AS build
WORKDIR /src

# Leverage Docker cache for dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Enable Go build cache
ENV GOCACHE=/go-cache

# Build the binary with cache and reproducibility
RUN --mount=type=cache,target=/go-cache \
    CGO_ENABLED=0 go build -trimpath -o /out/main .

# Final minimal image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/main /main
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]

# Healthcheck
HEALTHCHECK --interval=30s --timeout=3s CMD ["/main", "-healthcheck"]

# Expose port if needed (uncomment if your app listens on a port)
EXPOSE 8080
