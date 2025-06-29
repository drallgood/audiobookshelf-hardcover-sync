package testutils

import (
	"testing"
)

func TestLocalImageURLDetection(t *testing.T) {
	// Test the specific URL from your issue
	localURL := "https://abs.books.princess.local/api/items/d32f7a77-d3b1-464d-aec5-404db5e141ca/cover"

	if !isLocalAudiobookShelfURL(localURL) {
		t.Errorf("Expected %s to be detected as local URL", localURL)
	}

	// Test some public URLs that should NOT be detected as local
	publicURLs := []string{
		"https://m.media-amazon.com/images/I/51234567890.jpg",
		"https://covers.openlibrary.org/b/id/12345-L.jpg",
		"https://cdn.example.com/cover.jpg",
	}

	for _, url := range publicURLs {
		if isLocalAudiobookShelfURL(url) {
			t.Errorf("Expected %s NOT to be detected as local URL", url)
		}
	}
}
