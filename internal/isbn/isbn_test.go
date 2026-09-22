package isbn

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"978-0-306-40615-7", "9780306406157"},
		{" 978 0306 40615 7 ", "9780306406157"},
		{"0-306-40615-2", "0306406152"},
		{"0-8044-2957-x", "080442957X"},
		{"080442957X", "080442957X"},
		{"978–0306406157", "9780306406157"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		ok          bool
		given       string
		is13        bool
		counterpart string
		valid       bool
	}{
		{name: "plain ISBN-13 gets its ISBN-10", in: "9780306406157", ok: true, given: "9780306406157", is13: true, counterpart: "0306406152", valid: true},
		{name: "hyphenated ISBN-13", in: "978-0-306-40615-7", ok: true, given: "9780306406157", is13: true, counterpart: "0306406152", valid: true},
		{name: "plain ISBN-10 gets its ISBN-13", in: "0306406152", ok: true, given: "0306406152", counterpart: "9780306406157", valid: true},
		{name: "hyphenated ISBN-10", in: "0-306-40615-2", ok: true, given: "0306406152", counterpart: "9780306406157", valid: true},
		{name: "spaces separate the groups", in: "0 306 40615 2", ok: true, given: "0306406152", counterpart: "9780306406157", valid: true},
		{name: "lowercase x check digit", in: "0-8044-2957-x", ok: true, given: "080442957X", counterpart: "9780804429573", valid: true},
		{name: "979 ISBN-13 has no ISBN-10", in: "979-10-90636-07-1", ok: true, given: "9791090636071", is13: true, valid: true},
		{name: "invalid ISBN-13 checksum keeps only the given form", in: "9780306406158", ok: true, given: "9780306406158", is13: true},
		{name: "invalid ISBN-10 checksum keeps only the given form", in: "0306406153", ok: true, given: "0306406153"},
		{name: "wrong length", in: "97803064061", ok: false},
		{name: "letters in an ISBN-13", in: "97803064A6157", ok: false},
		{name: "X only allowed as the ISBN-10 check digit", in: "03064X6152", ok: false},
		{name: "X is not valid in an ISBN-13", in: "978030640615X", ok: false},
		{name: "empty", in: "", ok: false},
		{name: "text", in: "not an isbn", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.in)
			if ok != tt.ok {
				t.Fatalf("Parse(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.Given != tt.given || got.Is13 != tt.is13 || got.Counterpart != tt.counterpart || got.Valid != tt.valid {
				t.Errorf("Parse(%q) = %+v, want given %q is13 %v counterpart %q valid %v", tt.in, got, tt.given, tt.is13, tt.counterpart, tt.valid)
			}
		})
	}
}

func TestResultForms(t *testing.T) {
	r13, _ := Parse("9780306406157")
	if r13.ISBN13() != "9780306406157" || r13.ISBN10() != "0306406152" || r13.GivenISBN13() != "9780306406157" || r13.GivenISBN10() != "" {
		t.Errorf("ISBN-13 result forms = %+v", r13)
	}
	r10, _ := Parse("0306406152")
	if r10.ISBN10() != "0306406152" || r10.ISBN13() != "9780306406157" || r10.GivenISBN10() != "0306406152" || r10.GivenISBN13() != "" {
		t.Errorf("ISBN-10 result forms = %+v", r10)
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		in     string
		want10 string
		want13 string
	}{
		{in: "978-0-306-40615-7", want10: "", want13: "9780306406157"},
		{in: "0-306-40615-2", want10: "0306406152", want13: ""},
		{in: "0-8044-2957-x", want10: "080442957X", want13: ""},
		{in: "978 0306 40615 7", want10: "", want13: "9780306406157"},
		{in: "not-an-isbn", want10: "", want13: ""},
		{in: "", want10: "", want13: ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got10, got13 := Split(tt.in)
			if got10 != tt.want10 || got13 != tt.want13 {
				t.Errorf("Split(%q) = (%q, %q), want (%q, %q)", tt.in, got10, got13, tt.want10, tt.want13)
			}
		})
	}
}
