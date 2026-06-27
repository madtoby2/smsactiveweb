package pricing

import "testing"

func TestSaleFen(t *testing.T) {
	if got := SaleFen(.5, 7.2, 1); got != 460 {
		t.Fatalf("got %d", got)
	}
	if got := SaleFen(.0419, 7.2, 1); got != 131 {
		t.Fatalf("rounding got %d", got)
	}
	if got := SaleFen(.001, 7.2, 0); got != MinimumSaleFen {
		t.Fatalf("minimum got %d want %d", got, MinimumSaleFen)
	}
}
