// Command gateway is the main entry point for the Facebook API Gateway service.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/nicedavid98/api-gateway/internal/admin"
	"github.com/nicedavid98/api-gateway/internal/auth"
	"github.com/nicedavid98/api-gateway/internal/circuitbreaker"
	"github.com/nicedavid98/api-gateway/internal/config"
	"github.com/nicedavid98/api-gateway/internal/discovery"
	"github.com/nicedavid98/api-gateway/internal/health"
	"github.com/nicedavid98/api-gateway/internal/metrics"
	"github.com/nicedavid98/api-gateway/internal/middleware"
	"github.com/nicedavid98/api-gateway/internal/proxy"
	"github.com/nicedavid98/api-gateway/internal/ratelimit"
	"github.com/nicedavid98/api-gateway/internal/router"
)

const shutdownTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config/gateway.yaml", "path to gateway config file")
	routesPath := flag.String("routes", "config/routes.yaml", "path to routes config file")
	healthCheck := flag.Bool("health-check", false, "perform a health check and exit")
	flag.Parse()

	// Load configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Load routes from separate file if gateway.yaml doesn't embed them.
	if len(cfg.Routes) == 0 {
		routesCfg, err := config.Load(*routesPath)
		if err == nil {
			cfg.Routes = routesCfg.Routes
		}
	}

	// Initialize logger.
	logger, err := buildLogger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("starting api-gateway",
		zap.Int("port", cfg.Server.Port),
		zap.Int("admin_port", cfg.Server.AdminPort),
		zap.Int("routes", len(cfg.Routes)),
	)

	// Health check mode.
	if *healthCheck {
		return performHealthCheck(cfg)
	}

	// Initialize OpenTelemetry tracer.
	shutdownTracer, err := initTracer(cfg.Tracing, logger)
	if err != nil {
		logger.Warn("failed to initialize tracer, continuing without tracing", zap.Error(err))
	}
	if shutdownTracer != nil {
		defer shutdownTracer()
	}

	// Initialize Redis client.
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RateLimit.RedisAddr,
		Password: cfg.RateLimit.RedisPass,
	})
	defer redisClient.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pingCancel()
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		logger.Warn("redis connection failed, rate limiting will operate in fail-open mode", zap.Error(err))
	}

	// Initialize JWT validator.
	jwtValidator := auth.NewValidator(auth.ValidatorConfig{
		JWKURL:    cfg.Auth.JWKURL,
		Issuer:    cfg.Auth.Issuer,
		Audiences: cfg.Auth.Audiences,
		CacheTTL:  cfg.Auth.CacheTTL,
		Logger:    logger,
	})

	// Initialize token cache.
	tokenCache := auth.NewTokenCache(redisClient, cfg.Auth.CacheTTL)

	// Initialize rate limiter.
	redisStore := ratelimit.NewRedisStore(redisClient)
	rateLimiter := ratelimit.NewRateLimiter(redisStore, ratelimit.Config{
		DefaultRPS: cfg.RateLimit.DefaultRPS,
		BurstSize:  cfg.RateLimit.BurstSize,
		Logger:     logger,
	})

	// Initialize circuit breaker manager.
	cbSettings := circuitbreaker.Settings{
		MaxFailures:         cfg.CircuitBreaker.Threshold,
		Timeout:             cfg.CircuitBreaker.Timeout,
		MaxHalfOpenRequests: cfg.CircuitBreaker.MaxHalfOpen,
	}
	cbManager := circuitbreaker.NewManager(cbSettings)

	// Initialize service registry.
	registry := discovery.NewStaticRegistry()
	populateRegistry(registry, cfg.Routes, logger)

	// Initialize proxy.
	reverseProxy := proxy.New(proxy.Options{
		Registry:   registry,
		Logger:     logger,
		DefaultLB:  proxy.StrategyRoundRobin,
		MaxRetries: 2,
	})

	// Initialize metrics.
	metricsCollector := metrics.New(prometheus.DefaultRegisterer)

	// Build health checker.
	healthEndpoints := buildHealthEndpoints(cfg.Routes)
	healthChecker := health.NewChecker(healthEndpoints, 15*time.Second, logger)

	// Build main router.
	gRouter := router.New()
	buildMiddlewareChain(gRouter.Mux(), cfg, logger, jwtValidator, tokenCache, rateLimiter, cbManager, metricsCollector)

	// Register routes.
	gRouter.RegisterRoutes(cfg.Routes, reverseProxy.ServeHTTP)

	// Add built-in gateway endpoints.
	gRouter.Mux().Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	gRouter.Mux().Handle("/metrics", metrics.Handler())

	// Build admin router.
	adminHandler := admin.NewHandler(
		gRouter,
		registry,
		cbManager,
		healthChecker,
		reverseProxy.ServeHTTP,
		logger,
	)

	// Start servers.
	mainServer := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:        gRouter.Mux(),
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	adminServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.AdminPort),
		Handler:      adminHandler.Mux(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	serverErrors := make(chan error, 2)

	go func() {
		logger.Info("gateway server listening", zap.String("addr", mainServer.Addr))
		if err := mainServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("gateway server: %w", err)
		}
	}()

	go func() {
		logger.Info("admin server listening", zap.String("addr", adminServer.Addr))
		if err := adminServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("admin server: %w", err)
		}
	}()

	// Wait for shutdown signal or server error.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err
	case sig := <-quit:
		logger.Info("shutdown signal received", zap.Stringer("signal", sig))
	}

	// Graceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	logger.Info("shutting down servers gracefully")

	if err := mainServer.Shutdown(ctx); err != nil {
		logger.Error("error shutting down main server", zap.Error(err))
	}
	if err := adminServer.Shutdown(ctx); err != nil {
		logger.Error("error shutting down admin server", zap.Error(err))
	}

	logger.Info("gateway stopped")
	return nil
}

