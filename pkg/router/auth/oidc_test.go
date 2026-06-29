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

package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// --- test helpers ---------------------------------------------------------

// signToken builds a signed RS256 JWT from the given claims using key, with
// the given kid in the header.
func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]interface{}) string {
	t.Helper()
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signingInput := b64(hb) + "." + b64(cb)

	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + b64(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// validatorWithKey builds an OIDCValidator wired to verify against pub, with
// the JWKS cache pre-seeded (no network). issuer/clientID drive claim checks.
func validatorWithKey(pub *rsa.PublicKey, kid, issuer, clientID string) *OIDCValidator {
	cache := NewJWKSCache(time.Hour)
	cache.keys[kid] = pub
	return &OIDCValidator{
		issuerURL:  issuer,
		clientID:   clientID,
		adminGroup: "agenttier-admins",
		groupClaim: "groups",
		jwksCache:  cache,
		httpClient: nil, // unused — cache is pre-seeded
	}
}

func baseClaims(issuer, aud string) map[string]interface{} {
	return map[string]interface{}{
		"sub":    "user-123",
		"email":  "u@example.com",
		"name":   "Test User",
		"iss":    issuer,
		"aud":    aud,
		"exp":    float64(time.Now().Add(time.Hour).Unix()),
		"groups": []interface{}{"agenttier-admins", "devs"},
	}
}

// --- tests ----------------------------------------------------------------

func TestValidateToken_ValidToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")

	tok := signToken(t, key, "kid1", baseClaims("https://issuer.example", "client-abc"))
	claims, err := v.ValidateToken(context.TODO(), tok)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if claims.Sub != "user-123" {
		t.Errorf("sub = %q, want user-123", claims.Sub)
	}
	if !claims.IsAdmin {
		t.Errorf("expected IsAdmin true (user is in agenttier-admins)")
	}
}

// TestValidateToken_ForgedSignature is the regression test for the P0 hole:
// verifyRS256Signature used to return nil unconditionally, accepting any
// signature. A token signed by a DIFFERENT key (an attacker's) must be
// rejected.
func TestValidateToken_ForgedSignature(t *testing.T) {
	realKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	attackerKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	// Validator trusts realKey; attacker signs with attackerKey under the
	// same kid (as if they'd guessed it).
	v := validatorWithKey(&realKey.PublicKey, "kid1", "https://issuer.example", "client-abc")
	forged := signToken(t, attackerKey, "kid1", baseClaims("https://issuer.example", "client-abc"))

	if _, err := v.ValidateToken(context.TODO(), forged); err == nil {
		t.Fatal("SECURITY: forged-signature token was ACCEPTED — verifyRS256Signature regressed to a no-op")
	}
}

// TestValidateToken_TamperedPayload: valid signature over original payload,
// but the payload is swapped (e.g. escalating to a different sub). The
// signature no longer matches the signing input → reject.
func TestValidateToken_TamperedPayload(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")

	tok := signToken(t, key, "kid1", baseClaims("https://issuer.example", "client-abc"))
	// Tamper: replace the payload segment with a different one.
	evil, _ := json.Marshal(map[string]interface{}{"sub": "admin", "iss": "https://issuer.example", "aud": "client-abc", "exp": float64(time.Now().Add(time.Hour).Unix())})
	parts := splitJWT(tok)
	tampered := parts[0] + "." + b64(evil) + "." + parts[2]

	if _, err := v.ValidateToken(context.TODO(), tampered); err == nil {
		t.Fatal("tampered-payload token was accepted")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")

	c := baseClaims("https://issuer.example", "client-abc")
	c["exp"] = float64(time.Now().Add(-time.Minute).Unix()) // expired
	tok := signToken(t, key, "kid1", c)

	if _, err := v.ValidateToken(context.TODO(), tok); err == nil {
		t.Fatal("expired token was accepted")
	}
}

func TestValidateToken_WrongIssuer(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")

	tok := signToken(t, key, "kid1", baseClaims("https://evil.example", "client-abc"))
	if _, err := v.ValidateToken(context.TODO(), tok); err == nil {
		t.Fatal("token with wrong issuer was accepted")
	}
}

func TestValidateToken_WrongAudience(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")

	tok := signToken(t, key, "kid1", baseClaims("https://issuer.example", "someone-else"))
	if _, err := v.ValidateToken(context.TODO(), tok); err == nil {
		t.Fatal("token with wrong audience was accepted")
	}
}

func TestValidateToken_UnknownKid(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")

	// Sign with a kid the cache doesn't know.
	tok := signToken(t, key, "kid-unknown", baseClaims("https://issuer.example", "client-abc"))
	if _, err := v.ValidateToken(context.TODO(), tok); err == nil {
		t.Fatal("token with unknown kid was accepted")
	}
}

func TestValidateToken_NotAdminWhenNotInGroup(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")

	c := baseClaims("https://issuer.example", "client-abc")
	c["groups"] = []interface{}{"devs"} // not in agenttier-admins
	tok := signToken(t, key, "kid1", c)

	claims, err := v.ValidateToken(context.TODO(), tok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims.IsAdmin {
		t.Error("expected IsAdmin false for a user not in the admin group")
	}
}

func TestVerifyRS256Signature_NilKey(t *testing.T) {
	if err := verifyRS256Signature("a.b", "sig", nil); err == nil {
		t.Fatal("expected error for nil key")
	}
}

func splitJWT(tok string) [3]string {
	var out [3]string
	idx := 0
	start := 0
	for i := 0; i < len(tok) && idx < 3; i++ {
		if tok[i] == '.' {
			out[idx] = tok[start:i]
			idx++
			start = i + 1
		}
	}
	if idx < 3 {
		out[idx] = tok[start:]
	}
	return out
}

// TestValidateToken_RejectsNonRS256Alg guards the alg-confusion hardening:
// the validator only verifies RS256, so any other "alg" header (none, HS256,
// RS512, empty) must be rejected up front — before signature verification.
func TestValidateToken_RejectsNonRS256Alg(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := validatorWithKey(&key.PublicKey, "kid1", "https://issuer.example", "client-abc")
	claims := baseClaims("https://issuer.example", "client-abc")
	cb, _ := json.Marshal(claims)

	for _, alg := range []string{"none", "HS256", "RS512", ""} {
		header := map[string]string{"alg": alg, "typ": "JWT", "kid": "kid1"}
		hb, _ := json.Marshal(header)
		// Third part is a dummy signature — the alg gate must fire before
		// we ever attempt signature verification.
		tok := b64(hb) + "." + b64(cb) + ".ZHVtbXk"
		if _, err := v.ValidateToken(context.TODO(), tok); err == nil {
			t.Errorf("alg=%q was ACCEPTED; expected rejection (algorithm confusion)", alg)
		}
	}
}
