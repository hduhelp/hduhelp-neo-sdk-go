package core

import (
	"regexp"
	"testing"
)

// TestS256ChallengeRFC7636AppendixB checks S256 derivation against the canonical
// vector from RFC 7636 Appendix B.
func TestS256ChallengeRFC7636AppendixB(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const wantChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := S256Challenge(verifier); got != wantChallenge {
		t.Fatalf("S256Challenge(%q) = %q, want %q", verifier, got, wantChallenge)
	}
}

func TestGeneratePKCE(t *testing.T) {
	unreserved := regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
	for i := 0; i < 100; i++ {
		p, err := GeneratePKCE()
		if err != nil {
			t.Fatalf("GeneratePKCE: %v", err)
		}
		if l := len(p.Verifier); l < 43 || l > 128 {
			t.Fatalf("verifier length %d out of RFC 7636 range [43,128]", l)
		}
		if !unreserved.MatchString(p.Verifier) {
			t.Fatalf("verifier %q has characters outside the unreserved set", p.Verifier)
		}
		if p.Method != "S256" {
			t.Fatalf("method = %q, want S256", p.Method)
		}
		if p.Challenge != S256Challenge(p.Verifier) {
			t.Fatalf("challenge does not match S256(verifier)")
		}
	}
}
