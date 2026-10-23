package sms

import (
	"strings"
	"testing"
)

func TestExtractOTP(t *testing.T) {
	cases := map[string]string{
		"Your code is 482913":                    "482913",
		"123-456 is your Acme verification code": "123456",
		"Use 0042 to sign in.":                   "0042",
		"Call +351912345678 for help":            "",
		"No code here":                           "",
	}
	for body, want := range cases {
		if got := ExtractOTP(body); got != want {
			t.Errorf("ExtractOTP(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestSegments(t *testing.T) {
	cases := []struct {
		body     string
		encoding string
		n        int
	}{
		{"hello", "GSM-7", 1},
		{strings.Repeat("a", 160), "GSM-7", 1},
		{strings.Repeat("a", 161), "GSM-7", 2},
		{strings.Repeat("€", 81), "GSM-7", 2},
		{"olá 👋", "UCS-2", 1},
		{strings.Repeat("ç", 71), "UCS-2", 2},
	}
	for _, c := range cases {
		enc, n := Segments(c.body)
		if enc != c.encoding || n != c.n {
			t.Errorf("Segments(%.10q…) = %s, %d; want %s, %d", c.body, enc, n, c.encoding, c.n)
		}
	}
}
