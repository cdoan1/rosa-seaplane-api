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

// The Phase 4 PoC Lambda authorizer entrypoint. Configuration is
// environment variables (wired by the custom-authorizer-gateway terraform
// module in rosa-seaplane):
//
//	LINKING_TABLE    — DynamoDB link table name
//	RH_TOKEN_ISSUER  — expected token issuer (the PoC Keycloak via CloudFront)
//	RH_TOKEN_JWKS_URL — the issuer's JWKS endpoint
//	RH_TOKEN_AUDIENCE — expected audience (optional)
//	AUTHZ_CACHE_TTL  — Flow 2b decision cache TTL (default 5m)
package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/linking"
	authorize "github.com/openshift-online/rosa-seaplane-api/seaplane-authorizer/internal/authz"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	issuer := os.Getenv("RH_TOKEN_ISSUER")
	jwksURL := os.Getenv("RH_TOKEN_JWKS_URL")
	table := os.Getenv("LINKING_TABLE")
	if issuer == "" || jwksURL == "" || table == "" {
		logger.Error("missing required configuration: RH_TOKEN_ISSUER, RH_TOKEN_JWKS_URL, LINKING_TABLE")
		os.Exit(1)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		logger.Error("failed to load AWS config", "error", err)
		os.Exit(1)
	}

	verifier := linking.NewJWTVerifier(linking.TokenConfig{
		Issuer:   issuer,
		JWKSURL:  jwksURL,
		Audience: os.Getenv("RH_TOKEN_AUDIENCE"),
	})
	store := linking.NewStore(dynamodb.NewFromConfig(awsCfg), table)

	ttl := 5 * time.Minute
	if v := os.Getenv("AUTHZ_CACHE_TTL"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil && seconds > 0 {
			ttl = time.Duration(seconds) * time.Second
		}
	}

	a := authorize.New(verifier, store, ttl, 4096, logger)
	lambda.StartWithOptions(func(ctx context.Context, req events.APIGatewayCustomAuthorizerRequestTypeRequest) (events.APIGatewayCustomAuthorizerResponse, error) {
		return a.Handle(ctx, req)
	}, lambda.WithContext(context.Background()))
}
