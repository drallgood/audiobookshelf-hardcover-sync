package main

import (
	"testing"
	"time"
)

func TestIsValidASIN(t *testing.T) {
	tests := []struct {
		name     string
		asin     string
		expected bool
	}{
		{
			name:     "valid ASIN with letters and numbers",
			asin:     "B01234567X",
			expected: true,
		},
		{
			name:     "valid ASIN all numbers",
			asin:     "1234567890",
			expected: true,
		},
		{
			name:     "valid ASIN all letters",
			asin:     "ABCDEFGHIJ",
			expected: true,
		},
		{
			name:     "invalid ASIN too short",
			asin:     "B0123456",
			expected: false,
		},
		{
			name:     "invalid ASIN too long",
			asin:     "B01234567XX",
			expected: false,
		},
		{
			name:     "invalid ASIN with lowercase",
			asin:     "b01234567x",
			expected: false,
		},
		{
			name:     "invalid ASIN with special characters",
			asin:     "B0123456-X",
			expected: false,
		},
		{
			name:     "empty ASIN",
			asin:     "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidASIN(tt.asin)
			if result != tt.expected {
				t.Errorf("isValidASIN(%q) = %v; expected %v", tt.asin, result, tt.expected)
			}
		})
	}
}

func TestParseAudibleDate(t *testing.T) {
	tests := []struct {
		name        string
		dateStr     string
		expectError bool
		expected    string // Expected format: "2006-01-02"
	}{
		{
			name:        "YYYY-MM-DD format",
			dateStr:     "2023-12-25",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "YYYY/MM/DD format",
			dateStr:     "2023/12/25",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "MM/DD/YYYY format",
			dateStr:     "12/25/2023",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "Month DD, YYYY format",
			dateStr:     "December 25, 2023",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "Mon DD, YYYY format",
			dateStr:     "Dec 25, 2023",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "DD Month YYYY format",
			dateStr:     "25 December 2023",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "DD Mon YYYY format",
			dateStr:     "25 Dec 2023",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "ISO 8601 format",
			dateStr:     "2023-12-25T10:30:00Z",
			expectError: false,
			expected:    "2023-12-25",
		},
		{
			name:        "invalid date format",
			dateStr:     "invalid-date",
			expectError: true,
		},
		{
			name:        "empty string",
			dateStr:     "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseAudibleDate(tt.dateStr)
			
			if tt.expectError {
				if err == nil {
					t.Errorf("parseAudibleDate(%q) expected error but got none", tt.dateStr)
				}
				return
			}
			
			if err != nil {
				t.Errorf("parseAudibleDate(%q) unexpected error: %v", tt.dateStr, err)
				return
			}
			
			resultStr := result.Format("2006-01-02")
			if resultStr != tt.expected {
				t.Errorf("parseAudibleDate(%q) = %q; expected %q", tt.dateStr, resultStr, tt.expected)
			}
		})
	}
}

