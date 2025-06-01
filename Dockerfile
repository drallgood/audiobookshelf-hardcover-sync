# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.24.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/main main.go

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
