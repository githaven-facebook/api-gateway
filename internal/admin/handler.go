// Package admin provides the admin API for dynamic gateway management.
package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/nicedavid98/api-gateway/internal/circuitbreaker"
	"github.com/nicedavid98/api-gateway/internal/config"
	"github.com/nicedavid98/api-gateway/internal/discovery"
	"github.com/nicedavid98/api-gateway/internal/health"
	"github.com/nicedavid98/api-gateway/internal/router"
)

// Handler implements the admin API endpoints on a separate port.
type Handler struct {
	router       *router.Router
	registry     discovery.ServiceRegistry
	cbManager    *circuitbreaker.Manager
	checker      *health.Checker
	logger       *zap.Logger
	proxyHandler http.HandlerFunc
}

// NewHandler creates a new admin Handler.
func NewHandler(
	r *router.Router,
	registry discovery.ServiceRegistry,
	cbManager *circuitbreaker.Manager,
	checker *health.Checker,
	proxyHandler http.HandlerFunc,
	logger *zap.Logger,
) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		router:       r,
		registry:     registry,
		cbManager:    cbManager,
		checker:      checker,
		proxyHandler: proxyHandler,
		logger:       logger,
	}
}

// Mux returns a chi.Router with all admin endpoints registered.
func (h *Handler) Mux() chi.Router {
	r := chi.NewRouter()

	r.Get("/admin/routes", h.listRoutes)
	r.Post("/admin/routes", h.addRoute)
	r.Delete("/admin/routes/{id}", h.deleteRoute)
	r.Get("/admin/health", h.aggregateHealth)
	r.Get("/admin/services", h.listServices)
	r.Get("/admin/circuit-breakers", h.listCircuitBreakers)
	r.Post("/admin/circuit-breakers/{service}/reset", h.resetCircuitBreaker)

	return r
}

// listRoutes returns all registered routes.
func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes := h.router.ListRoutes()

	type routeResponse struct {
		ID           string   `json:"id"`
		Path         string   `json:"path"`
		Methods      []string `json:"methods"`
		ServiceName  string   `json:"service_name"`
		AuthRequired bool     `json:"auth_required"`
		LoadBalance  string   `json:"load_balance,omitempty"`
	}

	resp := make([]routeResponse, 0, len(routes))
	for _, route := range routes {
		resp = append(resp, routeResponse{
			ID:           route.ID,
			Path:         route.Path,
			Methods:      route.Methods,
			ServiceName:  route.ServiceName,
			AuthRequired: route.AuthRequired,
			LoadBalance:  route.LoadBalance,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes": resp,
		"total":  len(resp),
	})
}

// addRoute dynamically registers a new route.
func (h *Handler) addRoute(w http.ResponseWriter, r *http.Request) {
	var rc config.RouteConfig
	if err := json.NewDecoder(r.Body).Decode(&rc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if rc.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if rc.ServiceName == "" && rc.ServiceURL == "" {
		writeError(w, http.StatusBadRequest, "service_name or service_url is required")
		return
	}

	h.router.AddRoute(rc, h.proxyHandler)
	h.logger.Info("route added via admin API",
		zap.String("path", rc.Path),
		zap.String("service", rc.ServiceName),
	)

	writeJSON(w, http.StatusCreated, map[string]string{
		"status": "created",
		"path":   rc.Path,
	})
}

// deleteRoute removes a route by ID.
func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "route id is required")
		return
	}

	if !h.router.RemoveRoute(id) {
		writeError(w, http.StatusNotFound, "route not found: "+id)
		return
	}

	h.logger.Info("route removed via admin API", zap.String("id", id))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// aggregateHealth returns the aggregated health of all downstream services.
func (h *Handler) aggregateHealth(w http.ResponseWriter, r *http.Request) {
	var agg *health.AggregateHealth
	if h.checker != nil {
		agg = h.checker.CheckAll(r.Context())
	} else {
		agg = &health.AggregateHealth{
			Status:    health.StatusHealthy,
			Services:  map[string]*health.ServiceHealth{},
			CheckedAt: time.Now(),
		}
	}

	statusCode := http.StatusOK
	if agg.Status == health.StatusUnhealthy {
		statusCode = http.StatusServiceUnavailable
	}

	writeJSON(w, statusCode, agg)
}

// listServices returns the service registry status.
func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	if sr, ok := h.registry.(*discovery.StaticRegistry); ok {
		names := sr.ListServices()
		type serviceInfo struct {
			Name      string `json:"name"`
			Instances int    `json:"instances"`
		}

		services := make([]serviceInfo, 0, len(names))
		for _, name := range names {
			instances, err := h.registry.GetInstances(r.Context(), name)
			if err != nil {
				h.logger.Warn("failed to get instances for service", zap.String("service", name), zap.Error(err))
			}
			services = append(services, serviceInfo{
				Name:      name,
				Instances: len(instances),
			})
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"services": services,
			"total":    len(services),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"services": []interface{}{},
		"total":    0,
	})
}

// listCircuitBreakers returns the state of all circuit breakers.
func (h *Handler) listCircuitBreakers(w http.ResponseWriter, _ *http.Request) {
	breakers := h.cbManager.All()

	type breakerInfo struct {
		Service              string `json:"service"`
		State                string `json:"state"`
		ConsecutiveFailures  uint32 `json:"consecutive_failures"`
		ConsecutiveSuccesses uint32 `json:"consecutive_successes"`
		TotalRequests        uint32 `json:"total_requests"`
	}

	result := make([]breakerInfo, 0, len(breakers))
	for name, b := range breakers {
		counts := b.Counts()
		result = append(result, breakerInfo{
			Service:              name,
			State:                b.State().String(),
			ConsecutiveFailures:  counts.ConsecutiveFailures,
			ConsecutiveSuccesses: counts.ConsecutiveSuccesses,
			TotalRequests:        counts.Requests,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"circuit_breakers": result,
		"total":            len(result),
	})
}

// resetCircuitBreaker forces a circuit breaker back to closed state.
func (h *Handler) resetCircuitBreaker(w http.ResponseWriter, r *http.Request) {
	service := chi.URLParam(r, "service")
	if service == "" {
		writeError(w, http.StatusBadRequest, "service name is required")
		return
	}

	if !h.cbManager.Reset(service) {
		writeError(w, http.StatusNotFound, "circuit breaker not found for service: "+service)
		return
	}

	h.logger.Info("circuit breaker reset via admin API", zap.String("service", service))
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "reset",
		"service": service,
	})
}

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Best-effort; headers already written.
		_ = err
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
