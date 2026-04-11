//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nicedavid98/api-gateway/internal/circuitbreaker"
	"github.com/nicedavid98/api-gateway/internal/config"
	"github.com/nicedavid98/api-gateway/internal/discovery"
	"github.com/nicedavid98/api-gateway/internal/proxy"
	"github.com/nicedavid98/api-gateway/internal/router"
)

// mockBackend is a simple test backend server.
type mockBackend struct {
	server     *httptest.Server
	statusCode int
	body       string
	requests   int
}

func newMockBackend(statusCode int, body string) *mockBackend {
	m := &mockBackend{statusCode: statusCode, body: body}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		m.requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(m.statusCode)
		fmt.Fprint(w, m.body)
	}))
	return m
}

func (m *mockBackend) Close() {
	m.server.Close()
}

// buildTestGateway constructs a minimal gateway for integration testing.
func buildTestGateway(t *testing.T, routes []config.RouteConfig) *httptest.Server {
	t.Helper()

	reg := discovery.NewStaticRegistry()
	cbManager := circuitbreaker.NewManager(circuitbreaker.Settings{
		MaxFailures:         5,
		Timeout:             10 * time.Second,
		MaxHalfOpenRequests: 2,
	})

	reverseProxy := proxy.New(proxy.Options{
		Registry:   reg,
		MaxRetries: 0,
	})

	r := router.New()
	r.RegisterRoutes(routes, reverseProxy.ServeHTTP)

	_ = cbManager

	mux := chi.NewRouter()
	mux.Mount("/", r.Mux())

	return httptest.NewServer(mux)
}

func TestGateway_BasicRouting(t *testing.T) {
	backend := newMockBackend(http.StatusOK, `{"status":"ok"}`)
	defer backend.Close()

	routes := []config.RouteConfig{
		{
			ID:          "test-route",
			Path:        "/api/v1/test",
			Methods:     []string{"GET"},
			ServiceName: "test-service",
			ServiceURL:  backend.server.URL,
		},
	}

	gw := buildTestGateway(t, routes)
	defer gw.Close()

	resp, err := http.Get(gw.URL + "/api/v1/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}

	if backend.requests != 1 {
		t.Errorf("expected 1 backend request, got %d", backend.requests)
	}
}

func TestGateway_NotFound(t *testing.T) {
	gw := buildTestGateway(t, nil)
	defer gw.Close()

	resp, err := http.Get(gw.URL + "/no/such/path")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGateway_MultipleRoutes(t *testing.T) {
	backendA := newMockBackend(http.StatusOK, `{"service":"a"}`)
	backendB := newMockBackend(http.StatusCreated, `{"service":"b"}`)
	defer backendA.Close()
	defer backendB.Close()

	routes := []config.RouteConfig{
		{
			ID:          "route-a",
			Path:        "/api/v1/service-a",
			Methods:     []string{"GET"},
			ServiceName: "service-a",
			ServiceURL:  backendA.server.URL,
		},
		{
			ID:          "route-b",
			Path:        "/api/v1/service-b",
			Methods:     []string{"POST"},
			ServiceName: "service-b",
			ServiceURL:  backendB.server.URL,
		},
	}

	gw := buildTestGateway(t, routes)
	defer gw.Close()

	// Test route A.
	respA, err := http.Get(gw.URL + "/api/v1/service-a")
	if err != nil {
		t.Fatalf("request to A failed: %v", err)
	}
	defer respA.Body.Close()
	if respA.StatusCode != http.StatusOK {
		t.Errorf("route A: expected 200, got %d", respA.StatusCode)
	}

	// Test route B.
	respB, err := http.Post(gw.URL+"/api/v1/service-b", "application/json", nil)
	if err != nil {
		t.Fatalf("request to B failed: %v", err)
	}
	defer respB.Body.Close()
	if respB.StatusCode != http.StatusCreated {
		t.Errorf("route B: expected 201, got %d", respB.StatusCode)
	}
}

func TestGateway_BackendUnavailable(t *testing.T) {
	routes := []config.RouteConfig{
		{
			ID:          "dead-route",
			Path:        "/api/v1/dead",
			Methods:     []string{"GET"},
			ServiceName: "dead-service",
			ServiceURL:  "http://127.0.0.1:1", // Nothing listening here.
		},
	}

	gw := buildTestGateway(t, routes)
	defer gw.Close()

	resp, err := http.Get(gw.URL + "/api/v1/dead")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("expected 502 or 504, got %d", resp.StatusCode)
	}
}
