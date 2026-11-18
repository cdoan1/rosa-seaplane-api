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

package authz

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/linking"
)

const (
	testSub       = "user-1"
	testARN       = "arn:aws:iam::123456789012:role/Installer-Role"
	testAccount   = "123456789012"
	otherARN      = "arn:aws:iam::123456789012:role/Other"
	testMethodARN = "arn:aws:execute-api:us-east-1:123:api/stage/GET/"
)

// fakeLinks is an in-memory LinkLookup.
type fakeLinks struct {
	links []linking.Link
	calls int
}

func (f *fakeLinks) ListLinks(_ context.Context, subject string) ([]linking.Link, error) {
	f.calls++
	var out []linking.Link
	for _, l := range f.links {
		if l.Subject == subject {
			out = append(out, l)
		}
	}
	return out, nil
}

// fakeVerifier verifies any token whose value is "good".
type fakeVerifier struct{ expiry time.Time }

func (f fakeVerifier) VerifyClaims(_ context.Context, token string) (linking.VerifiedToken, error) {
	if token != "good" || time.Now().After(f.expiry) {
		return linking.VerifiedToken{}, io.ErrUnexpectedEOF
	}
	return linking.VerifiedToken{Subject: testSub, ExpiresAt: f.expiry}, nil
}

func newTestAuthorizer(t *testing.T, links []linking.Link, expiry time.Time) (*Authorizer, *fakeLinks) {
	t.Helper()
	fl := &fakeLinks{links: links}
	return New(fakeVerifier{expiry: expiry}, fl, 5*time.Minute, 4096,
		slog.New(slog.NewTextHandler(io.Discard, nil))), fl
}

func request(token, arn string) events.APIGatewayCustomAuthorizerRequestTypeRequest {
	req := events.APIGatewayCustomAuthorizerRequestTypeRequest{
		MethodArn: testMethodARN,
		Headers:   map[string]string{},
	}
	if token != "" {
		req.Headers["Authorization"] = "Bearer " + token
	}
	if arn != "" {
		req.Headers[SelectedARNHeader] = arn
	}
	return req
}

func effect(resp events.APIGatewayCustomAuthorizerResponse) string {
	return resp.PolicyDocument.Statement[0].Effect
}

func TestFlow2aTokenOnlyAllows(t *testing.T) {
	a, fl := newTestAuthorizer(t, nil, time.Now().Add(time.Hour))
	resp, err := a.Handle(context.Background(), request("good", ""))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if effect(resp) != "Allow" {
		t.Fatalf("expected Allow, got %s", effect(resp))
	}
	if resp.Context["sub"] != testSub || resp.Context["callerArn"] != "" {
		t.Fatalf("unexpected context: %v", resp.Context)
	}
	if fl.calls != 0 {
		t.Fatal("Flow 2a must not consult the link store")
	}
}

func TestFlow2aBadTokenDenies(t *testing.T) {
	a, _ := newTestAuthorizer(t, nil, time.Now().Add(time.Hour))
	resp, _ := a.Handle(context.Background(), request("bad", ""))
	if effect(resp) != "Deny" {
		t.Fatalf("expected Deny, got %s", effect(resp))
	}
}

func TestFlow2aMissingTokenDenies(t *testing.T) {
	a, _ := newTestAuthorizer(t, nil, time.Now().Add(time.Hour))
	resp, _ := a.Handle(context.Background(), request("", ""))
	if effect(resp) != "Deny" {
		t.Fatalf("expected Deny, got %s", effect(resp))
	}
}

func TestFlow2bLinkedARNAllows(t *testing.T) {
	a, _ := newTestAuthorizer(t,
		[]linking.Link{{Subject: testSub, ARN: testARN, AccountID: testAccount}},
		time.Now().Add(time.Hour))
	resp, err := a.Handle(context.Background(), request("good", testARN))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if effect(resp) != "Allow" {
		t.Fatalf("expected Allow, got %s", effect(resp))
	}
	if resp.Context["callerArn"] != testARN || resp.Context["accountId"] != testAccount {
		t.Fatalf("unexpected context: %v", resp.Context)
	}
}

