package helpermod

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// SignatureSuffix is appended to the index URL to find its detached signature:
// versions.json is signed by versions.json.sig, published beside it.
//
// The index names the digest of every jar the agent will install into a
// server, so whoever can change the index chooses the jar. A per-jar SHA-256
// only proves the jar matches the index; the signature proves the index came
// from the holder of the release key — not from whoever controls the host it
// is served from, nor from a process that rewrote the on-disk cache.
const SignatureSuffix = ".sig"

// ParsePublicKeys decodes a comma-separated list of base64 ed25519 public keys.
// More than one key lets a release key be rotated without a flag day.
func ParsePublicKeys(raw string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(part)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("helpermod: %q is not a base64 ed25519 public key", part)
		}
		keys = append(keys, ed25519.PublicKey(b))
	}
	return keys, nil
}

// SignIndex returns the detached signature for an index document, as the text
// published in versions.json.sig.
func SignIndex(key ed25519.PrivateKey, index []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, index)) + "\n")
}

// VerifyIndex reports whether sig is a valid signature over index by any of
// keys.
func VerifyIndex(keys []ed25519.PublicKey, index, sig []byte) bool {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	for _, k := range keys {
		if ed25519.Verify(k, index, raw) {
			return true
		}
	}
	return false
}
