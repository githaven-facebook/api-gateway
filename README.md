# API Gateway

A production-quality API Gateway service for Facebook microservices platform.

## Overview

This service acts as the central entry point for all Facebook microservices, providing:

- **Dynamic Routing** — Service-based routing with path matching and wildcard support
- **Authentication** — JWT validation with JWKS support (RS256/HS256)
- **Rate Limiting** — Token bucket algorithm with per-user, per-IP, per-route limits backed by Redis
- **Circuit Breaker** — Protects downstream services from cascading failures
- **Load Balancing** — Round-robin, weighted, and least-connections strategies
- **Observability** — Structured logging (zap), distributed tracing (OpenTelemetry), Prometheus metrics
- **Admin API** — Dynamic route configuration and runtime management
- **Health Aggregation** — Parallel downstream health checks with degraded state tracking

## Architecture

```
Client → [CORS] → [RequestID] → [Logging] → [Tracing] → [Auth] → [RateLimit] → [CircuitBreaker] → [Proxy] → Backend
```

## Middleware Pipeline

| Middleware      | Description                                              |
|-----------------|----------------------------------------------------------|
| Recovery        | Panic recovery, 500 response                             |
| CORS            | Cross-origin request handling                            |
| RequestID       | UUID v4 X-Request-ID generation/propagation              |
| Logging         | Structured request/response logging                      |
| Tracing         | OpenTelemetry span creation and propagation              |
| Auth            | JWT Bearer token validation, user context injection      |
| RateLimit       | Token bucket rate limiting with Redis backend            |
| CircuitBreaker  | Per-service failure tracking with automatic recovery     |

## Route Configuration

Routes are defined in `config/routes.yaml`:

```yaml
routes:
  - path: /api/v1/users/{userID}
    methods: [GET, PUT, DELETE]
    service_name: user-service
    auth_required: true
    strip_prefix: /api/v1
    timeout: 30s
    rate_limit:
      rps: 100
      burst: 200
    circuit_breaker:
      threshold: 5
      timeout: 30s
```

## Rate Limiting

Rate limiting uses a token bucket algorithm with three scopes:

- **Global** — Default RPS for all requests
- **Per-user** — Applied to authenticated requests by user ID
- **Per-route** — Route-specific overrides

Response headers:
- `X-RateLimit-Limit` — Maximum requests allowed
- `X-RateLimit-Remaining` — Remaining requests in window
- `X-RateLimit-Reset` — Unix timestamp when the window resets
- `Retry-After` — Seconds to wait on 429 responses

## Circuit Breaker

States: `Closed` → `Open` → `Half-Open` → `Closed`

- Opens after N consecutive failures (configurable threshold)
- Half-opens after timeout period
- Closes on successful request in half-open state
- Returns 503 when circuit is open

## Load Balancing

Three strategies available:

- `round-robin` — Equal distribution across instances
- `weighted` — Proportional distribution by weight
- `least-connections` — Route to instance with fewest active connections

## Admin API

Available on a separate port (default: 9090):

```
GET    /admin/routes                           List all routes
POST   /admin/routes                           Add a route dynamically
DELETE /admin/routes/{id}                      Remove a route
GET    /admin/health                           Aggregated health status
GET    /admin/services                         Service registry status
GET    /admin/circuit-breakers                 Circuit breaker states
POST   /admin/circuit-breakers/{service}/reset Reset a circuit breaker
```

## Deployment

### Local Development

```bash
make run
```

### Docker

```bash
make docker-build
docker-compose up
```

### Configuration

Set environment variables or edit `config/gateway.yaml`:

```yaml
server:
  port: 8080
  admin_port: 9090
  read_timeout: 30s
  write_timeout: 30s

auth:
  jwk_url: http://auth-service/jwks
  issuer: https://auth.facebook.internal
  audiences:
    - api-gateway

rate_limit:
  enabled: true
  default_rps: 1000
  burst_size: 2000
  redis_addr: redis:6379
```

## Monitoring

Prometheus metrics exposed at `/metrics`:

| Metric                                  | Type      | Labels                          |
|-----------------------------------------|-----------|---------------------------------|
| `gateway_requests_total`                | Counter   | service, method, status         |
| `gateway_request_duration_seconds`      | Histogram | service, method, status         |
| `gateway_active_connections`            | Gauge     | —                               |
| `gateway_circuit_breaker_state`         | Gauge     | service                         |
| `gateway_rate_limit_exceeded_total`     | Counter   | route, scope                    |

## Development

```bash
make build    # Build binary
make test     # Run tests
make lint     # Run golangci-lint
make bench    # Run benchmarks
```
