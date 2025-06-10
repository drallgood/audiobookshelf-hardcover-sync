package main

import (
	"os"
	"testing"
)

func TestEnhanceWithExternalDataIntegration(t *testing.T) {
	// Save original environment variables
	originalAPIEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	originalAPIToken := os.Getenv("AUDIBLE_API_TOKEN")
	
	// Restore environment variables after test
	defer func() {
		os.Setenv("AUDIBLE_API_ENABLED", originalAPIEnabled)
		os.Setenv("AUDIBLE_API_TOKEN", originalAPIToken)
	}()

	tests := []struct {
		name                 string
		apiEnabled           string
		apiToken             string
		asin                 string
		initialData          *PrepopulatedEditionInput
		expectedError        bool
		expectedSource       string
		expectedEnhancements map[string]interface{}
	}{
		{
			name:       "API disabled - only updates ASIN",
			apiEnabled: "false",
			asin:       "B01234567X",
			initialData: &PrepopulatedEditionInput{
				BookID:              123,
				Title:               "Test Book",
				PrepopulationSource: "hardcover",
			},
			expectedError:  false,
			expectedSource: "hardcover+external",
			expectedEnhancements: map[string]interface{}{
				"asin": "B01234567X",
			},
		},
		{
			name:       "API enabled but no token - fallback behavior",
			apiEnabled: "true",
			apiToken:   "",
			asin:       "B01234567X",
			initialData: &PrepopulatedEditionInput{
				BookID:              123,
				Title:               "Test Book",
				PrepopulationSource: "hardcover",
			},
			expectedError:  false,
			expectedSource: "hardcover+external",
			expectedEnhancements: map[string]interface{}{
				"asin": "B01234567X",
			},
		},
		{
			name:       "empty ASIN - no enhancement",
			apiEnabled: "true",
			asin:       "",
			initialData: &PrepopulatedEditionInput{
				BookID:              123,
				Title:               "Test Book",
				PrepopulationSource: "hardcover",
			},
			expectedError:  false,
			expectedSource: "hardcover",
			expectedEnhancements: map[string]interface{}{},
		},
		{
			name:       "invalid ASIN format - handled gracefully",
			apiEnabled: "true",
			asin:       "invalid-asin",
			initialData: &PrepopulatedEditionInput{
				BookID:              123,
				Title:               "Test Book",
				PrepopulationSource: "hardcover",
			},
			expectedError:  false,
			expectedSource: "hardcover+external",
			expectedEnhancements: map[string]interface{}{
				"asin": "invalid-asin",
			},
		},
		{
			name:       "preserves existing data when API fails",
			apiEnabled: "true",
			asin:       "B01234567X",
			initialData: &PrepopulatedEditionInput{
				BookID:              123,
				Title:               "Test Book",
				ReleaseDate:         "2023-01-01",
				AudioSeconds:        3600,
				PrepopulationSource: "hardcover",
			},
			expectedError:  false,
			expectedSource: "hardcover+external",
			expectedEnhancements: map[string]interface{}{
				"asin":         "B01234567X",
				"release_date": "2023-01-01",
				"audio_seconds": 3600,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			os.Setenv("AUDIBLE_API_ENABLED", tt.apiEnabled)
			os.Setenv("AUDIBLE_API_TOKEN", tt.apiToken)
			
			// Make a copy of initial data to avoid modifying the original
			testData := *tt.initialData
			
			// Call the function
			err := enhanceWithExternalData(&testData, tt.asin)
			
			// Check error expectation
			if tt.expectedError && err == nil {
				t.Errorf("Expected error but got none")
				return
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			// Check prepopulation source
			if testData.PrepopulationSource != tt.expectedSource {
				t.Errorf("Expected prepopulation source '%s', got '%s'", tt.expectedSource, testData.PrepopulationSource)
			}
			
			// Check specific enhancements
			for field, expectedValue := range tt.expectedEnhancements {
				switch field {
				case "asin":
					if testData.ASIN != expectedValue.(string) {
						t.Errorf("Expected ASIN '%s', got '%s'", expectedValue.(string), testData.ASIN)
					}
				case "release_date":
					if testData.ReleaseDate != expectedValue.(string) {
						t.Errorf("Expected release date '%s', got '%s'", expectedValue.(string), testData.ReleaseDate)
					}
				case "audio_seconds":
					if testData.AudioSeconds != expectedValue.(int) {
						t.Errorf("Expected audio seconds %d, got %d", expectedValue.(int), testData.AudioSeconds)
					}
				}
			}
		})
	}
}

func TestEnhanceWithExternalDataDateSpecificity(t *testing.T) {
	// Save original environment variables
	originalAPIEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	defer os.Setenv("AUDIBLE_API_ENABLED", originalAPIEnabled)

	// Enable API for this test (but it will fail and fallback)
	os.Setenv("AUDIBLE_API_ENABLED", "false")

	tests := []struct {
		name              string
		existingDate      string
		mockAudibleDate   string
		expectedDate      string
		shouldEnhance     bool
	}{
		{
			name:              "year only to full date",
			existingDate:      "2023",
			mockAudibleDate:   "2023-12-25",
			expectedDate:      "2023-12-25",
			shouldEnhance:     true,
		},
		{
			name:              "year-month to full date",
			existingDate:      "2023-12",
			mockAudibleDate:   "2023-12-25",
			expectedDate:      "2023-12-25",
			shouldEnhance:     true,
		},
		{
			name:              "full date to full date - no change",
			existingDate:      "2023-11-20",
			mockAudibleDate:   "2023-12-25",
			expectedDate:      "2023-11-20",
			shouldEnhance:     false,
		},
		{
			name:              "empty to full date",
			existingDate:      "",
			mockAudibleDate:   "2023-12-25",
			expectedDate:      "2023-12-25",
			shouldEnhance:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the date specificity logic directly
			result := isDateMoreSpecific(tt.mockAudibleDate, tt.existingDate)
			if result != tt.shouldEnhance {
				t.Errorf("isDateMoreSpecific(%q, %q) = %v; expected %v", 
					tt.mockAudibleDate, tt.existingDate, result, tt.shouldEnhance)
			}
		})
	}
}

