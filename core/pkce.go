package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCE holds a PKCE verifier/challenge pair (RFC 7636).
type PKCE struct {
	// Verifier is the high-entropy secret kept by the client and sent at the
	// token exchange. Never place it in the authorize URL.
	Verifier string
	// Challenge is BASE64URL(SHA256(Verifier)) for the S256 method.
	Challenge string
	// Method is always "S256".
	Method string
}

func base64URLNoPad(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// S256Challenge derives the S256 code_challenge from a verifier:
// BASE64URL-ENCODE(SHA256(ASCII(verifier))) per RFC 7636 §4.2.
func S256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64URLNoPad(sum[:])
}

// GeneratePKCE creates a fresh PKCE pair using the S256 method. It draws 32
// random bytes, which base64url-encode to a 43-character verifier — within the
// 43–128 character range required by RFC 7636 §4.1.
func GeneratePKCE() (PKCE, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return PKCE{}, fmt.Errorf("hduhelp: generate PKCE verifier: %w", err)
	}
	verifier := base64URLNoPad(buf)
	return PKCE{
		Verifier:  verifier,
		Challenge: S256Challenge(verifier),
		Method:    "S256",
	}, nil
}
