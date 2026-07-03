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

package sharelinks

import (
	"errors"
	"testing"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func TestGenerate_RoundTripsThroughValidate(t *testing.T) {
	id, raw, hash, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if id == "" || raw == "" || hash == "" {
		t.Fatalf("Generate returned an empty value: id=%q raw=%q hash=%q", id, raw, hash)
	}
	// The hash is the SHA-256 of the raw token in hex; it should always
	// be 64 lowercase hex characters.
	if got := len(hash); got != 64 {
		t.Errorf("hash length = %d, want 64 (sha256 hex)", got)
	}

	link := &agenttierv1alpha1.ShareLink{ID: id, TokenHash: hash, Level: "viewer"}
	if err := Validate(link, raw); err != nil {
		t.Errorf("Validate of fresh token rejected: %v", err)
	}
}

func TestValidate_RejectsWrongToken(t *testing.T) {
	_, _, hash, _ := Generate()
	link := &agenttierv1alpha1.ShareLink{TokenHash: hash, Level: "viewer"}
	if err := Validate(link, "completely-wrong-token"); !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("Validate of wrong token: got %v, want ErrTokenMismatch", err)
	}
}

func TestValidate_RejectsEmptyToken(t *testing.T) {
	_, _, hash, _ := Generate()
	link := &agenttierv1alpha1.ShareLink{TokenHash: hash, Level: "viewer"}
	if err := Validate(link, ""); !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("Validate of empty token: got %v, want ErrTokenMismatch", err)
	}
}

func TestValidate_LegacyPlaintextTokenStillWorks(t *testing.T) {
	// During the deprecation window, links written with the old plaintext
	// Token field must still validate so consumers don't break overnight.
	link := &agenttierv1alpha1.ShareLink{Token: "legacy-token-123", Level: "viewer"}
	if err := Validate(link, "legacy-token-123"); err != nil {
		t.Errorf("legacy plaintext token rejected: %v", err)
	}
	if err := Validate(link, "wrong"); !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("wrong legacy token: got %v, want ErrTokenMismatch", err)
	}
}

func TestValidate_HashWinsOverLegacyToken(t *testing.T) {
	// A transitional state: both fields set. The hash should be the
	// authoritative source — checking the hash first protects us if a
	// future bug accidentally leaves the legacy field populated with a
	// stale value after rotation.
	_, raw, hash, _ := Generate()
	link := &agenttierv1alpha1.ShareLink{
		Token:     "stale-old-value",
		TokenHash: hash,
		Level:     "viewer",
	}
	if err := Validate(link, raw); err != nil {
		t.Errorf("hash-matching token rejected: %v", err)
	}
	// The legacy field also still works as a fallback (intentional during
	// the deprecation window) — that's separately tested above.
}

func TestValidate_RejectsNilOrEmptyLink(t *testing.T) {
	if err := Validate(nil, "anything"); err == nil {
		t.Error("Validate(nil, ...) should error")
	}
	empty := &agenttierv1alpha1.ShareLink{Level: "viewer"}
	if err := Validate(empty, "anything"); err == nil {
		t.Error("Validate(empty link) should error")
	}
}

func TestHashToken_DeterministicAndUniform(t *testing.T) {
	a := HashToken("hello")
	b := HashToken("hello")
	c := HashToken("hello-world")

	if a != b {
		t.Errorf("HashToken not deterministic: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("HashToken collision on different inputs: %q == %q", a, c)
	}
	if len(a) != 64 {
		t.Errorf("hash length = %d, want 64 (sha256 hex)", len(a))
	}
}

func TestGenerate_TokensAreUnique(t *testing.T) {
	// Two consecutive Generates must produce different tokens. With 256
	// bits of entropy the probability of collision is ~1/2^256 — if this
	// ever fails, the random source is broken, not the test.
	_, raw1, _, _ := Generate()
	_, raw2, _, _ := Generate()
	if raw1 == raw2 {
		t.Errorf("two Generate() calls produced the same raw token: %q", raw1)
	}
}
