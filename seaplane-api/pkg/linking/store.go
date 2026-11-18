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

// Package linking implements the PoC account-linking store: a DynamoDB
// table mapping a Red Hat identity (token subject) to the AWS ARNs it may
// operate as. Schema and semantics are decided in
// docs/poc/preflight-ui-login/poc-decisions.md (D-T4):
//
//	PK "sub" (token subject), SK "arn" — one item per linked ARN,
//	attributes: arn, accountId, linkedAt. Revocation is a delete; the
//	authorizer treats missing and deleted identically.
//
// Org ownership, export-control enforcement, and link limits are out of
// PoC scope and belong to the real authz linking design.
package linking

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	// AttrSubject is the partition key: the verified token subject.
	AttrSubject = "sub"
	// AttrARN is the sort key and the linked AWS ARN.
	AttrARN = "arn"
)

// Link is one linked ARN for a subject.
type Link struct {
	Subject   string    `dynamodbav:"sub"`
	ARN       string    `dynamodbav:"arn"`
	AccountID string    `dynamodbav:"accountId"`
	LinkedAt  time.Time `dynamodbav:"linkedAt"`
}

// Store is the persistence interface for account links. The DynamoDB
// implementation is NewStore; tests use in-memory fakes.
type Store interface {
	// PutLink links arn to subject. Linking the same pair twice succeeds.
	PutLink(ctx context.Context, subject, arn string) error
	// DeleteLink revokes arn for subject. Deleting a missing link succeeds.
	DeleteLink(ctx context.Context, subject, arn string) error
	// ListLinks returns the ARNs linked to subject.
	ListLinks(ctx context.Context, subject string) ([]Link, error)
}

// DynamoStore implements Store against the linking table.
type DynamoStore struct {
	client    *dynamodb.Client
	tableName string
}

// NewStore creates a DynamoDB-backed linking store.
func NewStore(client *dynamodb.Client, tableName string) *DynamoStore {
	return &DynamoStore{client: client, tableName: tableName}
}

// arnAccountPattern matches IAM role and user ARNs, capturing the account ID.
var arnAccountPattern = regexp.MustCompile(`^arn:aws:iam::(\d{12}):(role|user)/[A-Za-z0-9+=,.@_/-]+$`)

// ValidateARN checks that arn is a well-formed IAM role or user ARN and
// returns its account ID.
func ValidateARN(arn string) (accountID string, err error) {
	m := arnAccountPattern.FindStringSubmatch(arn)
	if m == nil {
		return "", fmt.Errorf("arn must be an IAM role or user ARN")
	}
	return m[1], nil
}

// PutLink links arn to subject with a conditional write that tolerates an
// existing identical link (idempotent).
func (s *DynamoStore) PutLink(ctx context.Context, subject, arn string) error {
	accountID, err := ValidateARN(arn)
	if err != nil {
		return err
	}
	item := Link{Subject: subject, ARN: arn, AccountID: accountID, LinkedAt: time.Now().UTC()}
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return fmt.Errorf("marshal link: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("put link: %w", err)
	}
	return nil
}

// DeleteLink removes the link; a missing link is success.
func (s *DynamoStore) DeleteLink(ctx context.Context, subject, arn string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			AttrSubject: &dynamodbtypes.AttributeValueMemberS{Value: subject},
			AttrARN:     &dynamodbtypes.AttributeValueMemberS{Value: arn},
		},
	})
	if err != nil {
		var notFound *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("delete link: %w", err)
	}
	return nil
}

// ListLinks returns all links for subject, newest first.
func (s *DynamoStore) ListLinks(ctx context.Context, subject string) ([]Link, error) {
	resp, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("#s = :sub"),
		ExpressionAttributeNames: map[string]string{
			"#s": AttrSubject,
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":sub": &dynamodbtypes.AttributeValueMemberS{Value: subject},
		},
		ScanIndexForward: aws.Bool(false),
	})
	if err != nil {
		return nil, fmt.Errorf("query links: %w", err)
	}
	links := make([]Link, 0, len(resp.Items))
	for _, item := range resp.Items {
		var link Link
		if err := attributevalue.UnmarshalMap(item, &link); err != nil {
			return nil, fmt.Errorf("unmarshal link: %w", err)
		}
		links = append(links, link)
	}
	return links, nil
}
