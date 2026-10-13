package events

import "testing"

func TestPublishAssignsIncreasingSequence(t *testing.T) {
	b := New()
	sub, _, _ := b.Subscribe(0)
	defer sub.Close()
	b.Publish(Event{Type: ServerStatus})
	b.Publish(Event{Type: ServerStatus})
	first, second := <-sub.C(), <-sub.C()
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seq = %d, %d", first.Seq, second.Seq)
	}
}

// A client that reconnects with the last number it saw gets what it missed.
func TestSubscribeResumesFromRing(t *testing.T) {
	b := New()
	for i := 0; i < 5; i++ {
		b.Publish(Event{Type: ServerStats})
	}
	sub, backlog, resumed := b.Subscribe(3)
	defer sub.Close()
	if !resumed || len(backlog) != 2 || backlog[0].Seq != 4 {
		t.Fatalf("resume from 3: resumed=%v backlog=%v", resumed, backlog)
	}
}

// When the ring has moved past the client's resume point, it is told so rather
// than shown a screen with a hole in it.
func TestSubscribeReportsGap(t *testing.T) {
	b := New()
	for i := 0; i < ringSize+10; i++ {
		b.Publish(Event{Type: ServerStats})
	}
	sub, _, resumed := b.Subscribe(1)
	defer sub.Close()
	if resumed {
		t.Fatal("a resume point older than the ring must report a gap")
	}
}

// A subscriber that stops reading is cut off instead of blocking publishers.
func TestSlowSubscriberIsCutOff(t *testing.T) {
	b := New()
	sub, _, _ := b.Subscribe(0)
	for i := 0; i < subBuffer+5; i++ {
		b.Publish(Event{Type: ServerStats})
	}
	if !sub.Lagged() {
		t.Fatal("an overflowing subscriber should be marked lagging")
	}
	n := 0
	for range sub.C() {
		n++
	}
	if n != subBuffer {
		t.Fatalf("drained %d buffered events, want %d", n, subBuffer)
	}
}
