// Package sms holds provider-independent helpers for message bodies.
package sms

import (
	"regexp"
	"strings"
	"unicode/utf16"
)

const (
	gsmBasic    = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	gsmExtended = "^{}\\[~]|€\f"
)

var otpRe = regexp.MustCompile(`(?:^|\D)(\d{3}[- ]\d{3}|\d{4,8})(?:\D|$)`)

// ExtractOTP returns the first thing in body that looks like a one-time code
// (4 to 8 digits, or two groups of 3), or "" if there is none.
func ExtractOTP(body string) string {
	m := otpRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.NewReplacer("-", "", " ", "").Replace(m[1])
}

// Segments reports the encoding a carrier would use for body ("GSM-7" or
// "UCS-2") and how many SMS segments it would be billed as.
func Segments(body string) (encoding string, n int) {
	units, gsm := 0, true
	for _, r := range body {
		switch {
		case strings.ContainsRune(gsmBasic, r):
			units++
		case strings.ContainsRune(gsmExtended, r):
			units += 2
		default:
			gsm = false
		}
	}
	single, multi := 160, 153
	encoding = "GSM-7"
	if !gsm {
		units = len(utf16.Encode([]rune(body)))
		single, multi = 70, 67
		encoding = "UCS-2"
	}
	if units <= single {
		return encoding, 1
	}
	return encoding, (units + multi - 1) / multi
}
