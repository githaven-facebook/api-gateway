package router

import "context"

type contextKey int

const (
	routeContextKey contextKey = iota
)

// WithRoute stores the matched route in the request context.
func WithRoute(ctx context.Context, route *Route) context.Context {
	return context.WithValue(ctx, routeContextKey, route)
}

// RouteFromContext retrieves the matched route from the request context.
func RouteFromContext(ctx context.Context) *Route {
	route, _ := ctx.Value(routeContextKey).(*Route)
	return route
}