func TestFormatAudibleDate(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			name:     "valid date",
			input:    time.Date(2023, 12, 25, 10, 30, 0, 0, time.UTC),
			expected: "2023-12-25",
		},
		{
			name:     "zero time",
			input:    time.Time{},
			expected: "",
		},
		{
			name:     "leap year date",
			input:    time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
			expected: "2024-02-29",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatAudibleDate(tt.input)
			if result != tt.expected {
				t.Errorf("formatAudibleDate(%v) = %q; expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsDateMoreSpecific(t *testing.T) {
	tests := []struct {
		name         string
		newDate      string
		existingDate string
		expected     bool
	}{
		{
			name:         "full date vs year only",
			newDate:      "2023-12-25",
			existingDate: "2023",
			expected:     true,
		},
		{
			name:         "full date vs year-month",
			newDate:      "2023-12-25",
			existingDate: "2023-12",
			expected:     true,
		},
		{
			name:         "year-month vs year only",
			newDate:      "2023-12",
			existingDate: "2023",
			expected:     true,
		},
		{
			name:         "year only vs full date",
			newDate:      "2023",
			existingDate: "2023-12-25",
			expected:     false,
		},
		{
			name:         "same specificity",
			newDate:      "2023-12-25",
			existingDate: "2023-11-20",
			expected:     false,
		},
		{
			name:         "new date vs empty",
			newDate:      "2023-12-25",
			existingDate: "",
			expected:     true,
		},
		{
			name:         "empty vs existing",
			newDate:      "",
			existingDate: "2023",
			expected:     false,
		},
		{
			name:         "formatted date vs year",
			newDate:      "Dec 25, 2023",
			existingDate: "2023",
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDateMoreSpecific(tt.newDate, tt.existingDate)
			if result != tt.expected {
				t.Errorf("isDateMoreSpecific(%q, %q) = %v; expected %v", tt.newDate, tt.existingDate, result, tt.expected)
			}
		})
	}
}

func TestCountDateComponents(t *testing.T) {
	tests := []struct {
		name     string
		dateStr  string
		expected int
	}{
		{
			name:     "full date YYYY-MM-DD",
			dateStr:  "2023-12-25",
			expected: 3,
		},
		{
			name:     "full date Month DD, YYYY",
			dateStr:  "December 25, 2023",
			expected: 3,
		},
		{
			name:     "year-month YYYY-MM",
			dateStr:  "2023-12",
			expected: 2,
		},
		{
			name:     "year-month Mon YYYY",
			dateStr:  "Dec 2023",
			expected: 2,
		},
		{
			name:     "year only",
			dateStr:  "2023",
			expected: 1,
		},
		{
			name:     "empty string",
			dateStr:  "",
			expected: 0,
		},
		{
			name:     "unparseable with separators",
			dateStr:  "2023-??-??",
			expected: 3,
		},
		{
			name:     "unparseable with one separator",
			dateStr:  "2023-??",
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countDateComponents(tt.dateStr)
			if result != tt.expected {
				t.Errorf("countDateComponents(%q) = %d; expected %d", tt.dateStr, result, tt.expected)
			}
		})
	}
}

func TestIsAudibleImageBetter(t *testing.T) {
	tests := []struct {
		name        string
		audibleURL  string
		existingURL string
		expected    bool
	}{
		{
			name:        "audible high-res vs non-audible",
			audibleURL:  "https://m.media-amazon.com/images/I/51abc123._SL500_.jpg",
			existingURL: "https://example.com/image.jpg",
			expected:    true,
		},
		{
			name:        "audible large vs non-audible",
			audibleURL:  "https://audible.com/images/large/book.jpg",
			existingURL: "https://example.com/image.jpg",
			expected:    true,
		},
		{
			name:        "audible vs existing audible",
			audibleURL:  "https://audible.com/images/500/book.jpg",
			existingURL: "https://audible.com/images/book.jpg",
			expected:    false,
		},
		{
			name:        "low-res audible vs non-audible",
			audibleURL:  "https://audible.com/images/book.jpg",
			existingURL: "https://example.com/image.jpg",
			expected:    false,
		},
		{
			name:        "empty audible URL",
			audibleURL:  "",
			existingURL: "https://example.com/image.jpg",
			expected:    false,
		},
		{
			name:        "audible vs empty existing",
			audibleURL:  "https://audible.com/images/book.jpg",
			existingURL: "",
			expected:    true,
		},
		{
			name:        "audible 1000 vs non-audible",
			audibleURL:  "https://audible.com/images/1000/book.jpg",
			existingURL: "https://example.com/image.jpg",
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAudibleImageBetter(tt.audibleURL, tt.existingURL)
			if result != tt.expected {
				t.Errorf("isAudibleImageBetter(%q, %q) = %v; expected %v", tt.audibleURL, tt.existingURL, result, tt.expected)
			}
		})
	}
}

