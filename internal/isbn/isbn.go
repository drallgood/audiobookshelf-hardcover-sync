// Package isbn normalizes ISBN strings and converts between the ISBN-10 and
// ISBN-13 forms of the same book. It imports no other project package so the
// sync, mismatch, edition, and Hardcover client code can all share it.
//
// Checksum policy: a value is accepted by shape alone, so an ISBN whose check
// digit is wrong is still returned in the form it arrived in. Result.Valid
// reports the input's own checksum, as Hardcover's isbn_10_valid and
// isbn_13_valid fields do, so a caller can decide what to do with an invalid
// ISBN without this package dropping it. The other form of the same book is
// derived only from a valid ISBN-10, or a valid 978 ISBN-13.
package isbn

import (
	"strings"
	"unicode"
)

// Result is a parsed ISBN. Given is the normalized input, in whichever form it
// arrived. Counterpart is the other form of the same book, or empty when it
// cannot be derived: the input's own checksum was invalid, or it is a 979
// ISBN-13, which has no ISBN-10.
type Result struct {
	Given       string
	Counterpart string
	// Is13 reports whether Given is an ISBN-13 (otherwise an ISBN-10).
	Is13 bool
	// Valid reports whether Given's own check digit is correct. It is true for
	// a valid 979 ISBN-13 even though that has no Counterpart.
	Valid bool
}

// ISBN10 returns the ISBN-10 form that is known: the input itself or its
// derived counterpart. It is empty when neither is available.
func (r Result) ISBN10() string {
	if r.Is13 {
		return r.Counterpart
	}
	return r.Given
}

// ISBN13 returns the ISBN-13 form that is known: the input itself or its
// derived counterpart. It is empty when neither is available.
func (r Result) ISBN13() string {
	if r.Is13 {
		return r.Given
	}
	return r.Counterpart
}

// GivenISBN10 returns the input as an ISBN-10, or empty if it was an ISBN-13.
func (r Result) GivenISBN10() string {
	if r.Is13 {
		return ""
	}
	return r.Given
}

// GivenISBN13 returns the input as an ISBN-13, or empty if it was an ISBN-10.
func (r Result) GivenISBN13() string {
	if r.Is13 {
		return r.Given
	}
	return ""
}

// Normalize removes spaces, hyphens, and other separator characters and
// uppercases a trailing check-digit x. It does not validate the result.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) || isSeparator(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if strings.HasSuffix(out, "x") {
		out = strings.TrimSuffix(out, "x") + "X"
	}
	return out
}

// isSeparator reports whether r is a character used to separate ISBN digits.
func isSeparator(r rune) bool {
	switch r {
	case '-', '_', '.', '‐', '‑', '‒', '–', '—', '―':
		return true
	}
	return false
}

// Parse normalizes s and accepts it by shape only: 13 digits is an ISBN-13, and
// 9 digits followed by a digit or X is an ISBN-10. Anything else is rejected.
// The result records whether the input's checksum is valid, and the counterpart
// form is filled in only when it is.
func Parse(s string) (Result, bool) {
	n := Normalize(s)
	switch {
	case len(n) == 13 && allDigits(n):
		r := Result{Given: n, Is13: true, Valid: valid13(n)}
		if r.Valid && strings.HasPrefix(n, "978") {
			r.Counterpart = to10(n)
		}
		return r, true
	case len(n) == 10 && allDigits(n[:9]) && (allDigits(n[9:]) || n[9] == 'X'):
		r := Result{Given: n, Valid: valid10(n)}
		if r.Valid {
			r.Counterpart = to13(n)
		}
		return r, true
	}
	return Result{}, false
}

// allDigits reports whether s contains only digits and is not empty.
func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// check13 calculates the ISBN-13 check digit for the first 12 characters.
func check13(first12 string) byte {
	sum := 0
	for i, r := range first12 {
		d := int(r - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10)
}

// valid13 reports whether s is a valid ISBN-13 string.
func valid13(s string) bool { return s[12] == check13(s[:12]) }

// check10 calculates the ISBN-10 check digit for the first 9 characters.
func check10(first9 string) byte {
	sum := 0
	for i, r := range first9 {
		sum += (10 - i) * int(r-'0')
	}
	c := (11 - sum%11) % 11
	if c == 10 {
		return 'X'
	}
	return byte('0' + c)
}

// valid10 reports whether s is a valid ISBN-10 string.
func valid10(s string) bool { return s[9] == check10(s[:9]) }

// to13 converts a valid ISBN-10 to its 978-prefixed ISBN-13.
func to13(isbn10 string) string {
	body := "978" + isbn10[:9]
	return body + string(check13(body))
}

// to10 converts a valid 978-prefixed ISBN-13 to its ISBN-10.
func to10(isbn13 string) string {
	body := isbn13[3:12]
	return body + string(check10(body))
}

// Split returns the ISBN-10 or ISBN-13 form that the value itself carries
// (separators dropped, lowercase trailing x upcased), never a derived counterpart.
// Both return values are empty when the input is not ISBN-shaped.
func Split(raw string) (isbn10, isbn13 string) {
	if parsed, ok := Parse(raw); ok {
		return parsed.GivenISBN10(), parsed.GivenISBN13()
	}
	return "", ""
}
