/*
Copyright 2024 AgentTier Authors.

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

package router

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/agenttier/agenttier/pkg/router/auth"
)

// apiKeyPlaintextPrefix tags generated keys so they're recognizable in logs /
// support tickets and so a leaked key is greppable. The remainder is 32 bytes
// of CSPRNG entropy, base64url-encoded.
const apiKeyPlaintextPrefix = "atk_"

// generateAPIKey returns a new random API key string (plaintext, shown once).
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return apiKeyPlaintextPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// secretStore narrows s.apiKeyValidator's store back to the concrete
// secretAPIKeyStore so the handlers can call its create/list/delete helpers.
// The validator only needs the read interface; the handlers need the writes.
func (s *Server) secretStore() *secretAPIKeyStore {
	return newSecretAPIKeyStore(s.k8sClient, s.config.InstallNamespace)
}

// handleListAPIKeys returns metadata (never the key or its hash) for every
// API key owned by the authenticated user.
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	keys, err := s.secretStore().listAPIKeysForUser(r.Context(), claims.Sub)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list API keys: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})
}

// handleCreateAPIKey mints a new API key for the authenticated user. The
// plaintext key is returned EXACTLY ONCE in this response; only its SHA-256
// hash is persisted (in a Secret). There is no way to recover the key later.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Name      string `json:"name,omitempty"`
		ExpiresIn string `json:"expiresIn,omitempty"` // optional Go duration, e.g. "720h"
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}

	var expiresAt *time.Time
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err != nil || d <= 0 {
			respondError(w, http.StatusBadRequest, "expiresIn must be a positive Go duration (e.g. \"720h\")")
			return
		}
		t := time.Now().Add(d)
		expiresAt = &t
	}

	plaintext, err := generateAPIKey()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	keyHash := auth.HashAPIKey(plaintext)

	rec := &auth.APIKeyRecord{
		KeyHash:   keyHash,
		UserID:    claims.Sub,
		Email:     claims.Email,
		Name:      req.Name,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}
	if err := s.secretStore().createAPIKeySecret(r.Context(), keyHash, rec); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to store API key: "+err.Error())
		return
	}

	// The plaintext key is returned once, here, and never again.
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":        secretNameForHash(keyHash),
		"key":       plaintext,
		"name":      rec.Name,
		"createdAt": rec.CreatedAt,
		"expiresAt": rec.ExpiresAt,
		"warning":   "Store this key now — it cannot be retrieved again.",
	})
}

// handleRevokeAPIKey deletes an API key by its ID. Non-admins may only revoke
// their own keys; admins may revoke any.
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	keyID := mux.Vars(r)["keyId"]
	if keyID == "" {
		respondError(w, http.StatusBadRequest, "keyId is required")
		return
	}
	if err := s.secretStore().deleteAPIKey(r.Context(), keyID, claims.Sub, claims.IsAdmin); err != nil {
		// Map ownership failures to 403, everything else to 404/500-ish 400.
		if err.Error() == "access denied" {
			respondError(w, http.StatusForbidden, "you do not own this API key")
			return
		}
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"status": "revoked", "id": keyID})
}
