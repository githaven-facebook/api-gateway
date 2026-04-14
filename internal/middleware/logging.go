package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/nicedavid98/api-gateway/internal/router"
)

// loggingResponseWriter wraps http.ResponseWriter to capture response metadata.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode    int
	bytesWritten  int64
	headerWritten bool
}

func newLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	return &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	if !lrw.headerWritten {
		lrw.statusCode = code
		lrw.headerWritten = true
		lrw.ResponseWriter.WriteHeader(code)
	}
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if !lrw.headerWritten {
		lrw.WriteHeader(http.StatusOK)
	}
	n, err := lrw.ResponseWriter.Write(b)
	lrw.bytesWritten += int64(n)
	return n, err
}

// Logging returns a middleware that logs structured request/response information.
func Logging(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			lrw := newLoggingResponseWriter(w)
			ctx := r.Context()

			defer func() {
				duration := time.Since(start)
				route := router.RouteFromContext(ctx)

				fields := []zap.Field{
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.Int("status", lrw.statusCode),
					zap.Duration("duration", duration),
					zap.Int64("response_bytes", lrw.bytesWritten),
					zap.String("request_id", r.Header.Get("X-Request-ID")),
					zap.String("user_agent", r.UserAgent()),
				}

				if route != nil {
					fields = append(fields, zap.String("service", route.ServiceName))
				}

				userID := r.Header.Get("X-User-Id")
				if userID != "" {
					fields = append(fields, zap.String("user_id", userID))
				}

				switch {
				case lrw.statusCode >= http.StatusInternalServerError:
					logger.Error("request completed", fields...)
				case lrw.statusCode >= http.StatusBadRequest:
					logger.Warn("request completed", fields...)
				default:
					logger.Info("request completed", fields...)
				}
			}()

			next.ServeHTTP(lrw, r)
		})
	}
}
