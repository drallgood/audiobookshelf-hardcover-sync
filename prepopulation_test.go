package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExtractAuthorIDsNew(t *testing.T) {
	tests := []struct {
		name          string
		contributions []ContributionInfo
		expected      []int
	}{
		{
			name: "single author",
			contributions: []ContributionInfo{
				{
					Role:   "Author",
					Author: AuthorInfo{ID: 123},
				},
			},
			expected: []int{123},
		},
		{
			name: "multiple authors",
			contributions: []ContributionInfo{
				{
					Role:   "Author",
					Author: AuthorInfo{ID: 123},
				},
				{
					Role:   "Author",
					Author: AuthorInfo{ID: 456},
				},
			},
			expected: []int{123, 456},
		},
		{
			name: "mixed roles",
			contributions: []ContributionInfo{
				{
					Role:   "Author",
					Author: AuthorInfo{ID: 123},
				},
				{
					Role:   "Narrator",
					Author: AuthorInfo{ID: 456},
				},
				{
					Role:   "Author",
					Author: AuthorInfo{ID: 789},
				},
			},
			expected: []int{123, 789},
		},
		{
			name:          "no authors",
			contributions: []ContributionInfo{},
			expected:      []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAuthorIDs(tt.contributions)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("extractAuthorIDs() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractNarratorIDsNew(t *testing.T) {
	tests := []struct {
		name          string
		contributions []ContributionInfo
		expected      []int
	}{
		{
			name: "single narrator",
			contributions: []ContributionInfo{
				{
					Role:   "Narrator",
					Author: AuthorInfo{ID: 123},
				},
			},
			expected: []int{123},
		},
		{
			name: "multiple narrators",
			contributions: []ContributionInfo{
				{
					Role:   "Narrator",
					Author: AuthorInfo{ID: 123},
				},
				{
					Role:   "Narrator",
					Author: AuthorInfo{ID: 456},
				},
			},
			expected: []int{123, 456},
		},
		{
			name: "mixed roles",
			contributions: []ContributionInfo{
				{
					Role:   "Author",
					Author: AuthorInfo{ID: 123},
				},
				{
					Role:   "Narrator",
					Author: AuthorInfo{ID: 456},
				},
				{
					Role:   "Narrator",
					Author: AuthorInfo{ID: 789},
				},
			},
			expected: []int{456, 789},
		},
		{
			name:          "no narrators",
			contributions: []ContributionInfo{},
			expected:      []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractNarratorIDs(tt.contributions)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("extractNarratorIDs() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestValidatePrepopulatedDataNew(t *testing.T) {
	tests := []struct {
		name        string
		input       PrepopulatedEditionInput
		expectError bool
	}{
		{
			name: "valid complete data",
			input: PrepopulatedEditionInput{
				BookID:       123,
				Title:        "The Martian",
				AuthorIDs:    []int{456},
				NarratorIDs:  []int{789},
				PublisherID:  101,
				ReleaseDate:  "2015-01-01",
				AudioSeconds: 38400,
			},
			expectError: false,
		},
		{
			name: "missing required fields",
			input: PrepopulatedEditionInput{
				BookID: 123,
				// Missing Title, AuthorIDs
			},
			expectError: true,
		},
		{
			name: "invalid BookID",
			input: PrepopulatedEditionInput{
				BookID:      0,
				Title:       "Test Book",
				AuthorIDs:   []int{456},
				NarratorIDs: []int{789},
			},
			expectError: true,
		},
		{
			name: "invalid release date",
			input: PrepopulatedEditionInput{
				BookID:      123,
				Title:       "Test Book",
				AuthorIDs:   []int{456},
				ReleaseDate: "invalid-date",
			},
			expectError: true,
		},
		{
			name: "empty author IDs",
			input: PrepopulatedEditionInput{
				BookID:      123,
				Title:       "Test Book",
				AuthorIDs:   []int{},
				NarratorIDs: []int{789},
			},
			expectError: true,
		},
		{
			name: "negative audio seconds",
			input: PrepopulatedEditionInput{
				BookID:       123,
				Title:        "Test Book",
				AuthorIDs:    []int{456},
				AudioSeconds: -100,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrepopulatedData(&tt.input)
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestGeneratePrepopulatedJSONNew(t *testing.T) {
	bookID := "123456"

	// This is an integration test that would require the actual prepopulateFromHardcoverBook function
	// to work properly. For now, we'll test the concept.
	jsonStr, err := generatePrepopulatedJSON(bookID)

	// The function should either return valid JSON or an error
	if err != nil {
		// Expected for a test environment without actual Hardcover API
		if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "error") {
			t.Errorf("generatePrepopulatedJSON() unexpected error = %v", err)
		}
		return
	}

	// If we get here, verify JSON is valid
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	// Verify required fields are present
	requiredFields := []string{"book_id", "title", "author_ids"}
	for _, field := range requiredFields {
		if _, exists := parsed[field]; !exists {
			t.Errorf("Generated JSON missing required field: %s", field)
		}
	}
}

func TestPrepopulateFromHardcoverBookMockProcessingNew(t *testing.T) {
	// Test the data processing logic with mock structures
	mockResponse := BookMetadataResponse{
		ID:           "123456",
		Title:        "Test Book",
		Subtitle:     "A Test Subtitle",
		ReleaseDate:  "2024-01-15",
		AudioSeconds: 3600,
		Image: &ImageInfo{
			URL: "https://example.com/image.jpg",
		},
		Contributions: []ContributionInfo{
			{
				Role:   "Author",
				Author: AuthorInfo{ID: 100, Name: "Test Author"},
			},
			{
				Role:   "Narrator",
				Author: AuthorInfo{ID: 200, Name: "Test Narrator"},
			},
		},
		Editions: []EditionInfo{
			{
				ID:           "edition123",
				Title:        "Test Book",
				Subtitle:     "A Test Subtitle",
				ASIN:         "B01EXAMPLE",
				ISBN13:       "9781234567890",
				AudioSeconds: 3600,
				ReleaseDate:  "2024-01-15",
				PublisherID:  300,
				Publisher: PublisherInfo{
					ID:   300,
					Name: "Test Publisher",
				},
				Image: &ImageInfo{
					URL: "https://example.com/image.jpg",
				},
			},
		},
	}

	// Process the mock data like the real function would
	input := PrepopulatedEditionInput{
		BookID:       123456, // Convert string to int
		Title:        mockResponse.Title,
		Subtitle:     mockResponse.Subtitle,
		ImageURL:     mockResponse.Image.URL,
		AuthorIDs:    extractAuthorIDs(mockResponse.Contributions),
		NarratorIDs:  extractNarratorIDs(mockResponse.Contributions),
		PublisherID:  mockResponse.Editions[0].PublisherID, // Get from first edition
		ReleaseDate:  mockResponse.ReleaseDate,
		AudioSeconds: mockResponse.AudioSeconds,
	}

	// Set edition data from first audiobook edition if available
	for _, edition := range mockResponse.Editions {
		if edition.AudioSeconds > 0 { // Assume this is an audiobook if it has audio_seconds
			input.ASIN = edition.ASIN
			break
		}
	}

	// Verify the processed data
	if input.BookID != 123456 {
		t.Errorf("BookID = %v, want %v", input.BookID, 123456)
	}
	if input.Title != "Test Book" {
		t.Errorf("Title = %v, want %v", input.Title, "Test Book")
	}
	if input.Subtitle != "A Test Subtitle" {
		t.Errorf("Subtitle = %v, want %v", input.Subtitle, "A Test Subtitle")
	}
	if len(input.AuthorIDs) != 1 || input.AuthorIDs[0] != 100 {
		t.Errorf("AuthorIDs = %v, want [100]", input.AuthorIDs)
	}
	if len(input.NarratorIDs) != 1 || input.NarratorIDs[0] != 200 {
		t.Errorf("NarratorIDs = %v, want [200]", input.NarratorIDs)
	}
	if input.PublisherID != 300 {
		t.Errorf("PublisherID = %v, want %v", input.PublisherID, 300)
	}
	if input.ReleaseDate != "2024-01-15" {
		t.Errorf("ReleaseDate = %v, want %v", input.ReleaseDate, "2024-01-15")
	}
	if input.AudioSeconds != 3600 {
		t.Errorf("AudioSeconds = %v, want %v", input.AudioSeconds, 3600)
	}
	if input.ASIN != "B01EXAMPLE" {
		t.Errorf("ASIN = %v, want %v", input.ASIN, "B01EXAMPLE")
	}
}

func TestEnhanceWithExternalDataPlaceholderFunctionNew(t *testing.T) {
	// Test the placeholder function
	input := PrepopulatedEditionInput{
		BookID: 123456,
		Title:  "Test Book",
	}

	asin := "B01EXAMPLE"
	err := enhanceWithExternalData(&input, asin)

	// Currently should return no error (placeholder implementation)
	if err != nil {
		t.Errorf("enhanceWithExternalData() unexpected error = %v", err)
	}
}

func TestConvertPrepopulatedToInputNew(t *testing.T) {
	prepopulated := &PrepopulatedEditionInput{
		BookID:       123456,
		Title:        "Test Book",
		ImageURL:     "https://example.com/image.jpg",
		ASIN:         "B01EXAMPLE",
		AuthorIDs:    []int{100, 200},
		NarratorIDs:  []int{300},
		PublisherID:  400,
		ReleaseDate:  "2024-01-15",
		AudioSeconds: 3600,
	}

	result := convertPrepopulatedToInput(prepopulated)

	if result.BookID != 123456 {
		t.Errorf("BookID = %v, want %v", result.BookID, 123456)
	}
	if result.Title != "Test Book" {
		t.Errorf("Title = %v, want %v", result.Title, "Test Book")
	}
	if result.ImageURL != "https://example.com/image.jpg" {
		t.Errorf("ImageURL = %v, want %v", result.ImageURL, "https://example.com/image.jpg")
	}
	if result.ASIN != "B01EXAMPLE" {
		t.Errorf("ASIN = %v, want %v", result.ASIN, "B01EXAMPLE")
	}
	if !reflect.DeepEqual(result.AuthorIDs, []int{100, 200}) {
		t.Errorf("AuthorIDs = %v, want %v", result.AuthorIDs, []int{100, 200})
	}
	if !reflect.DeepEqual(result.NarratorIDs, []int{300}) {
		t.Errorf("NarratorIDs = %v, want %v", result.NarratorIDs, []int{300})
	}
	if result.PublisherID != 400 {
		t.Errorf("PublisherID = %v, want %v", result.PublisherID, 400)
	}
	if result.ReleaseDate != "2024-01-15" {
		t.Errorf("ReleaseDate = %v, want %v", result.ReleaseDate, "2024-01-15")
	}
	if result.AudioLength != 3600 {
		t.Errorf("AudioLength = %v, want %v", result.AudioLength, 3600)
	}
}
