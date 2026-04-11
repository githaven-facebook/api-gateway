package middleware

import (
	"net/http"
	"strings"

	"github.com/nicedavid98/api-gateway/internal/router"
)

// Transform returns a middleware that applies request and response transformations
// defined in the matched route's Transform configuration.
func Transform(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := router.RouteFromContext(r.Context())
		if route == nil || route.Transform == nil {
			next.ServeHTTP(w, r)
			return
		}

		t := route.Transform

		// Apply request header transformations.
		for k, v := range t.AddRequestHeaders {
			r.Header.Set(k, v)
		}
		for _, k := range t.RemoveRequestHeaders {
			r.Header.Del(k)
		}

		// Apply path rewrite if configured.
		if t.RewritePath != "" {
			original := r.URL.Path
			r.URL.Path = rewritePath(original, t.RewritePath)
			r.RequestURI = r.URL.RequestURI()
		}

		// Wrap response writer to apply response header transformations.
		if len(t.AddResponseHeaders) > 0 || len(t.RemoveResponseHeaders) > 0 {
			trw := &transformResponseWriter{
				ResponseWriter:        w,
				addHeaders:            t.AddResponseHeaders,
				removeHeaders:         t.RemoveResponseHeaders,
				headerTransformDone:   false,
			}
			next.ServeHTTP(trw, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// transformResponseWriter applies response header transformations before writing headers.
type transformResponseWriter struct {
	http.ResponseWriter
	addHeaders          map[string]string
	removeHeaders       []string
	headerTransformDone bool
}

func (t *transformResponseWriter) WriteHeader(code int) {
	if !t.headerTransformDone {
		t.applyHeaders()
		t.headerTransformDone = true
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *transformResponseWriter) Write(b []byte) (int, error) {
	if !t.headerTransformDone {
		t.applyHeaders()
		t.headerTransformDone = true
	}
	return t.ResponseWriter.Write(b)
}

func (t *transformResponseWriter) applyHeaders() {
	for _, k := range t.removeHeaders {
		t.ResponseWriter.Header().Del(k)
	}
	for k, v := range t.addHeaders {
		t.ResponseWriter.Header().Set(k, v)
	}
}

// rewritePath applies a simple template rewrite. The template may use {path}
// as a placeholder for the original path.
func rewritePath(original, template string) string {
	if !strings.Contains(template, "{path}") {
		return template
	}
	return strings.ReplaceAll(template, "{path}", original)
}
