package router

import (
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/nicedavid98/api-gateway/internal/config"
)

// Router wraps chi.Router with gateway-specific route management.
type Router struct {
	mux    chi.Router
	routes map[string]*Route
	mu     sync.RWMutex
}

// New creates a new Router with the given middleware chain applied.
func New() *Router {
	r := &Router{
		mux:    chi.NewRouter(),
		routes: make(map[string]*Route),
	}
	return r
}

// Mux returns the underlying chi.Router for middleware attachment.
func (r *Router) Mux() chi.Router {
	return r.mux
}

// RegisterRoutes registers all routes from the provided config slice.
func (r *Router) RegisterRoutes(routes []config.RouteConfig, handler http.HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, rc := range routes {
		rc := rc // capture loop variable
		route := FromConfig(rc)

		if route.ID == "" {
			route.ID = routeID(route.Path, route.Methods)
		}

		r.routes[route.ID] = &route
		r.registerChi(route, handler)
	}
}

// AddRoute adds a single route dynamically at runtime.
func (r *Router) AddRoute(rc config.RouteConfig, handler http.HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	route := FromConfig(rc)
	if route.ID == "" {
		route.ID = routeID(route.Path, route.Methods)
	}

	r.routes[route.ID] = &route
	r.registerChi(route, handler)
}

// RemoveRoute removes a route by ID. Note: chi does not support runtime deregistration;
// removed routes will 404 on next request after router rebuild.
func (r *Router) RemoveRoute(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, exists := r.routes[id]
	if !exists {
		return false
	}
	delete(r.routes, id)
	return true
}

// GetRoute retrieves a route by ID.
func (r *Router) GetRoute(id string) (*Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	route, ok := r.routes[id]
	return route, ok
}

// ListRoutes returns all registered routes.
func (r *Router) ListRoutes() []*Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	routes := make([]*Route, 0, len(r.routes))
	for _, route := range r.routes {
		routes = append(routes, route)
	}
	return routes
}

// MatchRoute finds the route for the given request by examining chi context.
func (r *Router) MatchRoute(req *http.Request) *Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Walk routes looking for path/method match.
	for _, route := range r.routes {
		if matchPath(route.Path, req.URL.Path) && matchMethod(route.Methods, req.Method) {
			return route
		}
	}
	return nil
}

// registerChi adds a route to the underlying chi mux.
func (r *Router) registerChi(route Route, handler http.HandlerFunc) {
	methods := route.Methods
	if len(methods) == 0 {
		methods = []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
			http.MethodHead,
		}
	}

	for _, method := range methods {
		method := strings.ToUpper(method)
		path := route.Path

		r.mux.Method(method, path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Attach route to request context for downstream middleware.
			ctx := WithRoute(req.Context(), &route)
			handler(w, req.WithContext(ctx))
		}))
	}
}

// routeID generates a stable ID from path and methods.
func routeID(path string, methods []string) string {
	return path + ":" + strings.Join(methods, ",")
}

// matchPath checks whether reqPath matches a route pattern.
// Handles exact, param ({name}), and wildcard (*) segments.
func matchPath(pattern, reqPath string) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	reqParts := strings.Split(strings.Trim(reqPath, "/"), "/")

	if len(patternParts) != len(reqParts) {
		// Allow wildcard tail match.
		if len(patternParts) > 0 && strings.HasSuffix(patternParts[len(patternParts)-1], "*") {
			return len(reqParts) >= len(patternParts)-1
		}
		return false
	}

	for i, p := range patternParts {
		if p == "*" || (strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}")) {
			continue
		}
		if p != reqParts[i] {
			return false
		}
	}
	return true
}

// matchMethod reports whether method matches the route's allowed methods.
// An empty list means all methods are allowed.
func matchMethod(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, m := range methods {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}
