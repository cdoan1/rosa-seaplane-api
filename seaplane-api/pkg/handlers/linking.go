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
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/api"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/linking"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/middleware"
)

// RHTokenHeader carries the Red Hat identity token on linking requests
// made through the AWS_IAM gateway, where the SigV4 signature already
// occupies the Authorization header. Flow 2a requests through the custom
// gateway use Authorization: Bearer instead (see the identity-header
// contract doc).
const RHTokenHeader = "X-Rh-Token"

// LinkingHandler manages account links between a Red Hat identity (token
// subject) and the AWS ARNs it may operate as.
//
// Routes (Phase 4 PoC):
//
//	POST   /api/v0/account_links  — link the SigV4-proven ARN to the token
//	                               subject (AWS_IAM gateway; SigV4 identity
//	                               + X-Rh-Token).
//	DELETE /api/v0/account_links?arn=... — revoke (same auth as POST).
//	GET    /api/v0/account_links  — Flow 2a: list the caller's linked ARNs
//	                               (token-only; registered outside
//	                               RequireIdentity per D-P4.4a).
type LinkingHandler struct {
	store    linking.Store
	verifier linking.TokenVerifier
	logger   *slog.Logger
}

// NewLinkingHandler creates a LinkingHandler. Nil verifier or store makes
// the endpoints return 503.
func NewLinkingHandler(store linking.Store, verifier linking.TokenVerifier, logger *slog.Logger) *LinkingHandler {
	return &LinkingHandler{store: store, verifier: verifier, logger: logger}
}

type linkRequest struct {
	ARN string `json:"arn"`
}

type linkListResponse struct {
	Arns []linkEntry `json:"arns"`
}

type linkEntry struct {
	ARN       string `json:"arn"`
	AccountID string `json:"accountId"`
}

// Link handles POST /api/v0/account_links. Control of the ARN is proven by
// SigV4 (the gateway identity), the Red Hat identity by the verified
// X-Rh-Token; the link ARN's account must match the SigV4 account.
func (h *LinkingHandler) Link(w http.ResponseWriter, r *http.Request) {
	if h.unconfigured(w) {
		return
	}
	var req linkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, ErrLinkingInvalidRequest, h.logger)
		return
	}
	subject, ok := h.requireToken(w, r, RHTokenHeader)
	if !ok {
		return
	}

	accountID := middleware.GetAccountID(r.Context())
	if accountID == "" {
		writeAPIError(w, ErrLinkingIdentityRequired, h.logger)
		return
	}
	arnAccount, err := linking.ValidateARN(req.ARN)
	if err != nil {
		writeAPIError(w, ErrLinkingInvalidARN, h.logger)
		return
	}
	if arnAccount != accountID {
		// The SigV4 identity proves control of the caller's account only;
		// linking an ARN from another account would bypass that proof.
		h.logger.Warn("link arn account mismatch",
			"arn_account", arnAccount, "caller_account", accountID)
		writeAPIError(w, ErrLinkingARNAccountMismatch, h.logger)
		return
	}

	if err := h.store.PutLink(r.Context(), subject, req.ARN); err != nil {
		h.logger.Error("failed to create link", "error", err)
		writeAPIError(w, ErrLinkingStoreFailed, h.logger)
		return
	}
	h.logger.Info("account linked", "subject", subject, "arn", req.ARN)
	w.WriteHeader(http.StatusNoContent)
}

// Unlink handles DELETE /api/v0/account_links?arn=... .
func (h *LinkingHandler) Unlink(w http.ResponseWriter, r *http.Request) {
	if h.unconfigured(w) {
		return
	}
	arn := r.URL.Query().Get("arn")
	subject, ok := h.requireToken(w, r, RHTokenHeader)
	if !ok {
		return
	}

	accountID := middleware.GetAccountID(r.Context())
	if accountID == "" {
		writeAPIError(w, ErrLinkingIdentityRequired, h.logger)
		return
	}
	arnAccount, err := linking.ValidateARN(arn)
	if err != nil {
		writeAPIError(w, ErrLinkingInvalidARN, h.logger)
		return
	}
	if arnAccount != accountID {
		writeAPIError(w, ErrLinkingARNAccountMismatch, h.logger)
		return
	}

	if err := h.store.DeleteLink(r.Context(), subject, arn); err != nil {
		h.logger.Error("failed to delete link", "error", err)
		writeAPIError(w, ErrLinkingStoreFailed, h.logger)
		return
	}
	h.logger.Info("account unlinked", "subject", subject, "arn", arn)
	w.WriteHeader(http.StatusNoContent)
}

// List handles GET /api/v0/account_links (Flow 2a). Token-only: the bearer
// token (Authorization: Bearer or X-Rh-Token) identifies the subject.
func (h *LinkingHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.unconfigured(w) {
		return
	}
	subject, ok := h.requireToken(w, r, "Authorization")
	if !ok {
		return
	}

	links, err := h.store.ListLinks(r.Context(), subject)
	if err != nil {
		h.logger.Error("failed to list links", "error", err)
		writeAPIError(w, ErrLinkingStoreFailed, h.logger)
		return
	}
	resp := linkListResponse{Arns: make([]linkEntry, 0, len(links))}
	for _, l := range links {
		resp.Arns = append(resp.Arns, linkEntry{ARN: l.ARN, AccountID: l.AccountID})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode link list", "error", err)
	}
}

// requireToken extracts and verifies the Red Hat identity token. header is
// the header to read: "Authorization" (Bearer) or RHTokenHeader.
func (h *LinkingHandler) requireToken(w http.ResponseWriter, r *http.Request, header string) (string, bool) {
	token := ""
	if header == "Authorization" {
		if parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2); len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token = parts[1]
		}
	} else {
		token = r.Header.Get(header)
	}
	if token == "" {
		writeAPIError(w, ErrLinkingTokenRequired, h.logger)
		return "", false
	}
	subject, err := h.verifier.Verify(r.Context(), token)
	if err != nil {
		h.logger.Warn("token verification failed", "error", err)
		writeAPIError(w, ErrLinkingTokenInvalid, h.logger)
		return "", false
	}
	return subject, true
}

func (h *LinkingHandler) unconfigured(w http.ResponseWriter) bool {
	if h.store == nil || h.verifier == nil {
		if err := api.WriteError(w, ErrLinkingNotConfigured); err != nil {
			h.logger.Error("failed to write error response", "error", err)
		}
		return true
	}
	return false
}
