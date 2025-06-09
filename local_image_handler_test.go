package main

import (
	"testing"
)

func TestIsLocalAudiobookShelfURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://abs.books.princess.local/api/items/123/cover", true},
		{"http://localhost:8080/cover", true},
		{"https://192.168.1.100/cover", true},
		{"http://10.0.0.5/image.jpg", true},
		{"https://172.16.0.1/cover", true},
		{"https://amazon.com/image.jpg", false},
		{"https://m.media-amazon.com/images/I/image.jpg", false},
		{"", false},
	}

	for _, test := range tests {
		result := isLocalAudiobookShelfURL(test.url)
		if result != test.expected {
			t.Errorf("isLocalAudiobookShelfURL(%q) = %v; expected %v", test.url, result, test.expected)
		}
	}
}
