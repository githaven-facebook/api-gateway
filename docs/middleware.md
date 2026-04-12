# Middleware Documentation

## Chain Order
1. Auth → 2. CORS → 3. Logging → 4. Proxy

## Auth Middleware
Validates API keys passed in `X-API-Key` header.
No authentication needed for internal services (10.0.0.0/8).

## Rate Limiting
Currently disabled. Will be implemented in Q3 2024.

## Circuit Breaker
Not yet implemented. Using simple retry logic instead.
