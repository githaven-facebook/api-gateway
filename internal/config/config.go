// Package config provides configuration loading and management for the API gateway.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the complete gateway configuration.
type Config struct {
	Server         ServerConfig         `yaml:"server"`
	Auth           AuthConfig           `yaml:"auth"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker"`
	Tracing        TracingConfig        `yaml:"tracing"`
	Routes         []RouteConfig        `yaml:"routes"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port           int           `yaml:"port"`
	AdminPort      int           `yaml:"admin_port"`
	ReadTimeout    time.Duration `yaml:"read_timeout"`
	WriteTimeout   time.Duration `yaml:"write_timeout"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"`
	MaxHeaderBytes int           `yaml:"max_header_bytes"`
}

// AuthConfig holds JWT authentication settings.
type AuthConfig struct {
	JWKURL    string        `yaml:"jwk_url"`
	Issuer    string        `yaml:"issuer"`
	Audiences []string      `yaml:"audiences"`
	CacheTTL  time.Duration `yaml:"cache_ttl"`
}

// RateLimitConfig holds rate limiting settings.
type RateLimitConfig struct {
	Enabled    bool          `yaml:"enabled"`
	DefaultRPS float64       `yaml:"default_rps"`
	BurstSize  int           `yaml:"burst_size"`
	RedisAddr  string        `yaml:"redis_addr"`
	RedisPass  string        `yaml:"redis_password"`
	KeyTTL     time.Duration `yaml:"key_ttl"`
}

// CircuitBreakerConfig holds circuit breaker settings.
type CircuitBreakerConfig struct {
	Threshold   uint32        `yaml:"threshold"`
	Timeout     time.Duration `yaml:"timeout"`
	MaxHalfOpen uint32        `yaml:"max_half_open"`
}

// TracingConfig holds OpenTelemetry tracing settings.
type TracingConfig struct {
	Enabled     bool    `yaml:"enabled"`
	Endpoint    string  `yaml:"endpoint"`
	SampleRate  float64 `yaml:"sample_rate"`
	ServiceName string  `yaml:"service_name"`
}

// RouteConfig defines a single route in the gateway configuration.
type RouteConfig struct {
	ID             string            `yaml:"id"`
	Path           string            `yaml:"path"`
	Methods        []string          `yaml:"methods"`
	ServiceName    string            `yaml:"service_name"`
	ServiceURL     string            `yaml:"service_url"`
	StripPrefix    string            `yaml:"strip_prefix"`
	Timeout        time.Duration     `yaml:"timeout"`
	AuthRequired   bool              `yaml:"auth_required"`
	RateLimit      *RouteLimitConfig `yaml:"rate_limit,omitempty"`
	CircuitBreaker *RouteCBConfig    `yaml:"circuit_breaker,omitempty"`
	Transform      *TransformConfig  `yaml:"transform,omitempty"`
	LoadBalance    string            `yaml:"load_balance"`
}

// RouteLimitConfig provides per-route rate limit overrides.
type RouteLimitConfig struct {
	RPS   float64 `yaml:"rps"`
	Burst int     `yaml:"burst"`
}

// RouteCBConfig provides per-route circuit breaker overrides.
type RouteCBConfig struct {
	Threshold   uint32        `yaml:"threshold"`
	Timeout     time.Duration `yaml:"timeout"`
	MaxHalfOpen uint32        `yaml:"max_half_open"`
}

// TransformConfig defines request/response transformation rules.
type TransformConfig struct {
	AddRequestHeaders     map[string]string `yaml:"add_request_headers,omitempty"`
	RemoveRequestHeaders  []string          `yaml:"remove_request_headers,omitempty"`
	AddResponseHeaders    map[string]string `yaml:"add_response_headers,omitempty"`
	RemoveResponseHeaders []string          `yaml:"remove_response_headers,omitempty"`
	RewritePath           string            `yaml:"rewrite_path,omitempty"`
}

// Load reads and parses the gateway configuration from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// defaultConfig returns a Config populated with sensible defaults.
func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:           8080,
			AdminPort:      9090,
			ReadTimeout:    30 * time.Second,
			WriteTimeout:   30 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20, // 1 MB
		},
		Auth: AuthConfig{
			CacheTTL: 5 * time.Minute,
		},
		RateLimit: RateLimitConfig{
			Enabled:    true,
			DefaultRPS: 1000,
			BurstSize:  2000,
			RedisAddr:  "localhost:6379",
			KeyTTL:     time.Minute,
		},
		CircuitBreaker: CircuitBreakerConfig{
			Threshold:   5,
			Timeout:     30 * time.Second,
			MaxHalfOpen: 2,
		},
		Tracing: TracingConfig{
			SampleRate:  0.1,
			ServiceName: "api-gateway",
		},
	}
}

// validate checks that required config fields are set.
func (c *Config) validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.Server.Port)
	}
	if c.Server.AdminPort <= 0 || c.Server.AdminPort > 65535 {
		return fmt.Errorf("server.admin_port must be between 1 and 65535, got %d", c.Server.AdminPort)
	}
	if c.Server.Port == c.Server.AdminPort {
		return fmt.Errorf("server.port and server.admin_port must be different")
	}
	if c.RateLimit.Enabled && c.RateLimit.DefaultRPS <= 0 {
		return fmt.Errorf("rate_limit.default_rps must be positive when rate limiting is enabled")
	}
	return nil
}
