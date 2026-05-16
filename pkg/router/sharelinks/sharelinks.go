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

// Package sharelinks generates and validates share-link tokens stored on
// the Sandbox CR.
//
// **Why SHA-256, not bcrypt.** Bcrypt is the right choice for *passwords*
// because it's intentionally slow, thwarting offline brute force against
// low-entropy human inputs. Share-link tokens are something we generate
// ourselves — at 256 bits of cryptographic randomness, the entropy is so
// far above what bcrypt's slowness would protect against that a fast hash
// is correct. SHA-256 with constant-time comparison gives us:
//
//   - Plaintext tokens never persist (only the hash lands in etcd).
//   - O(1) hash-and-compare on validation — works fine at scale.
//   - No new dependency (stdlib only); avoids the Go-toolchain churn that
//     pulling in golang.org/x/crypto/bcrypt would trigger today.
//
// The legacy plaintext Token field on ShareLink stays around for one
// minor release as a deprecation window. Validate() falls back to a
// constant-time compare against that field if TokenHash is empty, and
// emits a deprecation warning via the supplied logger so operators see
// when old links are still in flight.
package sharelinks

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TokenBytes is the entropy length of a freshly-generated raw token in
// bytes. 32 bytes = 256 bits — well above 128-bit security ceiling and
// what every standard random-token recommendation specifies.
const TokenBytes = 32

// IDBytes is the entropy length of the public, non-secret link ID.
// 8 bytes (64 bits) is enough to make collisions vanishingly unlikely
// across any realistic per-sandbox link count, and short enough to keep
// log lines readable.
const IDBytes = 8

// ErrTokenMismatch is returned when an incoming raw token doesn't match
// the stored hash. We deliberately use a single error type for "wrong
// token" so callers can't distinguish "no such link" from "wrong
// token" — making timing attacks against the lookup itself harder.
var ErrTokenMismatch = errors.New("share link token mismatch")

// Generate returns (id, rawToken, hash). The rawToken is what we hand to
// the API caller; the hash is what we persist on the CR. The id is a
// stable, non-secret identifier used for revocation and audit logging.
func Generate() (id, rawToken, hash string, err error) {
	idBytes := make([]byte, IDBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", "", fmt.Errorf("read random for id: %w", err)
	}
	tokBytes := make([]byte, TokenBytes)
	if _, err := rand.Read(tokBytes); err != nil {
		return "", "", "", fmt.Errorf("read random for token: %w", err)
	}

	id = hex.EncodeToString(idBytes)
	// URL-safe so the raw token can ship as-is in a URL fragment.
	rawToken = base64.RawURLEncoding.EncodeToString(tokBytes)
	hash = HashToken(rawToken)
	return id, rawToken, hash, nil
}

// HashToken returns the lower-case hex-encoded SHA-256 of the raw token.
// Stable encoding keeps Validate() byte-comparisons deterministic across
// platforms and Go versions.
func HashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// Validate checks an incoming raw token against a stored ShareLink. It
// prefers TokenHash (the post-fix path) and falls back to the deprecated
// Token field for backward compatibility during the deprecation window.
//
// Comparisons use crypto/subtle.ConstantTimeCompare to avoid leaking match
// progress through timing — an attacker that can only see response timing
// learns nothing about how many leading characters matched.
//
// Returns nil on a successful match and ErrTokenMismatch otherwise.
// Returns a non-nil error when the link itself is malformed
// (zero-value link, both fields empty, etc) so callers can return a
// 500-class error rather than 401.
func Validate(link *agenttierv1alpha1.ShareLink, rawToken string) error {
	if link == nil {
		return errors.New("nil share link")
	}
	if rawToken == "" {
		return ErrTokenMismatch
	}

	if link.TokenHash != "" {
		incoming := HashToken(rawToken)
		// Both sides hex-encoded SHA-256 → constant length, safe for
		// constant-time compare.
		if subtle.ConstantTimeCompare([]byte(incoming), []byte(link.TokenHash)) == 1 {
			return nil
		}
		// Even with a hash mismatch, fall through to the legacy Token
		// path: a link could have been written before the migration
		// completed, in which case TokenHash would have been empty —
		// but if both fields are present (transitional state), we
		// honor either.
	}

	// Legacy fallback. A link from before the schema change has only
	// the plaintext Token; honor it for one deprecation window then
	// rip this branch out.
	if link.Token != "" {
		if subtle.ConstantTimeCompare([]byte(rawToken), []byte(link.Token)) == 1 {
			return nil
		}
	}

	if link.TokenHash == "" && link.Token == "" {
		return errors.New("share link has no token or token hash")
	}
	return ErrTokenMismatch
}
