# Stage 1: Build the Go binary
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy dependency definition files first for caching
COPY go.mod ./
RUN go mod download || true

# Copy all source files
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /xray-endpoints-prober ./cmd/prober

# Stage 2: Minimal runtime image
FROM alpine:3.20

# Copy preinstalled ca-certificates directly from the official golang:alpine builder image.
# This eliminates ANY network calls (apk update / apk add) during docker build!
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

WORKDIR /app

# Copy binary from builder
COPY --from=builder /xray-endpoints-prober /app/xray-endpoints-prober

# Setup default directories
RUN mkdir -p /var/cache/xray /var/cache/xray-prober && chmod -R 777 /var/cache

ENV XRAY_CACHE_DIR=/var/cache/xray \
    SUBSCRIPTION_CACHE_FILE=/var/cache/xray-prober/subscription.cache \
    LISTEN_ADDR=:8080

EXPOSE 8080

ENTRYPOINT ["/app/xray-endpoints-prober"]
