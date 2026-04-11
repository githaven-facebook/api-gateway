package middleware

import (
	"net/http"

	"github.com/google/uuid"
)

const requestIDHeader = "X-Request-ID"

// RequestID returns a middleware that generates or propagates X-Request-ID headers.
// If the incoming request already carries a request ID, it is preserved.
// Otherwise a new UUID v4 is generated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(requestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
			r.Header.Set(requestIDHeader, requestID)
		}
		w.Header().Set(requestIDHeader, requestID)
		next.ServeHTTP(w, r)
	})
}
