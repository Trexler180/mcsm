package metrics

import "testing"

func TestCounterRate(t *testing.T) {
	tests := []struct {
		name              string
		previous, current uint64
		seconds           float64
		want              uint64
	}{
		{name: "normal delta", previous: 100, current: 500, seconds: 2, want: 200},
		{name: "sub-second", previous: 100, current: 200, seconds: 0.5, want: 200},
		{name: "counter reset", previous: 500, current: 10, seconds: 1, want: 0},
		{name: "no elapsed time", previous: 100, current: 200, seconds: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := counterRate(tt.previous, tt.current, tt.seconds); got != tt.want {
				t.Fatalf("counterRate() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNewCollectorStartsNetworkAtBaseline(t *testing.T) {
	c := NewCollector()
	if !c.prevTime.IsZero() {
		t.Fatal("new collector has a network timestamp before its first counter sample")
	}
}
