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

// Package authz implements the Phase 4 PoC Lambda authorizer for the
// custom-authorizer API Gateway.
//
// Two modes (docs/poc/preflight-ui-login/phase-4-ui-auth-path.md):
//
//   - Flow 2a (token-only): the request carries no selected ARN; the token
//     is verified and the request is allowed. The backend (the
//     /api/v0/account_links list endpoint) re-verifies the token for the
//     subject. Never cached.
//   - Flow 2b (token + selected ARN): the token is verified and the
//     selected ARN must be linked to the token subject in the DynamoDB
//     linking table. Decisions are cached in-process keyed on
//     (token, arn) with TTL = min(cacheTTL, token remaining validity) —
//     a cached decision never outlives its token.
//
// The authorizer emits callerArn, accountId, and sub in the authorizer
// context; the gateway maps them to the same identity headers the AWS_IAM
// gateway produces (see identity-header-contract.md).
package authz

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/linking"
)

// Headers the authorizer reads. Authorization carries the bearer token;
// SelectedARNHeader carries the UI's selected ARN (Flow 2b only).
const (
	authorizationHeader = "Authorization"
	SelectedARNHeader   = "X-Seaplane-Arn"
)

// LinkLookup is the subset of the linking store the authorizer needs: a
// point lookup by composite key, so Flow 2b costs one DynamoDB GetItem
// regardless of how many ARNs the subject has linked.
type LinkLookup interface {
	GetLink(ctx context.Context, subject, arn string) (bool, error)
}

// Authorizer handles API Gateway REQUEST authorizer events.
type Authorizer struct {
	verifier linking.ClaimsVerifier
	links    LinkLookup
	cache    *decisionCache
	logger   *slog.Logger
}

// New creates an Authorizer. cacheTTL bounds Flow 2b decision caching;
// decisions also expire with their tokens.
func New(verifier linking.ClaimsVerifier, links LinkLookup, cacheTTL time.Duration, maxEntries int, logger *slog.Logger) *Authorizer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Authorizer{
		verifier: verifier,
		links:    links,
		cache:    newDecisionCache(maxEntries),
		logger:   logger,
	}
}

// Handle processes one authorizer event.
func (a *Authorizer) Handle(ctx context.Context, req events.APIGatewayCustomAuthorizerRequestTypeRequest) (events.APIGatewayCustomAuthorizerResponse, error) {
	token := bearerToken(req.Headers[authorizationHeader])
	if token == "" {
		return deny(), nil
	}
	selectedARN := strings.TrimSpace(req.Headers[SelectedARNHeader])

	// Flow 2a: token-only. Verify and allow; the backend derives the
	// subject from the token itself. Never cached.
	if selectedARN == "" {
		vt, err := a.verifier.VerifyClaims(ctx, token)
		if err != nil {
			a.logger.Warn("token verification failed", "error", err)
			return deny(), nil
		}
		return allow(req, vt.Subject, "", ""), nil
	}

	// Flow 2b: token + selected ARN, cached.
	if cached, ok := a.cache.get(token, selectedARN); ok {
		if !cached.allow {
			return deny(), nil
		}
		return allow(req, cached.subject, cached.callerARN, cached.accountID), nil
	}

	vt, err := a.verifier.VerifyClaims(ctx, token)
	if err != nil {
		a.logger.Warn("token verification failed", "error", err)
		return deny(), nil
	}
	accountID, err := linking.ValidateARN(selectedARN)
	if err != nil {
		a.logger.Warn("malformed selected arn", "error", err)
		return deny(), nil
	}

	linked, err := a.isLinked(ctx, vt.Subject, selectedARN)
	if err != nil {
		// Fail closed on store errors.
		a.logger.Error("link lookup failed", "error", err)
		return deny(), nil
	}
	if !linked {
		// Negative decisions get a much shorter TTL than allows: a stale
		// deny would block a freshly linked ARN for the whole window, and
		// the point read being skipped is cheap.
		a.cache.put(token, selectedARN, decision{allow: false}, time.Now().Add(denyTTL(a.cache.ttl)))
		return deny(), nil
	}

	d := decision{allow: true, subject: vt.Subject, callerARN: selectedARN, accountID: accountID}
	a.cache.put(token, selectedARN, d, a.cache.expiry(vt.ExpiresAt))
	return allow(req, d.subject, d.callerARN, d.accountID), nil
}

// isLinked reports whether subject has a link to arn (point lookup).
func (a *Authorizer) isLinked(ctx context.Context, subject, arn string) (bool, error) {
	return a.links.GetLink(ctx, subject, arn)
}

// denyTTL bounds negative-decision caching: the smaller of 30 seconds and
// the allow-TTL. Stale denials lock users out of freshly linked ARNs, so
// they expire quickly; DynamoDB point reads are cheap enough that the
// extra lookups are acceptable.
func denyTTL(allowTTL time.Duration) time.Duration {
	const maxDenyTTL = 30 * time.Second
	if allowTTL < maxDenyTTL {
		return allowTTL
	}
	return maxDenyTTL
}

func bearerToken(header string) string {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func deny() events.APIGatewayCustomAuthorizerResponse {
	return events.APIGatewayCustomAuthorizerResponse{
		PrincipalID: "denied",
		PolicyDocument: events.APIGatewayCustomAuthorizerPolicy{
			Version:   "2012-10-17",
			Statement: []events.IAMPolicyStatement{denyStatement()},
		},
	}
}

func allow(req events.APIGatewayCustomAuthorizerRequestTypeRequest, subject, callerARN, accountID string) events.APIGatewayCustomAuthorizerResponse {
	return events.APIGatewayCustomAuthorizerResponse{
		PrincipalID: subject,
		PolicyDocument: events.APIGatewayCustomAuthorizerPolicy{
			Version:   "2012-10-17",
			Statement: []events.IAMPolicyStatement{allowStatement(req)},
		},
		Context: map[string]interface{}{
			"sub":       subject,
			"callerArn": callerARN,
			"accountId": accountID,
		},
	}
}

func allowStatement(req events.APIGatewayCustomAuthorizerRequestTypeRequest) events.IAMPolicyStatement {
	return events.IAMPolicyStatement{
		Action:   []string{"execute-api:Invoke"},
		Effect:   "Allow",
		Resource: []string{methodARN(req)},
	}
}

func denyStatement() events.IAMPolicyStatement {
	return events.IAMPolicyStatement{
		Action:   []string{"execute-api:Invoke"},
		Effect:   "Deny",
		Resource: []string{"*"},
	}
}

// methodARN rebuilds the method ARN for the invoked API. REST API
// REQUEST authorizer events carry the fragments in TypeRequestId-era
// fields; assemble from methodArn when present.
func methodARN(req events.APIGatewayCustomAuthorizerRequestTypeRequest) string {
	if req.MethodArn != "" {
		return req.MethodArn
	}
	return fmt.Sprintf("arn:aws:execute-api:*:*:*")
}