func TestFlow2bUnlinkedARNDenies(t *testing.T) {
	a, _ := newTestAuthorizer(t,
		[]linking.Link{{Subject: testSub, ARN: testARN, AccountID: testAccount}},
		time.Now().Add(time.Hour))
	resp, _ := a.Handle(context.Background(), request("good", otherARN))
	if effect(resp) != "Deny" {
		t.Fatalf("expected Deny, got %s", effect(resp))
	}
}

func TestFlow2bMalformedARNDenies(t *testing.T) {
	a, _ := newTestAuthorizer(t, nil, time.Now().Add(time.Hour))
	resp, _ := a.Handle(context.Background(), request("good", "not-an-arn"))
	if effect(resp) != "Deny" {
		t.Fatalf("expected Deny, got %s", effect(resp))
	}
}

func TestFlow2bCachedDecisionSkipsLookup(t *testing.T) {
	a, fl := newTestAuthorizer(t,
		[]linking.Link{{Subject: testSub, ARN: testARN, AccountID: testAccount}},
		time.Now().Add(time.Hour))
	if _, err := a.Handle(context.Background(), request("good", testARN)); err != nil {
		t.Fatalf("first handle: %v", err)
	}
	if fl.calls != 1 {
		t.Fatalf("expected 1 lookup, got %d", fl.calls)
	}
	resp, err := a.Handle(context.Background(), request("good", testARN))
	if err != nil {
		t.Fatalf("cached handle: %v", err)
	}
	if effect(resp) != "Allow" {
		t.Fatalf("expected Allow, got %s", effect(resp))
	}
	if fl.calls != 1 {
		t.Fatalf("cached decision must skip the lookup; got %d calls", fl.calls)
	}
}

func TestFlow2bCacheHonorsRevokedLinkWithinWindow(t *testing.T) {
	// Documented bounded staleness: a decision stays cached until the token
	// or TTL expires even if the link is revoked mid-window.
	a, fl := newTestAuthorizer(t,
		[]linking.Link{{Subject: testSub, ARN: testARN, AccountID: testAccount}},
		time.Now().Add(time.Hour))
	_, _ = a.Handle(context.Background(), request("good", testARN))
	fl.links = nil // revoke everything
	resp, _ := a.Handle(context.Background(), request("good", testARN))
	if effect(resp) != "Allow" {
		t.Fatalf("expected cached Allow inside the staleness window, got %s", effect(resp))
	}
}

func TestFlow2bCacheExpiresWithToken(t *testing.T) {
	// A decision cached with a near-expiry token must expire with it.
	a, _ := newTestAuthorizer(t,
		[]linking.Link{{Subject: testSub, ARN: testARN, AccountID: testAccount}},
		time.Now().Add(50*time.Millisecond))
	_, _ = a.Handle(context.Background(), request("good", testARN))
	time.Sleep(80 * time.Millisecond)
	resp, _ := a.Handle(context.Background(), request("good", testARN))
	// Token now expired: verifier fails, deny.
	if effect(resp) != "Deny" {
		t.Fatalf("expected Deny for expired token, got %s", effect(resp))
	}
}

func TestFlow2aNeverCached(t *testing.T) {
	a, _ := newTestAuthorizer(t, nil, time.Now().Add(time.Hour))
	if _, err := a.Handle(context.Background(), request("good", "")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(a.cache.entries) != 0 {
		t.Fatal("Flow 2a decisions must never be cached")
	}
}

func TestDecisionCacheExpiryBoundedByTTL(t *testing.T) {
	c := newDecisionCache(8)
	exp := c.expiry(time.Now().Add(time.Hour))
	if time.Until(exp) > c.ttl+time.Second {
		t.Fatal("expiry must not exceed the TTL")
	}
	tokenExpiry := time.Now().Add(time.Minute)
	exp = c.expiry(tokenExpiry)
	if !exp.Before(time.Now().Add(2 * time.Minute)) {
		t.Fatal("expiry must not outlive the token")
	}
}

func TestDecisionCacheEvictsWhenFull(t *testing.T) {
	c := newDecisionCache(4)
	for i := 0; i < 10; i++ {
		c.put("tok", string(rune('a'+i)), decision{allow: true}, time.Now().Add(time.Hour))
	}
	if len(c.entries) > 4 {
		t.Fatalf("cache must stay bounded, got %d entries", len(c.entries))
	}
}

// Compile-time: the HTTP server import is only used by helpers below if
