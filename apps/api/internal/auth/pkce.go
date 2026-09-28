package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

// PKCE (RFC 7636) is what makes the authorization code flow safe for a public
// client. The client keeps a random verifier, sends only its hash up front, and
// proves possession at token exchange — so an authorization code intercepted in
// transit, in a log, or by a malicious app on the same machine is useless
// without the verifier that never left the client.
//
// Only S256 is implemented. The `plain` method transmits the verifier itself at
// authorization time, which defeats the entire purpose, and supporting it would
// let a client negotiate its way out of the protection.

const (
	// minVerifierLen and maxVerifierLen are RFC 7636 §4.1.
	minVerifierLen = 43
	maxVerifierLen = 128
)

// VerifyPKCES256 reports whether a presented verifier hashes to the challenge
// recorded when the authorization request was parked.
//
// The comparison is constant-time. The inputs are high-entropy and the
// challenge is not secret, so a timing leak here is not a practical attack —
// but a credential comparison that is obviously constant-time is one nobody has
// to re-derive that argument for later.
func VerifyPKCES256(challenge, verifier string) bool {
	challenge = strings.TrimSpace(challenge)
	verifier = strings.TrimSpace(verifier)
	if challenge == "" || len(verifier) < minVerifierLen || len(verifier) > maxVerifierLen {
		return false
	}
	if !isUnreservedPKCE(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// isUnreservedPKCE enforces the verifier's allowed alphabet. A verifier outside
// it is a malformed client rather than an attack, but accepting one would mean
// two clients could disagree about what string was hashed.
func isUnreservedPKCE(s string) bool {
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	return true
}
