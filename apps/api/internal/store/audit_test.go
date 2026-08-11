package store

import (
	"context"
	"testing"
)

func TestLogActionReturnsSerializationFailure(t *testing.T) {
	s := testStore(t)
	err := s.LogAction(context.Background(), "", "", "test.invalid", "", make(chan struct{}))
	if err == nil {
		t.Fatal("expected unsupported audit detail to return an error")
	}
}
