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

package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/linking"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/middleware"
)

// fakeStore is an in-memory linking.Store for handler tests.
type fakeStore struct {
	links map[string]linking.Link // key: subject + "\x00" + arn
	order []string
}

func newFakeStore() *fakeStore { return &fakeStore{links: map[string]linking.Link{}} }

func (f *fakeStore) PutLink(_ context.Context, subject, arn string) error {
	accountID, err := linking.ValidateARN(arn)
	if err != nil {
		return err
	}
	key := subject + "\x00" + arn
	if _, exists := f.links[key]; !exists {
		f.order = append(f.order, key)
	}
	f.links[key] = linking.Link{Subject: subject, ARN: arn, AccountID: accountID, LinkedAt: time.Now()}
	return nil
}

func (f *fakeStore) DeleteLink(_ context.Context, subject, arn string) error {
	delete(f.links, subject+"\x00"+arn)
	return nil
}

func (f *fakeStore) GetLink(_ context.Context, subject, arn string) (bool, error) {
	_, ok := f.links[subject+"\x00"+arn]
	return ok, nil
}

func (f *fakeStore) ListLinks(_ context.Context, subject string) ([]linking.Link, error) {
	var out []linking.Link
	for _, key := range f.order {
		if l, ok := f.links[key]; ok && l.Subject == subject {
			out = append(out, l)
		}
	}
	return out, nil
}

// fakeVerifier returns a fixed subject unless the token is "bad".
type fakeVerifier struct{ subject string }

func (f fakeVerifier) Verify(_ context.Context, token string) (string, error) {
	if token == "bad" {
		return "", io.ErrClosedPipe
	}
	return f.subject, nil
}

const (
	testAccount = "123456789012"
	testARN     = "arn:aws:iam::123456789012:role/Installer-Role"
)

func newLinkingTestServer(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewLinkingHandler(newFakeStore(), fakeVerifier{subject: "user-1"}, logger)
	r := mux.NewRouter()
	r.HandleFunc("/api/v0/account_links", h.List).Methods(http.MethodGet)
	r.HandleFunc("/api/v0/account_links", h.Link).Methods(http.MethodPost)
	r.HandleFunc("/api/v0/account_links", h.Unlink).Methods(http.MethodDelete)
	return r
}

func doLinkingRequest(t *testing.T, h http.Handler, method, target, rhToken, accountID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if rhToken != "" {
		req.Header.Set(RHTokenHeader, rhToken)
	}
	if strings.HasPrefix(target, "/") && accountID != "" {
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLinkingLink(t *testing.T) {
	h := newLinkingTestServer(t)

	rec := doLinkingRequest(t, h, http.MethodPost, "/api/v0/account_links", "tok", testAccount,
		`{"arn": "`+testARN+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLinkingLinkRequiresToken(t *testing.T) {
	h := newLinkingTestServer(t)
	rec := doLinkingRequest(t, h, http.MethodPost, "/api/v0/account_links", "", testAccount,
		`{"arn": "`+testARN+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestLinkingLinkRejectsInvalidToken(t *testing.T) {
	h := newLinkingTestServer(t)
	rec := doLinkingRequest(t, h, http.MethodPost, "/api/v0/account_links", "bad", testAccount,
		`{"arn": "`+testARN+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestLinkingLinkRequiresSigV4Identity(t *testing.T) {
	h := newLinkingTestServer(t)
	rec := doLinkingRequest(t, h, http.MethodPost, "/api/v0/account_links", "tok", "",
		`{"arn": "`+testARN+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestLinkingLinkRejectsCrossAccountARN(t *testing.T) {
	h := newLinkingTestServer(t)
	// ARN belongs to a different account than the SigV4 caller.
	rec := doLinkingRequest(t, h, http.MethodPost, "/api/v0/account_links", "tok", testAccount,
		`{"arn": "arn:aws:iam::999999999999:role/Other"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestLinkingLinkRejectsMalformedARN(t *testing.T) {
	h := newLinkingTestServer(t)
	rec := doLinkingRequest(t, h, http.MethodPost, "/api/v0/account_links", "tok", testAccount,
		`{"arn": "not-an-arn"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestLinkingListFlow2a(t *testing.T) {
	store := newFakeStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewLinkingHandler(store, fakeVerifier{subject: "user-1"}, logger)

	// Link two ARNs for user-1, one for another subject.
	_ = store.PutLink(context.Background(), "user-1", testARN)
	_ = store.PutLink(context.Background(), "user-1", "arn:aws:iam::123456789012:user/alice")
	_ = store.PutLink(context.Background(), "user-2", "arn:aws:iam::123456789012:role/Other")

	req := httptest.NewRequest(http.MethodGet, "/api/v0/account_links", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Arns []struct {
			ARN       string `json:"arn"`
			AccountID string `json:"accountId"`
		} `json:"arns"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Arns) != 2 {
		t.Fatalf("expected 2 links for user-1, got %d", len(resp.Arns))
	}
	if resp.Arns[0].ARN != testARN || resp.Arns[0].AccountID != testAccount {
		t.Fatalf("unexpected first link: %+v", resp.Arns[0])
	}
}

func TestLinkingListTokenOnly(t *testing.T) {
	// Flow 2a must work with no SigV4 identity at all — that is its purpose.
	h := newLinkingTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v0/account_links", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 without identity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLinkingUnlink(t *testing.T) {
	store := newFakeStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewLinkingHandler(store, fakeVerifier{subject: "user-1"}, logger)
	_ = store.PutLink(context.Background(), "user-1", testARN)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/account_links?arn="+testARN, nil)
	req.Header.Set(RHTokenHeader, "tok")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, testAccount))
	rec := httptest.NewRecorder()
	h.Unlink(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	links, _ := store.ListLinks(context.Background(), "user-1")
	if len(links) != 0 {
		t.Fatalf("expected link removed, got %d", len(links))
	}
}

func TestLinkingUnconfigured(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewLinkingHandler(nil, nil, logger)
	req := httptest.NewRequest(http.MethodGet, "/api/v0/account_links", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}