// buildMiddlewareChain attaches all middleware to the chi router.
func buildMiddlewareChain(
	mux chi.Router,
	cfg *config.Config,
	logger *zap.Logger,
	jwtValidator *auth.Validator,
	tokenCache *auth.TokenCache,
	rateLimiter *ratelimit.RateLimiter,
	cbManager *circuitbreaker.Manager,
	metricsCollector *metrics.Metrics,
) {
	// Built-in chi middlewares.
	mux.Use(chiMiddleware.RealIP)

	// Custom middleware stack (outermost → innermost).
	mux.Use(middleware.Recovery(logger))
	mux.Use(middleware.CORS(middleware.DefaultCORSConfig()))
	mux.Use(middleware.RequestID)
	mux.Use(middleware.Logging(logger))
	mux.Use(middleware.Tracing(nil))
	mux.Use(metricsCollector.TrackActiveConnections)
	mux.Use(metricsCollector.InstrumentHandler)
	mux.Use(middleware.Transform)

	authMW := middleware.NewAuthMiddleware(jwtValidator, tokenCache, logger)
	mux.Use(authMW.Handler)

	if cfg.RateLimit.Enabled {
		rlMW := middleware.NewRateLimitMiddleware(rateLimiter, logger)
		mux.Use(rlMW.Handler)
	}

	cbMW := middleware.NewCircuitBreakerMiddleware(cbManager, logger)
	mux.Use(cbMW.Handler)
}

// initTracer initializes the OpenTelemetry tracer provider.
func initTracer(cfg config.TracingConfig, logger *zap.Logger) (func(), error) {
	if !cfg.Enabled {
		return nil, nil
	}

	ctx := context.Background()

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)

	logger.Info("tracing initialized",
		zap.String("endpoint", cfg.Endpoint),
		zap.Float64("sample_rate", cfg.SampleRate),
	)

	return func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(shutCtx); err != nil {
			logger.Error("error shutting down tracer provider", zap.Error(err))
		}
	}, nil
}

// buildLogger creates a production zap logger.
func buildLogger() (*zap.Logger, error) {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	cfg := zap.Config{
		Level:             zap.NewAtomicLevelAt(zap.InfoLevel),
		Development:       false,
		DisableCaller:     false,
		DisableStacktrace: false,
		Sampling:          nil,
		Encoding:          "json",
		EncoderConfig:     encoderCfg,
		OutputPaths:       []string{"stdout"},
		ErrorOutputPaths:  []string{"stderr"},
	}

	return cfg.Build()
}

// populateRegistry adds known service instances to the static registry.
func populateRegistry(reg *discovery.StaticRegistry, routes []config.RouteConfig, logger *zap.Logger) {
	seen := make(map[string]bool)
	for _, rc := range routes {
		if rc.ServiceURL == "" || seen[rc.ServiceName] {
			continue
		}
		seen[rc.ServiceName] = true

		inst := discovery.Instance{
			ID:          rc.ServiceName + "-0",
			ServiceName: rc.ServiceName,
			Host:        rc.ServiceURL,
			Weight:      1,
			Healthy:     true,
		}

		if err := reg.Register(context.Background(), inst); err != nil {
			logger.Warn("failed to register service instance",
				zap.String("service", rc.ServiceName),
				zap.Error(err),
			)
		}
	}
}

// buildHealthEndpoints creates health check endpoints from route configuration.
func buildHealthEndpoints(routes []config.RouteConfig) []health.ServiceEndpoint {
	seen := make(map[string]bool)
	var endpoints []health.ServiceEndpoint

	for _, rc := range routes {
		if rc.ServiceURL == "" || seen[rc.ServiceName] || rc.ServiceName == "gateway" {
			continue
		}
		seen[rc.ServiceName] = true

		endpoints = append(endpoints, health.ServiceEndpoint{
			Name:      rc.ServiceName,
			HealthURL: rc.ServiceURL + "/health",
			Timeout:   5 * time.Second,
		})
	}
	return endpoints
}

// performHealthCheck makes a request to the local gateway health endpoint.
func performHealthCheck(cfg *config.Config) error {
	url := fmt.Sprintf("http://localhost:%d/health", cfg.Server.Port)
	resp, err := http.Get(url) //nolint:gosec,noctx
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}
