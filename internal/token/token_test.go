package token

import "testing"

func TestGenerateIsWellFormedAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !WellFormed(tok) {
			t.Fatalf("generated token not well formed: %q", tok)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
}

func TestWellFormedRejectsOtherValues(t *testing.T) {
	for _, v := range []string{"", "abc", "not-a-token", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"} {
		if WellFormed(v) {
			t.Errorf("%q should not be well formed", v)
		}
	}
}

func TestHashIsDeterministicAndHidesToken(t *testing.T) {
	tok, _ := Generate()
	h1, h2 := Hash(tok), Hash(tok)
	if h1 != h2 || len(h1) != 64 || h1 == tok {
		t.Fatalf("unexpected hash %q", h1)
	}
	other, _ := Generate()
	if Hash(other) == h1 {
		t.Fatal("different tokens produced same hash")
	}
}
