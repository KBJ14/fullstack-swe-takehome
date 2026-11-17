package party

import "testing"

func TestCanCheckIn(t *testing.T) {
	cases := []struct {
		name                       string
		ahead                      int64
		capacity, seatsInUse, size int
		want                       bool
	}{
		{"head, enough seats", 0, 10, 0, 4, true},
		{"head, exactly enough", 0, 10, 6, 4, true},
		{"head, not enough", 0, 10, 6, 5, false},
		{"not head, enough seats", 1, 10, 0, 1, false},
		{"not head, small party", 3, 10, 6, 2, false},
		{"head, full", 0, 10, 10, 1, false},
	}
	for _, c := range cases {
		if got := CanCheckIn(c.ahead, c.capacity, c.seatsInUse, c.size); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
