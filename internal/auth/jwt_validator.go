// Package auth provides JWT validation and token caching for the API gateway.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// Claims represents the JWT claims extracted from a validated token.
type Claims struct {
	UserID      string   `json:"sub"`
	Email       string   `json:"email"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	Issuer      string   `json:"iss"`
	Audiences   []string `json:"aud"`
	ExpiresAt   time.Time
	IssuedAt    time.Time
}

// JWKSResponse represents the JSON Web Key Set from the auth service.
type JWKSResponse struct {
	Keys []JWK `json:"keys"`
}

// JWK represents a single JSON Web Key.
type JWK struct {
	KeyID     string `json:"kid"`
	KeyType   string `json:"kty"`
	Algorithm string `json:"alg"`
	Use       string `json:"use"`
	N         string `json:"n"`
	E         string `json:"e"`
}

// jwksCacheEntry holds a fetched JWKS with its expiry time.
type jwksCacheEntry struct {
	keys      map[string]interface{}
	expiresAt time.Time
}

// Validator validates JWT tokens against a remote JWKS endpoint.
type Validator struct {
	jwkURL    string
	issuer    string
	audiences []string
	client    *http.Client
	logger    *zap.Logger

	mu        sync.RWMutex
	jwksCache *jwksCacheEntry
	cacheTTL  time.Duration

	// hmacSecret is used for HS256 tokens (optional).
	hmacSecret []byte
}

// ValidatorConfig holds configuration for the JWT Validator.
type ValidatorConfig struct {
	JWKURL     string
	Issuer     string
	Audiences  []string
	CacheTTL   time.Duration
	HTTPClient *http.Client
	HMACSecret []byte
	Logger     *zap.Logger
}

// NewValidator creates a new JWT Validator.
func NewValidator(cfg ValidatorConfig) *Validator {
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Minute
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Validator{
		jwkURL:     cfg.JWKURL,
		issuer:     cfg.Issuer,
		audiences:  cfg.Audiences,
		client:     cfg.HTTPClient,
		logger:     cfg.Logger,
		cacheTTL:   cfg.CacheTTL,
		hmacSecret: cfg.HMACSecret,
	}
}

// Validate parses and validates the given JWT token string.
// It returns the extracted Claims on success.
func (v *Validator) Validate(ctx context.Context, tokenStr string) (*Claims, error) {
	token, err := jwt.Parse(tokenStr,
		v.keyFunc,
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("token is not valid")
	}

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	claims, err := v.extractClaims(mapClaims)
	if err != nil {
		return nil, fmt.Errorf("extracting claims: %w", err)
	}

	if err := v.validateAudience(claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// keyFunc returns the appropriate signing key for a given token.
func (v *Validator) keyFunc(token *jwt.Token) (interface{}, error) {
	switch token.Method.(type) {
	case *jwt.SigningMethodHMAC:
		if len(v.hmacSecret) == 0 {
			return nil, fmt.Errorf("HS256 token received but no HMAC secret configured")
		}
		return v.hmacSecret, nil

	case *jwt.SigningMethodRSA:
		return v.rsaKeyFromToken(token)

	default:
		return nil, fmt.Errorf("unsupported signing method: %v", token.Header["alg"])
	}
}

// rsaKeyFromToken fetches the RSA public key matching the token's key ID.
func (v *Validator) rsaKeyFromToken(token *jwt.Token) (interface{}, error) {
	kid, _ := token.Header["kid"].(string)

	keys, err := v.getJWKS(context.Background())
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS: %w", err)
	}

	key, ok := keys[kid]
	if !ok {
		// Try refreshing if key not found.
		v.invalidateCache()
		keys, err = v.getJWKS(context.Background())
		if err != nil {
			return nil, fmt.Errorf("refreshing JWKS: %w", err)
		}
		key, ok = keys[kid]
		if !ok {
			return nil, fmt.Errorf("key %q not found in JWKS", kid)
		}
	}

	return key, nil
}

// getJWKS returns the cached JWKS or fetches it from the remote endpoint.
func (v *Validator) getJWKS(ctx context.Context) (map[string]interface{}, error) {
	v.mu.RLock()
	if v.jwksCache != nil && time.Now().Before(v.jwksCache.expiresAt) {
		keys := v.jwksCache.keys
		v.mu.RUnlock()
		return keys, nil
	}
	v.mu.RUnlock()

	// Upgrade to write lock to refresh.
	v.mu.Lock()
	defer v.mu.Unlock()

	// Double-check after acquiring write lock.
	if v.jwksCache != nil && time.Now().Before(v.jwksCache.expiresAt) {
		return v.jwksCache.keys, nil
	}

	keys, err := v.fetchJWKS(ctx)
	if err != nil {
		return nil, err
	}

	v.jwksCache = &jwksCacheEntry{
		keys:      keys,
		expiresAt: time.Now().Add(v.cacheTTL),
	}
	return keys, nil
}

// fetchJWKS retrieves and parses the JWKS from the remote endpoint.
func (v *Validator) fetchJWKS(ctx context.Context) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwkURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building JWKS request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS from %q: %w", v.jwkURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks JWKSResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("decoding JWKS response: %w", err)
	}

	keys := make(map[string]interface{}, len(jwks.Keys))
	for _, jwk := range jwks.Keys {
		key, err := jwkToPublicKey(jwk)
		if err != nil {
			v.logger.Warn("skipping invalid JWK", zap.String("kid", jwk.KeyID), zap.Error(err))
			continue
		}
		keys[jwk.KeyID] = key
	}
	return keys, nil
}

// invalidateCache clears the JWKS cache to force a refresh.
func (v *Validator) invalidateCache() {
	v.mu.Lock()
	v.jwksCache = nil
	v.mu.Unlock()
}

// extractClaims maps JWT MapClaims to our Claims struct.
func (v *Validator) extractClaims(mc jwt.MapClaims) (*Claims, error) { //nolint:gocognit,unparam
	claims := &Claims{}

	if sub, ok := mc["sub"].(string); ok {
		claims.UserID = sub
	}
	if email, ok := mc["email"].(string); ok {
		claims.Email = email
	}
	if iss, ok := mc["iss"].(string); ok {
		claims.Issuer = iss
	}

	if roles, ok := mc["roles"].([]interface{}); ok {
		for _, r := range roles {
			if s, ok := r.(string); ok {
				claims.Roles = append(claims.Roles, s)
			}
		}
	}

	if perms, ok := mc["permissions"].([]interface{}); ok {
		for _, p := range perms {
			if s, ok := p.(string); ok {
				claims.Permissions = append(claims.Permissions, s)
			}
		}
	}

	switch aud := mc["aud"].(type) {
	case string:
		claims.Audiences = []string{aud}
	case []interface{}:
		for _, a := range aud {
			if s, ok := a.(string); ok {
				claims.Audiences = append(claims.Audiences, s)
			}
		}
	}

	if exp, ok := mc["exp"].(float64); ok {
		claims.ExpiresAt = time.Unix(int64(exp), 0)
	}
	if iat, ok := mc["iat"].(float64); ok {
		claims.IssuedAt = time.Unix(int64(iat), 0)
	}

	return claims, nil
}

// validateAudience checks that at least one expected audience is present in the token.
func (v *Validator) validateAudience(claims *Claims) error {
	if len(v.audiences) == 0 {
		return nil
	}
	for _, expected := range v.audiences {
		for _, got := range claims.Audiences {
			if got == expected {
				return nil
			}
		}
	}
	return fmt.Errorf("token audience %v does not match expected %v", claims.Audiences, v.audiences)
}

// jwkToPublicKey converts a JWK into an *rsa.PublicKey.
func jwkToPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	if jwk.KeyType != "RSA" {
		return nil, fmt.Errorf("unsupported key type %q", jwk.KeyType)
	}

	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("decoding modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("decoding exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	eInt := new(big.Int).SetBytes(eBytes)

	return &rsa.PublicKey{
		N: n,
		E: int(eInt.Int64()),
	}, nil
}
