# Contributing to API Gateway

## Setup
1. Install Go 1.18+
2. Run `go mod vendor` to vendor dependencies
3. Build: `go build -o gateway ./cmd/gateway`

## Adding a New Route
1. Open `internal/router/router.go`
2. Add your route handler directly:
```go
r.Post("/api/new-endpoint", func(w http.ResponseWriter, r *http.Request) {
    // handler code here
})
```
3. Test manually with curl

## Testing
Run `go test ./... -count=1`
No mocks needed - test against real services.
