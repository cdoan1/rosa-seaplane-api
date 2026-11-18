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
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestValidateARN(t *testing.T) {
	if _, err := ValidateARN("arn:aws:iam::123456789012:role/Installer"); err != nil {
		t.Errorf("role ARN should validate: %v", err)
	}
	if _, err := ValidateARN("arn:aws:iam::123456789012:user/alice"); err != nil {
		t.Errorf("user ARN should validate: %v", err)
	}
	for _, bad := range []string{
		"", "not-an-arn", "arn:aws:iam::12345:role/x",
		"arn:aws:iam::123456789012:role/", "arn:aws:s3:::bucket",
		"arn:aws:iam::123456789012:policy/P",
	} {
		if _, err := ValidateARN(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

// startJWKSServes signs tokens with a fresh RSA key and serves its JWKS.
func startJWKSServer(t *testing.T) (key *rsa.PrivateKey, kid, jwksURL string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	kid = "test-kid-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kid": kid,
				"kty": "RSA",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(bigIntBytes(key.E)),
			}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return key, kid, srv.URL + "/jwks"
}

func bigIntBytes(n int) []byte {
	// Encode a small int as big-endian bytes, as JWKS expects for "e".
	if n == 0 {
		return []byte{0}
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte(n & 0xff)}, out...)
		n >>= 8
	}
	return out
}

func mintToken(t *testing.T, key *rsa.PrivateKey, kid, issuer, subject string, expires time.Time) string {
	t.Helper()
	claims := &jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   subject,
		Audience:  jwt.ClaimStrings{"seaplane-console"},
		ExpiresAt: jwt.NewNumericDate(expires),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func TestJWTVerifier(t *testing.T) {
	key, kid, jwksURL := startJWKSServer(t)
	verifier := NewJWTVerifier(TokenConfig{
		Issuer:   "https://sso.example.test/realms/poc",
		JWKSURL:  jwksURL,
		Audience: "seaplane-console",
	})
	sub := "user-1"

	t.Run("valid token", func(t *testing.T) {
		tok := mintToken(t, key, kid, "https://sso.example.test/realms/poc", sub, time.Now().Add(10*time.Minute))
		got, err := verifier.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if got != sub {
			t.Fatalf("expected subject %q, got %q", sub, got)
		}
	})

	t.Run("cached jwks reuse", func(t *testing.T) {
		// A second verification must be served from cache (same key works).
		tok := mintToken(t, key, kid, "https://sso.example.test/realms/poc", sub, time.Now().Add(10*time.Minute))
		if _, err := verifier.Verify(context.Background(), tok); err != nil {
			t.Fatalf("cached verify: %v", err)
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		tok := mintToken(t, key, kid, "https://evil.example.test", sub, time.Now().Add(10*time.Minute))
		if _, err := verifier.Verify(context.Background(), tok); err == nil {
			t.Fatal("expected issuer mismatch to fail")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		tok := mintToken(t, key, kid, "https://sso.example.test/realms/poc", sub, time.Now().Add(-time.Minute))
		if _, err := verifier.Verify(context.Background(), tok); err == nil {
			t.Fatal("expected expired token to fail")
		}
	})

	t.Run("garbage token", func(t *testing.T) {
		if _, err := verifier.Verify(context.Background(), "not.a.jwt"); err == nil {
			t.Fatal("expected garbage token to fail")
		}
	})

	t.Run("unknown kid", func(t *testing.T) {
		tok := mintToken(t, key, "other-kid", "https://sso.example.test/realms/poc", sub, time.Now().Add(10*time.Minute))
		if _, err := verifier.Verify(context.Background(), tok); err == nil {
			t.Fatal("expected unknown kid to fail")
		}
	})
}

func TestJWTVerifierUnconfigured(t *testing.T) {
	verifier := NewJWTVerifier(TokenConfig{})
	if _, err := verifier.Verify(context.Background(), "x"); err == nil {
		t.Fatal("expected unconfigured verifier to fail")
	}
}
