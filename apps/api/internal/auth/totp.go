package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 TOTP with the parameters every authenticator app defaults to: SHA-1,
// 6 digits, 30-second steps. Implemented directly so the project takes on no new
// dependency for a ~60-line algorithm.

const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// totpSkew allows the immediately previous and next windows, covering clock
	// drift and a code entered right at a boundary.
	totpSkew = 1
)

var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a fresh base32-encoded secret (160 bits, the
// recommended length for SHA-1 HMAC).
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32NoPad.EncodeToString(b), nil
}

// TOTPProvisioningURI builds the otpauth:// URI an authenticator app consumes
// (typically via QR code). issuer and account are shown to the user in the app.
func TOTPProvisioningURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", int(totpPeriod.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// ValidateTOTP reports whether code is valid for secret at time t, accepting the
// adjacent windows to tolerate drift. The comparison is constant-time.
func ValidateTOTP(secret, code string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return false
	}
	counter := uint64(t.Unix()) / uint64(totpPeriod.Seconds())
	for delta := -totpSkew; delta <= totpSkew; delta++ {
		want := hotp(key, counter+uint64(delta))
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod)
}

// GenerateRecoveryCodes returns n human-friendly single-use recovery codes
// (groups of base32 chars). Show them once; store only their hashes.
func GenerateRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		raw := base32NoPad.EncodeToString(b) // 16 chars
		codes = append(codes, raw[:4]+"-"+raw[4:8]+"-"+raw[8:12]+"-"+raw[12:16])
	}
	return codes, nil
}

// NormalizeRecoveryCode strips formatting so "abcd-efgh" and "ABCDEFGH" match.
func NormalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}
