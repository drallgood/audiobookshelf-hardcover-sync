package testutils

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGenerateExampleJSON(t *testing.T) {
	tmpFile := "test_example.json"
	defer os.Remove(tmpFile) // Clean up

	err := generateExampleJSON(tmpFile)
	if err != nil {
		t.Fatalf("generateExampleJSON failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		t.Fatal("Example JSON file was not created")
	}

	// Verify file contains valid JSON
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read generated JSON: %v", err)
	}

	var input EditionCreatorInput
	err = json.Unmarshal(data, &input)
	if err != nil {
		t.Fatalf("Generated JSON is not valid: %v", err)
	}

	// Verify required fields are present
	if input.BookID == 0 {
		t.Error("BookID should not be zero")
	}
	if input.Title == "" {
		t.Error("Title should not be empty")
	}
	if input.ASIN == "" {
		t.Error("ASIN should not be empty")
	}
	if input.ImageURL == "" {
		t.Error("ImageURL should not be empty")
	}
	if len(input.AuthorIDs) == 0 {
		t.Error("AuthorIDs should not be empty")
	}
	if input.AudioLength == 0 {
		t.Error("AudioLength should not be zero")
	}
}

func TestParseAudibleDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"12h34m56s", 12*3600 + 34*60 + 56},
		{"1h", 3600},
		{"45m30s", 45*60 + 30},
		{"2h30", 2*3600 + 30*60},
		{"10 hours and 15 minutes", 10*3600 + 15*60},
	}

	for _, test := range tests {
		result, err := ParseAudibleDuration(test.input)
		if err != nil {
			t.Errorf("ParseAudibleDuration('%s') failed: %v", test.input, err)
			continue
		}
		if result != test.expected {
			t.Errorf("ParseAudibleDuration('%s') = %d, expected %d", test.input, result, test.expected)
		}
	}
}

func TestParseAudibleDurationInvalid(t *testing.T) {
	invalidInputs := []string{
		"invalid format",
		"",
		"just text",
		"123", // no units
	}

	for _, input := range invalidInputs {
		_, err := ParseAudibleDuration(input)
		if err == nil {
			t.Errorf("ParseAudibleDuration('%s') should have failed but didn't", input)
		}
	}
}

func TestEditionCreatorInputValidation(t *testing.T) {
	// Test that our input structure can be marshaled/unmarshaled correctly
	input := EditionCreatorInput{
		BookID:        123,
		Title:         "Test Book",
		Subtitle:      "A Test Subtitle",
		ImageURL:      "https://example.com/image.jpg",
		ASIN:          "B01TEST123",
		ISBN10:        "1234567890",
		ISBN13:        "9781234567890",
		AuthorIDs:     []int{1, 2, 3},
		NarratorIDs:   []int{4, 5},
		PublisherID:   10,
		ReleaseDate:   "2024-01-01",
		AudioLength:   7200,
		EditionFormat: "Audible Audio",
		EditionInfo:   "Unabridged",
		LanguageID:    1,
		CountryID:     1,
	}

	// Marshal to JSON
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Failed to marshal EditionCreatorInput: %v", err)
	}

	// Unmarshal back
	var decoded EditionCreatorInput
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal EditionCreatorInput: %v", err)
	}

	// Verify all fields match
	if decoded.BookID != input.BookID {
		t.Errorf("BookID mismatch: got %d, expected %d", decoded.BookID, input.BookID)
	}
	if decoded.Title != input.Title {
		t.Errorf("Title mismatch: got %s, expected %s", decoded.Title, input.Title)
	}
	if decoded.Subtitle != input.Subtitle {
		t.Errorf("Subtitle mismatch: got %s, expected %s", decoded.Subtitle, input.Subtitle)
	}
	if decoded.ISBN10 != input.ISBN10 {
		t.Errorf("ISBN10 mismatch: got %s, expected %s", decoded.ISBN10, input.ISBN10)
	}
	if decoded.ISBN13 != input.ISBN13 {
		t.Errorf("ISBN13 mismatch: got %s, expected %s", decoded.ISBN13, input.ISBN13)
	}
	if decoded.EditionFormat != input.EditionFormat {
		t.Errorf("EditionFormat mismatch: got %s, expected %s", decoded.EditionFormat, input.EditionFormat)
	}
	if decoded.EditionInfo != input.EditionInfo {
		t.Errorf("EditionInfo mismatch: got %s, expected %s", decoded.EditionInfo, input.EditionInfo)
	}
	if decoded.LanguageID != input.LanguageID {
		t.Errorf("LanguageID mismatch: got %d, expected %d", decoded.LanguageID, input.LanguageID)
	}
	if decoded.CountryID != input.CountryID {
		t.Errorf("CountryID mismatch: got %d, expected %d", decoded.CountryID, input.CountryID)
	}
	if len(decoded.AuthorIDs) != len(input.AuthorIDs) {
		t.Errorf("AuthorIDs length mismatch: got %d, expected %d", len(decoded.AuthorIDs), len(input.AuthorIDs))
	}
	if decoded.AudioLength != input.AudioLength {
		t.Errorf("AudioLength mismatch: got %d, expected %d", decoded.AudioLength, input.AudioLength)
	}
}

