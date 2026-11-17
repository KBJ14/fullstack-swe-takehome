package party

import (
	"testing"
	"time"
)

func TestServiceDuration(t *testing.T) {
	cases := map[int]time.Duration{1: 3 * time.Second, 4: 12 * time.Second, 10: 30 * time.Second}
	for size, want := range cases {
		if got := ServiceDuration(size); got != want {
			t.Errorf("ServiceDuration(%d) = %v, want %v", size, got, want)
		}
	}
}
