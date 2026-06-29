package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)

	key, err := base32NoPad.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	counter := uint64(now.Unix()) / 30
	code := hotp(key, counter)

	if !ValidateTOTP(secret, code, now) {
		t.Fatal("freshly generated code should validate")
	}
	// Adjacent window (clock drift) accepted.
	if !ValidateTOTP(secret, code, now.Add(30*time.Second)) {
		t.Error("code should validate one window later (skew)")
	}
	// Far-future window rejected.
	if ValidateTOTP(secret, code, now.Add(5*time.Minute)) {
		t.Error("stale code should not validate")
	}
	if ValidateTOTP(secret, "000000", now.Add(time.Hour*999)) {
		t.Error("arbitrary wrong code should not validate")
	}
}

func TestTOTPRejectsMalformed(t *testing.T) {
	secret, _ := GenerateTOTPSecret()
	now := time.Now()
	for _, code := range []string{"", "12345", "1234567", "abcdef"} {
		if ValidateTOTP(secret, code, now) {
			t.Errorf("malformed code %q must not validate", code)
		}
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := TOTPProvisioningURI("ABCDEF", "ServerManager", "admin@example.com")
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("unexpected uri: %s", uri)
	}
	for _, want := range []string{"secret=ABCDEF", "issuer=ServerManager", "digits=6", "period=30"} {
		if !strings.Contains(uri, want) {
			t.Errorf("uri missing %q: %s", want, uri)
		}
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("want 10 codes, got %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		n := NormalizeRecoveryCode(c)
		if seen[n] {
			t.Errorf("duplicate recovery code %q", c)
		}
		seen[n] = true
		if strings.Contains(n, "-") || n != strings.ToUpper(n) {
			t.Errorf("normalized code still formatted: %q", n)
		}
	}
}