func TestEditionCreatorConfigurableFields(t *testing.T) {
	// Test with all optional fields provided
	t.Run("AllFieldsProvided", func(t *testing.T) {
		input := EditionCreatorInput{
			BookID:        123,
			Title:         "Test Book",
			Subtitle:      "An Epic Tale",
			ImageURL:      "https://example.com/image.jpg",
			ASIN:          "B01TEST123",
			ISBN10:        "1234567890",
			ISBN13:        "9781234567890",
			AuthorIDs:     []int{1, 2},
			NarratorIDs:   []int{3, 4},
			PublisherID:   10,
			ReleaseDate:   "2024-01-01",
			AudioLength:   7200,
			EditionFormat: "Unabridged Audible Audio",
			EditionInfo:   "Enhanced Audio Experience",
			LanguageID:    2, // Spanish
			CountryID:     3, // Canada
		}

		// Test JSON serialization
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("Failed to marshal input: %v", err)
		}

		var decoded EditionCreatorInput
		err = json.Unmarshal(data, &decoded)
		if err != nil {
			t.Fatalf("Failed to unmarshal input: %v", err)
		}

		// Verify all fields are preserved
		if decoded.Subtitle != "An Epic Tale" {
			t.Errorf("Subtitle not preserved: got %s", decoded.Subtitle)
		}
		if decoded.ISBN10 != "1234567890" {
			t.Errorf("ISBN10 not preserved: got %s", decoded.ISBN10)
		}
		if decoded.ISBN13 != "9781234567890" {
			t.Errorf("ISBN13 not preserved: got %s", decoded.ISBN13)
		}
		if decoded.EditionFormat != "Unabridged Audible Audio" {
			t.Errorf("EditionFormat not preserved: got %s", decoded.EditionFormat)
		}
		if decoded.EditionInfo != "Enhanced Audio Experience" {
			t.Errorf("EditionInfo not preserved: got %s", decoded.EditionInfo)
		}
		if decoded.LanguageID != 2 {
			t.Errorf("LanguageID not preserved: got %d", decoded.LanguageID)
		}
		if decoded.CountryID != 3 {
			t.Errorf("CountryID not preserved: got %d", decoded.CountryID)
		}
	})

	// Test with minimal required fields only
	t.Run("MinimalFields", func(t *testing.T) {
		input := EditionCreatorInput{
			BookID:      123,
			Title:       "Minimal Book",
			ImageURL:    "https://example.com/image.jpg",
			ASIN:        "B01MIN123",
			AuthorIDs:   []int{1},
			ReleaseDate: "2024-01-01",
			AudioLength: 3600,
		}

		// Test JSON serialization
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("Failed to marshal minimal input: %v", err)
		}

		var decoded EditionCreatorInput
		err = json.Unmarshal(data, &decoded)
		if err != nil {
			t.Fatalf("Failed to unmarshal minimal input: %v", err)
		}

		// Verify optional fields are empty/zero
		if decoded.Subtitle != "" {
			t.Errorf("Subtitle should be empty: got %s", decoded.Subtitle)
		}
		if decoded.ISBN10 != "" {
			t.Errorf("ISBN10 should be empty: got %s", decoded.ISBN10)
		}
		if decoded.ISBN13 != "" {
			t.Errorf("ISBN13 should be empty: got %s", decoded.ISBN13)
		}
		if decoded.EditionFormat != "" {
			t.Errorf("EditionFormat should be empty: got %s", decoded.EditionFormat)
		}
		if decoded.EditionInfo != "" {
			t.Errorf("EditionInfo should be empty: got %s", decoded.EditionInfo)
		}
		if decoded.LanguageID != 0 {
			t.Errorf("LanguageID should be zero: got %d", decoded.LanguageID)
		}
		if decoded.CountryID != 0 {
			t.Errorf("CountryID should be zero: got %d", decoded.CountryID)
		}
	})
}