func TestEnhanceWithExternalDataFieldPriority(t *testing.T) {
	// Test that enhancement preserves existing data when appropriate
	// and only enhances empty or less specific fields
	
	// Save original environment variables
	originalAPIEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	defer os.Setenv("AUDIBLE_API_ENABLED", originalAPIEnabled)

	// Disable API for controlled testing
	os.Setenv("AUDIBLE_API_ENABLED", "false")

	testData := &PrepopulatedEditionInput{
		BookID:              123,
		Title:               "Existing Title",
		Subtitle:            "Existing Subtitle",
		ReleaseDate:         "2023-01-01",
		AudioSeconds:        3600,
		ImageURL:            "https://example.com/existing.jpg",
		PrepopulationSource: "hardcover",
	}

	err := enhanceWithExternalData(testData, "B01234567X")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Verify that existing data is preserved
	if testData.Title != "Existing Title" {
		t.Errorf("Expected title to be preserved, got '%s'", testData.Title)
	}
	
	if testData.Subtitle != "Existing Subtitle" {
		t.Errorf("Expected subtitle to be preserved, got '%s'", testData.Subtitle)
	}
	
	if testData.ReleaseDate != "2023-01-01" {
		t.Errorf("Expected release date to be preserved, got '%s'", testData.ReleaseDate)
	}
	
	if testData.AudioSeconds != 3600 {
		t.Errorf("Expected audio seconds to be preserved, got %d", testData.AudioSeconds)
	}
	
	if testData.ImageURL != "https://example.com/existing.jpg" {
		t.Errorf("Expected image URL to be preserved, got '%s'", testData.ImageURL)
	}
	
	// Verify ASIN was added
	if testData.ASIN != "B01234567X" {
		t.Errorf("Expected ASIN to be added, got '%s'", testData.ASIN)
	}
	
	// Verify source was updated
	if testData.PrepopulationSource != "hardcover+external" {
		t.Errorf("Expected source to be updated to 'hardcover+external', got '%s'", testData.PrepopulationSource)
	}
}