func TestConvertAudibleResponse(t *testing.T) {
	// Test the conversion of Audible API response to internal metadata format
	response := &AudibleAPIResponse{
		Product: struct {
			ASIN     string `json:"asin"`
			Title    string `json:"title"`
			Subtitle string `json:"subtitle"`
			Authors  []struct {
				Name string `json:"name"`
			} `json:"authors"`
			Narrators []struct {
				Name string `json:"name"`
			} `json:"narrators"`
			Publisher struct {
				Name string `json:"name"`
			} `json:"publisher"`
			ReleaseDate string `json:"release_date"`
			Runtime     struct {
				LengthMs int `json:"length_mins"`
			} `json:"runtime"`
			ProductImages struct {
				Large string `json:"500"`
			} `json:"product_images"`
			Language struct {
				Name string `json:"name"`
			} `json:"language"`
		}{
			ASIN:     "B01234567X",
			Title:    "Test Book",
			Subtitle: "A Test Subtitle",
			Authors: []struct {
				Name string `json:"name"`
			}{
				{Name: "Author One"},
				{Name: "Author Two"},
			},
			Narrators: []struct {
				Name string `json:"name"`
			}{
				{Name: "Narrator One"},
			},
			Publisher: struct {
				Name string `json:"name"`
			}{
				Name: "Test Publisher",
			},
			ReleaseDate: "2023-12-25",
			Runtime: struct {
				LengthMs int `json:"length_mins"`
			}{
				LengthMs: 480, // 8 hours in minutes
			},
			ProductImages: struct {
				Large string `json:"500"`
			}{
				Large: "https://audible.com/image.jpg",
			},
			Language: struct {
				Name string `json:"name"`
			}{
				Name: "English",
			},
		},
	}

	result := convertAudibleResponse(response)

	// Test basic fields
	if result.ASIN != "B01234567X" {
		t.Errorf("Expected ASIN 'B01234567X', got '%s'", result.ASIN)
	}
	
	if result.Title != "Test Book" {
		t.Errorf("Expected title 'Test Book', got '%s'", result.Title)
	}
	
	if result.Subtitle != "A Test Subtitle" {
		t.Errorf("Expected subtitle 'A Test Subtitle', got '%s'", result.Subtitle)
	}
	
	if result.Publisher != "Test Publisher" {
		t.Errorf("Expected publisher 'Test Publisher', got '%s'", result.Publisher)
	}
	
	if result.Language != "English" {
		t.Errorf("Expected language 'English', got '%s'", result.Language)
	}
	
	if result.ImageURL != "https://audible.com/image.jpg" {
		t.Errorf("Expected image URL 'https://audible.com/image.jpg', got '%s'", result.ImageURL)
	}
	
	// Test authors
	if len(result.Authors) != 2 {
		t.Errorf("Expected 2 authors, got %d", len(result.Authors))
	} else {
		if result.Authors[0] != "Author One" {
			t.Errorf("Expected first author 'Author One', got '%s'", result.Authors[0])
		}
		if result.Authors[1] != "Author Two" {
			t.Errorf("Expected second author 'Author Two', got '%s'", result.Authors[1])
		}
	}
	
	// Test narrators
	if len(result.Narrators) != 1 {
		t.Errorf("Expected 1 narrator, got %d", len(result.Narrators))
	} else {
		if result.Narrators[0] != "Narrator One" {
			t.Errorf("Expected narrator 'Narrator One', got '%s'", result.Narrators[0])
		}
	}
	
	// Test duration conversion (minutes to seconds)
	expectedDuration := 480 * 60 // 8 hours * 60 minutes/hour * 60 seconds/minute
	if result.Duration != expectedDuration {
		t.Errorf("Expected duration %d seconds, got %d", expectedDuration, result.Duration)
	}
	
	// Test release date parsing
	expectedDate := time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC)
	if !result.ReleaseDate.Equal(expectedDate) {
		t.Errorf("Expected release date %v, got %v", expectedDate, result.ReleaseDate)
	}
}