func TestCreateEditionWithAllNewFields(t *testing.T) {
	// Set environment for dry run to avoid actual API calls
	os.Setenv("DRY_RUN", "true")
	defer os.Unsetenv("DRY_RUN")

	// Create input with all new configurable fields
	input := EditionCreatorInput{
		BookID:        999999,
		Title:         "Complete Test Book",
		Subtitle:      "With All New Fields",
		ImageURL:      "https://example.com/test.jpg",
		ASIN:          "B01FULLTEST",
		ISBN10:        "0123456789",
		ISBN13:        "9780123456789",
		AuthorIDs:     []int{100, 200},
		NarratorIDs:   []int{300, 400},
		PublisherID:   500,
		ReleaseDate:   "2025-06-08",
		AudioLength:   14400, // 4 hours
		EditionFormat: "Premium Audible Audio",
		EditionInfo:   "Director's Cut Edition",
		LanguageID:    2, // Non-default language
		CountryID:     3, // Non-default country
	}

	// Test that CreateHardcoverEdition processes all fields without error
	result, err := CreateHardcoverEdition(input)
	if err != nil {
		t.Fatalf("CreateHardcoverEdition failed: %v", err)
	}

	// Verify dry run returned expected fake IDs
	if result.EditionID != 777777 {
		t.Errorf("Expected edition ID 777777 in dry run, got %d", result.EditionID)
	}
	if result.ImageID != 888888 {
		t.Errorf("Expected image ID 888888 in dry run, got %d", result.ImageID)
	}
}

func TestEditionCreationWithDefaults(t *testing.T) {
	// Set environment for dry run
	os.Setenv("DRY_RUN", "true")
	defer os.Unsetenv("DRY_RUN")

	// Create input with minimal fields, relying on defaults
	input := EditionCreatorInput{
		BookID:      888888,
		Title:       "Minimal Test Book",
		ImageURL:    "https://example.com/minimal.jpg",
		ASIN:        "B01MINIMAL",
		AuthorIDs:   []int{111},
		ReleaseDate: "2025-01-01",
		AudioLength: 3600,
		// All new fields are empty/zero, should use defaults
	}

	// Test that CreateHardcoverEdition handles defaults correctly
	result, err := CreateHardcoverEdition(input)
	if err != nil {
		t.Fatalf("CreateHardcoverEdition with defaults failed: %v", err)
	}

	// Verify dry run returned expected fake IDs
	if result.EditionID != 777777 {
		t.Errorf("Expected edition ID 777777 in dry run, got %d", result.EditionID)
	}
	if result.ImageID != 888888 {
		t.Errorf("Expected image ID 888888 in dry run, got %d", result.ImageID)
	}
}

