package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/nicedavid98/api-gateway/internal/router"
)

const tracerName = "api-gateway"

// Tracing returns a middleware that creates OpenTelemetry spans for each request.
func Tracing(tracer trace.Tracer) func(http.Handler) http.Handler {
	if tracer == nil {
		tracer = otel.Tracer(tracerName)
	}
	propagator := otel.GetTextMapPropagator()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract parent context from incoming headers (W3C TraceContext / B3).
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			route := router.RouteFromContext(ctx)
			spanName := r.Method + " " + r.URL.Path
			if route != nil && route.ServiceName != "" {
				spanName = r.Method + " " + route.ServiceName + r.URL.Path
			}

			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPMethod(r.Method),
					semconv.HTTPURL(r.URL.String()),
					semconv.HTTPRoute(r.URL.Path),
					attribute.String("http.request_id", r.Header.Get("X-Request-ID")),
					attribute.String("net.peer.ip", extractClientIP(r)),
				),
			)
			defer span.End()

			// Inject trace context into outgoing headers so the upstream can correlate.
			propagator.Inject(ctx, propagation.HeaderCarrier(r.Header))

			rec := newStatusRecorder(w)
			next.ServeHTTP(rec, r.WithContext(ctx))

			span.SetAttributes(semconv.HTTPStatusCode(rec.statusCode))

			if rec.statusCode >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(rec.statusCode))
			} else {
				span.SetStatus(codes.Ok, "")
			}
		})
	}
}
