package main

import (
	"os"
	"strings"
	"testing"
)

func TestBulkLookupAuthorsParsing(t *testing.T) {
	// Test that bulk lookup properly parses comma-separated input
	authorNames := "Stephen King,Brandon Sanderson,J.K. Rowling"
	names := strings.Split(authorNames, ",")

	if len(names) != 3 {
		t.Errorf("Expected 3 names, got %d", len(names))
	}

	expected := []string{"Stephen King", "Brandon Sanderson", "J.K. Rowling"}
	for i, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed != expected[i] {
			t.Errorf("Expected %q, got %q", expected[i], trimmed)
		}
	}
}

func TestBulkLookupEmptyInput(t *testing.T) {
	// Test handling of empty input
	authorNames := ""
	names := strings.Split(authorNames, ",")

	if len(names) != 1 || names[0] != "" {
		t.Errorf("Expected single empty string, got %v", names)
	}
}

func TestImageUploadURLParsing(t *testing.T) {
	// Test URL parsing for image upload
	testCases := []struct {
		input        string
		expectedURL  string
		expectedDesc string
		shouldError  bool
	}{
		{
			input:        "https://example.com/image.jpg:Test description",
			expectedURL:  "https://example.com/image.jpg",
			expectedDesc: "Test description",
			shouldError:  false,
		},
		{
			input:        "https://cdn.hardcover.app/covers/1234.jpg:Book cover",
			expectedURL:  "https://cdn.hardcover.app/covers/1234.jpg",
			expectedDesc: "Book cover",
			shouldError:  false,
		},
		{
			input:       "no-colon-here",
			shouldError: true,
		},
		{
			input:       ":empty-url",
			shouldError: true,
		},
		{
			input:       "url-no-description:",
			shouldError: true,
		},
	}

	for _, tc := range testCases {
		// Parse format: url:description
		lastColonIndex := strings.LastIndex(tc.input, ":")
		if lastColonIndex == -1 || lastColonIndex == 0 || lastColonIndex == len(tc.input)-1 {
			if !tc.shouldError {
				t.Errorf("Expected valid parsing for %q, but got error", tc.input)
			}
			continue
		}

		imageURL := strings.TrimSpace(tc.input[:lastColonIndex])
		description := strings.TrimSpace(tc.input[lastColonIndex+1:])

		if tc.shouldError {
			t.Errorf("Expected error for %q, but got valid parsing", tc.input)
			continue
		}

		if imageURL != tc.expectedURL {
			t.Errorf("Expected URL %q, got %q", tc.expectedURL, imageURL)
		}

		if description != tc.expectedDesc {
			t.Errorf("Expected description %q, got %q", tc.expectedDesc, description)
		}
	}
}

func TestBulkLookupIntegration(t *testing.T) {
	// Skip integration test if no Hardcover token is available
	if os.Getenv("HARDCOVER_TOKEN") == "" {
		t.Skip("Skipping integration test: HARDCOVER_TOKEN not set")
	}

	// This is an integration test that would call the actual API
	// We're not running it by default to avoid hitting the API during tests
	t.Skip("Integration test skipped by default")
}