// Test conversion from prepopulated to input
func TestConvertPrepopulatedToInput(t *testing.T) {
	prepopulated := &PrepopulatedEditionInput{
		BookID:        12345,
		Title:         "Test Book",
		Subtitle:      "Test Subtitle",
		ImageURL:      "https://example.com/image.jpg",
		ASIN:          "B123456789",
		ISBN10:        "1234567890",
		ISBN13:        "9781234567890",
		AuthorIDs:     []int{1, 2},
		NarratorIDs:   []int{3, 4},
		PublisherID:   100,
		ReleaseDate:   "2023-01-01",
		AudioSeconds:  3600,
		EditionFormat: "Audible Audio",
		EditionInfo:   "Unabridged",
		LanguageID:    2, // Non-default language
		CountryID:     3, // Non-default country
	}

	input := convertPrepopulatedToInput(prepopulated)

	// Verify all fields are correctly transferred
	tests := []struct {
		name     string
		expected interface{}
		actual   interface{}
	}{
		{"BookID", 12345, input.BookID},
		{"Title", "Test Book", input.Title},
		{"Subtitle", "Test Subtitle", input.Subtitle},
		{"ImageURL", "https://example.com/image.jpg", input.ImageURL},
		{"ASIN", "B123456789", input.ASIN},
		{"ISBN10", "1234567890", input.ISBN10},
		{"ISBN13", "9781234567890", input.ISBN13},
		{"PublisherID", 100, input.PublisherID},
		{"ReleaseDate", "2023-01-01", input.ReleaseDate},
		{"AudioLength", 3600, input.AudioLength},
		{"EditionFormat", "Audible Audio", input.EditionFormat},
		{"EditionInfo", "Unabridged", input.EditionInfo},
		{"LanguageID", 2, input.LanguageID},
		{"CountryID", 3, input.CountryID},
		{"AuthorIDs length", 2, len(input.AuthorIDs)},
		{"NarratorIDs length", 2, len(input.NarratorIDs)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.actual != test.expected {
				t.Errorf("Expected %s to be %v, got %v", test.name, test.expected, test.actual)
			}
		})
	}
}

func TestPrepopulatedDataStructure(t *testing.T) {
	// Test that PrepopulatedEditionInput includes all new fields
	prepopulated := &PrepopulatedEditionInput{
		BookID:        12345,
		Title:         "Test Book",
		Subtitle:      "Test Subtitle",
		ImageURL:      "https://example.com/image.jpg",
		ASIN:          "B123456789",
		ISBN10:        "1234567890",
		ISBN13:        "9781234567890",
		AuthorIDs:     []int{1, 2},
		NarratorIDs:   []int{3, 4},
		PublisherID:   100,
		ReleaseDate:   "2023-01-01",
		AudioSeconds:  3600,
		EditionFormat: "Audible Audio",
		EditionInfo:   "Unabridged",
		LanguageID:    2,
		CountryID:     3,
	}

	// Convert to JSON and back to verify serialization
	jsonData, err := json.MarshalIndent(prepopulated, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal prepopulated data: %v", err)
	}

	var unmarshaled PrepopulatedEditionInput
	err = json.Unmarshal(jsonData, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal prepopulated data: %v", err)
	}

	// Verify all new fields are preserved
	if unmarshaled.Subtitle != "Test Subtitle" {
		t.Errorf("Expected subtitle 'Test Subtitle', got %s", unmarshaled.Subtitle)
	}
	if unmarshaled.ISBN10 != "1234567890" {
		t.Errorf("Expected ISBN10 '1234567890', got %s", unmarshaled.ISBN10)
	}
	if unmarshaled.ISBN13 != "9781234567890" {
		t.Errorf("Expected ISBN13 '9781234567890', got %s", unmarshaled.ISBN13)
	}
	if unmarshaled.EditionFormat != "Audible Audio" {
		t.Errorf("Expected edition format 'Audible Audio', got %s", unmarshaled.EditionFormat)
	}
	if unmarshaled.EditionInfo != "Unabridged" {
		t.Errorf("Expected edition info 'Unabridged', got %s", unmarshaled.EditionInfo)
	}
	if unmarshaled.LanguageID != 2 {
		t.Errorf("Expected language ID 2, got %d", unmarshaled.LanguageID)
	}
	if unmarshaled.CountryID != 3 {
		t.Errorf("Expected country ID 3, got %d", unmarshaled.CountryID)
	}
}
