package party

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  A@Example.COM \t"); got != "a@example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		in, want, msg string
	}{
		{"  Kim  ", "Kim", ""},
		{"", "", MsgNameRequired},
		{"   \t ", "", MsgNameRequired},
		{strings.Repeat("가", MaxNameLength), strings.Repeat("가", MaxNameLength), ""},
		{strings.Repeat("가", MaxNameLength+1), "", MsgNameTooLong},
	}
	for _, c := range cases {
		got, msg := ValidateName(c.in)
		if got != c.want || msg != c.msg {
			t.Errorf("ValidateName(%q) = %q, %q; want %q, %q", c.in, got, msg, c.want, c.msg)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	valid := map[string]string{
		"a@example.com":         "a@example.com",
		"  A@Example.COM  ":     "a@example.com",
		"first.last@sub.ex.org": "first.last@sub.ex.org",
		"x+tag@example.co.kr":   "x+tag@example.co.kr",
	}
	for in, want := range valid {
		trimmed, norm, msg := ValidateEmail(in)
		if msg != "" || norm != want || trimmed != strings.TrimSpace(in) {
			t.Errorf("ValidateEmail(%q) = %q, %q, %q", in, trimmed, norm, msg)
		}
	}

	invalid := map[string]string{
		"":                       MsgEmailRequired,
		"   ":                    MsgEmailRequired,
		"abc":                    MsgEmailInvalid,
		"a@":                     MsgEmailInvalid,
		"@example.com":           MsgEmailInvalid,
		"a@localhost":            MsgEmailInvalid,
		"a@example.":             MsgEmailInvalid,
		"a@.example.com":         MsgEmailInvalid,
		"a b@example.com":        MsgEmailInvalid,
		"Kim <a@example.com>":    MsgEmailInvalid,
		"a@example.com, b@x.com": MsgEmailInvalid,
	}
	invalid[strings.Repeat("a", 250)+"@x.com"] = MsgEmailTooLong
	for in, want := range invalid {
		if _, _, msg := ValidateEmail(in); msg != want {
			t.Errorf("ValidateEmail(%q) msg = %q; want %q", in, msg, want)
		}
	}
}

func TestValidatePartySize(t *testing.T) {
	for _, n := range []int{1, 2, 9, 10} {
		if msg := ValidatePartySize(n); msg != "" {
			t.Errorf("%d should be valid: %s", n, msg)
		}
	}
	for _, n := range []int{-1, 0, 11, 100} {
		if msg := ValidatePartySize(n); msg != MsgPartySizeInvalid {
			t.Errorf("%d should be invalid", n)
		}
	}
}

func TestStatusActive(t *testing.T) {
	if !StatusWaiting.Active() || !StatusDining.Active() || StatusCompleted.Active() {
		t.Fatal("unexpected Active result")
	}
}
