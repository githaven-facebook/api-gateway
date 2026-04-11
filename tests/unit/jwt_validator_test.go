package unit_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/nicedavid98/api-gateway/internal/auth"
)

func TestJWTValidator(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	const kid = "test-key-1"
	const issuer = "https://auth.test.internal"
	const audience = "api-gateway"

	// Start mock JWKS server.
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
		eBytes := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
		e := base64.RawURLEncoding.EncodeToString(eBytes)

		resp := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kid": kid,
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"n":   n,
					"e":   e,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer jwksServer.Close()

	validator := auth.NewValidator(auth.ValidatorConfig{
		JWKURL:    jwksServer.URL,
		Issuer:    issuer,
		Audiences: []string{audience},
		CacheTTL:  time.Minute,
	})

	makeToken := func(claims jwt.MapClaims, key interface{}) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = kid
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatalf("signing token: %v", err)
		}
		return signed
	}

	validClaims := jwt.MapClaims{
		"sub":   "user-123",
		"email": "user@example.com",
		"iss":   issuer,
		"aud":   audience,
		"iat":   time.Now().Add(-time.Minute).Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
		"roles": []string{"user"},
	}

	tests := []struct {
		name    string
		token   func() string
		wantErr bool
		check   func(t *testing.T, claims *auth.Claims)
	}{
		{
			name: "valid token",
			token: func() string {
				return makeToken(validClaims, privateKey)
			},
			wantErr: false,
			check: func(t *testing.T, claims *auth.Claims) {
				t.Helper()
				if claims.UserID != "user-123" {
					t.Errorf("expected UserID=user-123, got %q", claims.UserID)
				}
				if claims.Email != "user@example.com" {
					t.Errorf("expected Email=user@example.com, got %q", claims.Email)
				}
			},
		},
		{
			name: "expired token",
			token: func() string {
				c := jwt.MapClaims{
					"sub": "user-123",
					"iss": issuer,
					"aud": audience,
					"iat": time.Now().Add(-2 * time.Hour).Unix(),
					"exp": time.Now().Add(-time.Hour).Unix(),
				}
				return makeToken(c, privateKey)
			},
			wantErr: true,
		},
		{
			name: "wrong issuer",
			token: func() string {
				c := jwt.MapClaims{
					"sub": "user-123",
					"iss": "https://evil.example.com",
					"aud": audience,
					"iat": time.Now().Add(-time.Minute).Unix(),
					"exp": time.Now().Add(time.Hour).Unix(),
				}
				return makeToken(c, privateKey)
			},
			wantErr: true,
		},
		{
			name: "invalid signature",
			token: func() string {
				otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
				return makeToken(validClaims, otherKey)
			},
			wantErr: true,
		},
		{
			name: "wrong audience",
			token: func() string {
				c := jwt.MapClaims{
					"sub": "user-123",
					"iss": issuer,
					"aud": "other-service",
					"iat": time.Now().Add(-time.Minute).Unix(),
					"exp": time.Now().Add(time.Hour).Unix(),
				}
				return makeToken(c, privateKey)
			},
			wantErr: true,
		},
		{
			name:    "empty token",
			token:   func() string { return "" },
			wantErr: true,
		},
		{
			name:    "malformed token",
			token:   func() string { return "not.a.jwt" },
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := validator.Validate(context.Background(), tc.token())
			if tc.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, claims)
			}
		})
	}
}
