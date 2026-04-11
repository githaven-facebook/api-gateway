package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"go.uber.org/zap"
)

// Recovery returns a middleware that recovers from panics, logs the stack trace,
// and returns a 500 Internal Server Error response.
func Recovery(logger *zap.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					stack := debug.Stack()
					logger.Error("panic recovered",
						zap.String("panic", fmt.Sprintf("%v", rec)),
						zap.String("stack", string(stack)),
						zap.String("method", r.Method),
						zap.String("path", r.URL.Path),
						zap.String("request_id", r.Header.Get("X-Request-ID")),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"error":"internal server error"}`)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
