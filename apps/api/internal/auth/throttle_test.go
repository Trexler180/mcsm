package auth

import "testing"

func TestLoginThrottleLocksAfterFreeAttempts(t *testing.T) {
	tr := NewLoginThrottle()
	key := "ip:203.0.113.7"

	// The free attempts must not lock.
	for i := 0; i < tr.freeAttempts; i++ {
		if ok, _ := tr.Allowed(key); !ok {
			t.Fatalf("attempt %d should be allowed", i)
		}
		tr.Fail(key)
	}

	// One more failure crosses the threshold and arms the lockout.
	if ok, _ := tr.Allowed(key); !ok {
		t.Fatal("still allowed at threshold boundary")
	}
	tr.Fail(key)

	ok, retry := tr.Allowed(key)
	if ok {
		t.Fatal("expected lockout after exceeding free attempts")
	}
	if retry <= 0 {
		t.Fatalf("expected positive retry-after, got %v", retry)
	}
}

func TestLoginThrottleResetClearsLock(t *testing.T) {
	tr := NewLoginThrottle()
	key := "email:victim@example.com"
	for i := 0; i < tr.freeAttempts+2; i++ {
		tr.Fail(key)
	}
	if ok, _ := tr.Allowed(key); ok {
		t.Fatal("expected lockout before reset")
	}
	tr.Reset(key)
	if ok, _ := tr.Allowed(key); !ok {
		t.Fatal("reset should clear the lockout")
	}
}

func TestLoginThrottleEmptyKeyIsNoop(t *testing.T) {
	tr := NewLoginThrottle()
	tr.Fail("")
	if ok, _ := tr.Allowed(""); !ok {
		t.Fatal("empty key must always be allowed")
	}
}
