package testutils

import (
	"testing"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		hours    float64
		expected string
	}{
		{0, "0h 0m 0s"},
		{1.0, "1h 00m 00s"},
		{1.5, "1h 30m 00s"},
		{2.25, "2h 15m 00s"},
		{8.2, "8h 12m 00s"},
		{12.9, "12h 54m 00s"},
		{24.5, "24h 30m 00s"},
	}

	for _, test := range tests {
		result := formatDuration(test.hours)
		if result != test.expected {
			t.Errorf("formatDuration(%.1f) = %s; expected %s", test.hours, result, test.expected)
		}
	}
}

func TestFormatReleaseDate(t *testing.T) {
	tests := []struct {
		publishedDate string
		publishedYear string
		expected      string
	}{
		{"", "2020", "2020"},
		{"2020-01-15", "", "Jan 15, 2020"},
		{"2020/01/15", "", "Jan 15, 2020"},
		{"January 15, 2020", "", "Jan 15, 2020"},
		{"Jan 15, 2020", "", "Jan 15, 2020"},
		{"15 January 2020", "", "Jan 15, 2020"},
		{"2020-01", "", "Jan 2020"},
		{"January 2020", "", "Jan 2020"},
		{"2020", "", "2020"},
		{"", "", ""},
		{"2020-06-04", "2019", "Jun 4, 2020"}, // prefer publishedDate
	}

	for _, test := range tests {
		result := formatReleaseDate(test.publishedDate, test.publishedYear)
		if result != test.expected {
			t.Errorf("formatReleaseDate(%q, %q) = %q; expected %q",
				test.publishedDate, test.publishedYear, result, test.expected)
		}
	}
}
