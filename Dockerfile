# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go module files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -extldflags '-static'" \
    -o gateway ./cmd/gateway/...

# Final stage - distroless
FROM gcr.io/distroless/static-debian12:nonroot

# Copy timezone data and CA certs from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binary
COPY --from=builder /build/gateway /gateway

# Copy default config
COPY --from=builder /build/config /config

EXPOSE 8080 9090

USER nonroot:nonroot

ENTRYPOINT ["/gateway"]
CMD ["-config", "/config/gateway.yaml"]
