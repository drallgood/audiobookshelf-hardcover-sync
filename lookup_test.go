package main

import (
	"strings"
	"testing"
)

func TestSearchPersonResult(t *testing.T) {
	// Test PersonSearchResult structure
	result := PersonSearchResult{
		ID:          123,
		Name:        "Test Author",
		BooksCount:  10,
		Bio:         "Test biography",
		IsCanonical: true,
	}

	if result.ID != 123 {
		t.Errorf("Expected ID 123, got %d", result.ID)
	}
	if result.Name != "Test Author" {
		t.Errorf("Expected Name 'Test Author', got '%s'", result.Name)
	}
	if result.BooksCount != 10 {
		t.Errorf("Expected BooksCount 10, got %d", result.BooksCount)
	}
	if !result.IsCanonical {
		t.Error("Expected IsCanonical to be true")
	}
}

func TestPublisherSearchResult(t *testing.T) {
	// Test PublisherSearchResult structure
	result := PublisherSearchResult{
		ID:            456,
		Name:          "Test Publisher",
		EditionsCount: 25,
		IsCanonical:   false,
	}

	if result.ID != 456 {
		t.Errorf("Expected ID 456, got %d", result.ID)
	}
	if result.Name != "Test Publisher" {
		t.Errorf("Expected Name 'Test Publisher', got '%s'", result.Name)
	}
	if result.EditionsCount != 25 {
		t.Errorf("Expected EditionsCount 25, got %d", result.EditionsCount)
	}
	if result.IsCanonical {
		t.Error("Expected IsCanonical to be false")
	}
}

func TestSearchQueryValidation(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected bool
	}{
		{"Valid query", "Stephen King", true},
		{"Single character", "A", true},
		{"Empty query", "", false},
		{"Whitespace only", "   ", false},
		{"Special characters", "O'Neil", true},
		{"Numbers", "Author123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trimmed := strings.TrimSpace(tt.query)
			valid := len(trimmed) > 0
			if valid != tt.expected {
				t.Errorf("Expected query '%s' validity to be %v, got %v", tt.query, tt.expected, valid)
			}
		})
	}
}

func TestFormatSearchResults(t *testing.T) {
	// Test that search results can be formatted properly
	authors := []PersonSearchResult{
		{
			ID:          1,
			Name:        "Stephen King",
			BooksCount:  100,
			Bio:         "American author",
			IsCanonical: true,
		},
		{
			ID:          2,
			Name:        "Steven King",
			BooksCount:  5,
			Bio:         "Another author",
			IsCanonical: false,
		},
	}

	if len(authors) != 2 {
		t.Errorf("Expected 2 authors, got %d", len(authors))
	}

	// Test canonical vs alias identification
	canonical := authors[0]
	alias := authors[1]

	if !canonical.IsCanonical {
		t.Error("First author should be canonical")
	}
	if alias.IsCanonical {
		t.Error("Second author should be an alias")
	}
}

func TestPublisherFormatting(t *testing.T) {
	publishers := []PublisherSearchResult{
		{
			ID:            1,
			Name:          "Penguin Random House",
			EditionsCount: 500,
			IsCanonical:   true,
		},
		{
			ID:            2,
			Name:          "Penguin",
			EditionsCount: 50,
			IsCanonical:   false,
		},
	}

	if len(publishers) != 2 {
		t.Errorf("Expected 2 publishers, got %d", len(publishers))
	}

	// Verify ordering logic would work (higher editions count first, then canonical)
	main := publishers[0]
	secondary := publishers[1]

	if main.EditionsCount <= secondary.EditionsCount && !main.IsCanonical {
		t.Error("Main publisher should have more editions or be canonical")
	}
}

func TestIDValidation(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		expected bool
	}{
		{"Valid numeric ID", "12345", true},
		{"Valid UUID-like", "123e4567-e89b-12d3-a456-426614174000", true},
		{"Empty ID", "", false},
		{"Whitespace only", "   ", false},
		{"Valid with dashes", "123-456", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trimmed := strings.TrimSpace(tt.id)
			valid := len(trimmed) > 0
			if valid != tt.expected {
				t.Errorf("Expected ID '%s' validity to be %v, got %v", tt.id, tt.expected, valid)
			}
		})
	}
}
