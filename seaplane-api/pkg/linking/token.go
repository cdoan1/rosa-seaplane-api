/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package linking

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenVerifier verifies Red Hat identity tokens (JWT) and returns the
// subject claim. The PoC issuer is the Phase 4 Keycloak behind CloudFront
// (D-P4-token); the issuer/JWKS/audience are configuration so swapping to
// real Red Hat SSO is a config change.
type TokenVerifier interface {
	// Verify validates the token's signature (against the configured
	// issuer's JWKS), issuer, audience, and expiry, and returns its subject.
	Verify(ctx context.Context, token string) (subject string, err error)
}

// VerifiedToken is the outcome of a successful token verification.
type VerifiedToken struct {
	Subject   string
	ExpiresAt time.Time
}

// ClaimsVerifier verifies tokens and exposes the expiry, so callers (the
// Lambda authorizer) can bound cache TTLs by the token's remaining life.
type ClaimsVerifier interface {
	VerifyClaims(ctx context.Context, token string) (VerifiedToken, error)
}

// TokenConfig configures JWT verification.
type TokenConfig struct {
	Issuer   string // expected iss claim
	JWKSURL  string // issuer's JWKS endpoint
	Audience string // expected aud claim; empty skips audience checking
}

// JWTVerifier implements TokenVerifier with a cached JWKS fetch.
type JWTVerifier struct {
	cfg  TokenConfig
	http *http.Client

	mu      sync.Mutex
	jwks    jwksDocument
	jwksAt  time.Time
	jwksTTL time.Duration
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// NewJWTVerifier creates a verifier with a 5-minute JWKS cache.
func NewJWTVerifier(cfg TokenConfig) *JWTVerifier {
	return &JWTVerifier{
		cfg:     cfg,
		http:    &http.Client{Timeout: 10 * time.Second},
		jwksTTL: 5 * time.Minute,
	}
}

// VerifyClaims implements ClaimsVerifier.
func (v *JWTVerifier) VerifyClaims(ctx context.Context, token string) (VerifiedToken, error) {
	if v.cfg.Issuer == "" || v.cfg.JWKSURL == "" {
		return VerifiedToken{}, errors.New("token verification is not configured (issuer/JWKS)")
	}

	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		key, err := v.key(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	}, jwt.WithIssuer(v.cfg.Issuer), jwt.WithExpirationRequired(), jwt.WithAudience(v.cfg.Audience))
	if err != nil {
		return VerifiedToken{}, fmt.Errorf("verify token: %w", err)
	}
	if claims.Subject == "" {
		return VerifiedToken{}, errors.New("token has no subject claim")
	}
	return VerifiedToken{Subject: claims.Subject, ExpiresAt: claims.ExpiresAt.Time}, nil
}

// Verify implements TokenVerifier.
func (v *JWTVerifier) Verify(ctx context.Context, token string) (string, error) {
	vt, err := v.VerifyClaims(ctx, token)
	return vt.Subject, err
}

// key resolves the RSA public key for kid, refreshing the cached JWKS when
// the kid is unknown or the cache has expired.
func (v *JWTVerifier) key(ctx context.Context, kid string) (any, error) {
	if key, ok := v.cachedKey(kid); ok {
		return key, nil
	}
	if err := v.fetchJWKS(ctx); err != nil {
		return nil, err
	}
	if key, ok := v.cachedKey(kid); ok {
		return key, nil
	}
	return nil, fmt.Errorf("no JWKS key for kid %q", kid)
}

func (v *JWTVerifier) cachedKey(kid string) (any, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.jwksAt) > v.jwksTTL || len(v.jwks.Keys) == 0 {
		return nil, false
	}
	return v.jwks.rsaKey(kid)
}

func (v *JWTVerifier) fetchJWKS(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return fmt.Errorf("build JWKS request: %w", err)
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: status %d", resp.StatusCode)
	}
	var doc jwksDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}
	v.mu.Lock()
	v.jwks = doc
	v.jwksAt = time.Now()
	v.mu.Unlock()
	return nil
}

func (d *jwksDocument) rsaKey(kid string) (any, bool) {
	for _, k := range d.Keys {
		if k.Kid == kid && k.Kty == "RSA" {
			key, err := rsaFromJWK(k)
			if err == nil {
				return key, true
			}
		}
	}
	return nil, false
}

// rsaFromJWK converts an RSA JWK's base64url n/e into an rsa.PublicKey.
func rsaFromJWK(k jwk) (*rsa.PublicKey, error) {
	if k.N == "" || k.E == "" {
		return nil, errors.New("jwk missing n/e")
	}
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode jwk modulus: %w", err)
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode jwk exponent: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nb),
		E: int(new(big.Int).SetBytes(eb).Int64()),
	}, nil
}
