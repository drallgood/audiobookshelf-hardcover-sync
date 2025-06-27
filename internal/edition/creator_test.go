package edition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockHardcoverClient is a mock implementation of the HardcoverClient interface
type MockHardcoverClient struct {
	mock.Mock
}

// GetAuthHeader mocks the GetAuthHeader method
func (m *MockHardcoverClient) GetAuthHeader() string {
	args := m.Called()
	return args.String(0)
}

// GetEdition mocks the GetEdition method
func (m *MockHardcoverClient) GetEdition(ctx context.Context, id string) (*models.Edition, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Edition), args.Error(1)
}

// GetEditionByISBN13 mocks the GetEditionByISBN13 method
func (m *MockHardcoverClient) GetEditionByISBN13(ctx context.Context, isbn13 string) (*models.Edition, error) {
	args := m.Called(ctx, isbn13)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Edition), args.Error(1)
}

// GetEditionByASIN mocks the GetEditionByASIN method
func (m *MockHardcoverClient) GetEditionByASIN(ctx context.Context, asin string) (*models.Edition, error) {
	args := m.Called(ctx, asin)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Edition), args.Error(1)
}



// GraphQLQuery mocks the GraphQLQuery method
func (m *MockHardcoverClient) GraphQLQuery(ctx context.Context, query string, variables map[string]interface{}, response interface{}) error {
	args := m.Called(ctx, query, variables, response)
	return args.Error(0)
}

// GraphQLMutation mocks the GraphQLMutation method
func (m *MockHardcoverClient) GraphQLMutation(ctx context.Context, query string, variables map[string]interface{}, response interface{}) error {
	args := m.Called(ctx, query, variables, response)
	
	// Handle different response types
	switch resp := response.(type) {
	case *struct{ Data json.RawMessage }:
		// Handle raw JSON response
		if rawData, ok := args.Get(3).(*struct{ Data json.RawMessage }); ok && rawData != nil {
			resp.Data = rawData.Data
		}
	case *struct{ Data struct{ InsertEdition struct{ ID *int; Errors []string } } }:
		// Handle structured response for edition creation
		if respData, ok := args.Get(3).(map[string]interface{}); ok {
			if data, ok := respData["data"].(map[string]interface{}); ok {
				if insertData, ok := data["insert_edition"].(map[string]interface{}); ok {
					if id, ok := insertData["id"].(*int); ok {
						resp.Data.InsertEdition.ID = id
					}
					if errors, ok := insertData["errors"].([]string); ok {
						resp.Data.InsertEdition.Errors = errors
					}
				}
			}
		}
	}
	
	return args.Error(0)
}

// GetGoogleUploadCredentials mocks the GetGoogleUploadCredentials method
func (m *MockHardcoverClient) GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*edition.GoogleUploadInfo, error) {
	args := m.Called(ctx, filename, editionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*edition.GoogleUploadInfo), args.Error(1)
}

// newTestCreator creates a new test creator with a mock HTTP client
func newTestCreator(t *testing.T, mockClient *MockHardcoverClient) *edition.Creator {
	t.Helper()

	// Create a mock HTTP transport that returns a minimal PNG image
	mockTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// Return a minimal 1x1 transparent PNG
		imgData := []byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
			0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
			0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 image
			0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
			0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, // IDAT chunk
			0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
			0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, // IEND chunk
			0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(imgData)),
			Header: http.Header{
				"Content-Type": []string{"image/png"},
			},
		}, nil
	})

	// Create a new HTTP client with the mock transport
	httpClient := &http.Client{
		Transport: mockTransport,
	}

	// Create a new creator with the mock HTTP client
	return edition.NewCreatorWithHTTPClient(
		mockClient,
		logger.Get(),
		false,
		"",
		httpClient,
	)
}

// roundTripFunc is a function that implements http.RoundTripper
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	// If this is an image upload request, return a successful response
	if strings.Contains(req.URL.String(), "https://api.hardcover.app/v1/upload") {
		// Create a response with the image ID
		response := map[string]interface{}{
			"id": 12345,
		}
		jsonData, _ := json.Marshal(response)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(jsonData)),
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
		}, nil
	}
	// For image download requests, return a dummy image
	if strings.HasPrefix(req.URL.String(), "http") && (strings.HasSuffix(req.URL.Path, ".jpg") || strings.HasSuffix(req.URL.Path, ".png")) {
		imgData := []byte("dummy image data")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(imgData)),
			Header: http.Header{
				"Content-Type":   []string{"image/jpeg"},
				"Content-Length": []string{strconv.Itoa(len(imgData))},
			},
		}, nil
	}
	return f(req)
}

func TestEditionCreator_CreateEdition(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	// HTTP client is already defined in roundTripFunc type
	// Use the default HTTP client with our custom transport

	tests := []struct {
		name          string
		input         *edition.EditionInput
		setupMock     func(*MockHardcoverClient)
		expectError   bool
		expectSuccess bool
		expectImageID int
	}{
		{
			name: "API error",
			input: &edition.EditionInput{
				BookID:    999,
				Title:     "Error Book",
				AuthorIDs: []int{1},
			},
			setupMock: func(m *MockHardcoverClient) {
				m.On("GetAuthHeader").Return("Bearer test-token").Once()
				m.On("GraphQLMutation", mock.Anything, mock.MatchedBy(func(query string) bool {
					return strings.Contains(query, "mutation CreateEdition")
				}), mock.Anything, mock.Anything).
					Run(func(args mock.Arguments) {
						// Simulate an error response
						respPtr := args.Get(3).(*struct {
							Data struct {
								InsertEdition struct {
									ID     *int     `json:"id"`
									Errors []string `json:"errors"`
								} `json:"insert_edition"`
							} `json:"data"`
						})
						respPtr.Data.InsertEdition.Errors = []string{"API error"}
					}).Return(nil).Once()
			},
			expectError:   true,
			expectSuccess: false,
		},
		{
			name: "missing required fields",
			input: &edition.EditionInput{
				BookID: 789,
				// Missing required fields
			},
			setupMock: func(m *MockHardcoverClient) {
				// No mocks needed as validation should fail before any API calls
			},
			expectError:   true,
			expectSuccess: false,
		},
		{
			name: "valid input without image",
			input: &edition.EditionInput{
				BookID:     123,
				Title:      "Test Book",
				AuthorIDs:  []int{1, 2},
				NarratorIDs: []int{3},
				PublisherID: 1,
				ReleaseDate: "2020-01-01",
			},
			setupMock: func(m *MockHardcoverClient) {
				// Setup mock expectations for a successful edition creation without image
				m.On("GetAuthHeader").Return("Bearer test-token").Once()
				m.On("GraphQLMutation", mock.Anything, mock.MatchedBy(func(query string) bool {
					return strings.Contains(query, "mutation CreateAudiobookEdition")
				}), mock.Anything, mock.Anything).
					Run(func(args mock.Arguments) {
						// Get the response pointer and set a success response
						respPtr := args.Get(3).(*struct {
							Data struct {
								CreateEdition struct {
									ID    int    `json:"id"`
									Title string `json:"title"`
								} `json:"createEdition"`
							} `json:"data"`
							Errors []struct {
								Message string `json:"message"`
							} `json:"errors,omitempty"`
						})
						respPtr.Data.CreateEdition.ID = 123
						respPtr.Data.CreateEdition.Title = "Test Book"
					}).Return(nil).Once()
			},
			expectError:   false,
			expectSuccess: true,
		},
		{
			name: "valid input with image",
			input: &edition.EditionInput{
				BookID:     456,
				Title:      "Test Book with Image",
				AuthorIDs:  []int{4, 5},
				NarratorIDs: []int{6},
				PublisherID: 2,
				ReleaseDate: "2021-01-01",
				ImageURL:    "http://example.com/cover.jpg",
			},
			setupMock: func(m *MockHardcoverClient) {
				// Setup mock expectations for a successful edition creation with image
				// First call for image upload
				m.On("GetAuthHeader").Return("Bearer test-token").Once()
				
				// Mock the GraphQL mutation for edition creation
				expectedMutation := `
	mutation CreateEdition($bookId: Int!, $edition: EditionInput!) {
	  insert_edition(book_id: $bookId, edition: $edition) {
	    id
	    errors
	  }
	}`

				m.On("GraphQLMutation", mock.Anything, mock.MatchedBy(func(query string) bool {
					// Normalize whitespace for comparison
					expected := strings.TrimSpace(expectedMutation)
					actual := strings.TrimSpace(query)
					return expected == actual
				}), mock.Anything, mock.Anything).
					Run(func(args mock.Arguments) {
						// Get the response pointer and set a success response
						respPtr := args.Get(3).(*struct {
							Data struct {
								InsertEdition struct {
									ID     *int     `json:"id"`
									Errors []string `json:"errors"`
								} `json:"insert_edition"`
							} `json:"data"`
						})
						editionID := 456
						respPtr.Data.InsertEdition.ID = &editionID
					}).Return(nil).Once()

				// Mock the Google upload credentials call
				uploadInfo := &edition.GoogleUploadInfo{
					URL:    "https://storage.googleapis.com/upload/storage/v1/b/hardcover/o",
					Fields: map[string]string{"key": "covers/456.jpg"},
				}
				m.On("GetGoogleUploadCredentials", mock.Anything, mock.Anything, mock.Anything).Return(uploadInfo, nil).Once()
				
				// Second call for edition creation (after image upload)
				m.On("GetAuthHeader").Return("Bearer test-token").Once()
			},
			expectError:   false,
			expectSuccess: true,
			expectImageID: 789,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new mock client for each test
			mockClient := new(MockHardcoverClient)

			// Create a new creator with the mock client
			creator := newTestCreator(t, mockClient)

			// Setup test-specific mock expectations
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			// Execute the test
			editionID, err := creator.CreateEdition(context.Background(), tt.input)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, editionID)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, editionID)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestEditionCreator_PrepopulateFromBook(t *testing.T) {
	// Setup
	mockClient := new(MockHardcoverClient)
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "text",
	})
	log := logger.Get()
	
	// Create a new creator with the mock client
	creator := edition.NewCreator(mockClient, log, false, "")

	tests := []struct {
		name        string
		bookID      int
		setupMock   func(*MockHardcoverClient)
		expectError bool
	}{
		{
			name:   "successful prepopulation",
			bookID: 123,
			setupMock: func(m *MockHardcoverClient) {
				// Mock GetEdition call
				m.On("GetEdition", mock.Anything, "123").
					Return(&models.Edition{ID: "123"}, nil)

				// Mock GraphQLQuery call
				m.On("GraphQLQuery", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil).
				Run(func(args mock.Arguments) {
					// The response is a pointer to a struct that directly contains the Book field
					respPtr := args.Get(3).(*struct {
						Book struct {
							ID           int    `json:"id"`
							Title        string `json:"title"`
							Subtitle     string `json:"subtitle"`
							CoverImageURL string `json:"coverImageUrl"`
							ISBN10        string `json:"isbn10"`
							ISBN13        string `json:"isbn13"`
							ASIN          string `json:"asin"`
							PublishedDate string `json:"publishedDate"`
							Authors       []struct {
								ID   int    `json:"id"`
								Name string `json:"name"`
							} `json:"authors"`
							Narrators []struct {
								ID   int    `json:"id"`
								Name string `json:"name"`
							} `json:"narrators"`
							Publisher *struct {
								ID   int    `json:"id"`
								Name string `json:"name"`
							} `json:"publisher"`
							Language *struct {
								ID   int    `json:"id"`
								Name string `json:"name"`
							} `json:"language"`
							Country *struct {
								ID   int    `json:"id"`
								Name string `json:"name"`
							} `json:"country"`
						} `json:"book"`
					})

					// Set the response values
					resp := *respPtr
					resp.Book = struct {
						ID           int    `json:"id"`
						Title        string `json:"title"`
						Subtitle     string `json:"subtitle"`
						CoverImageURL string `json:"coverImageUrl"`
						ISBN10        string `json:"isbn10"`
						ISBN13        string `json:"isbn13"`
						ASIN          string `json:"asin"`
						PublishedDate string `json:"publishedDate"`
						Authors       []struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						} `json:"authors"`
						Narrators []struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						} `json:"narrators"`
						Publisher *struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						} `json:"publisher"`
						Language *struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						} `json:"language"`
						Country *struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						} `json:"country"`
					}{
						ID:           123,
						Title:        "Test Book",
						Subtitle:     "A Test Subtitle",
						CoverImageURL: "http://example.com/cover.jpg",
						ISBN10:        "1234567890",
						ISBN13:        "9781234567890",
						ASIN:          "B00TEST123",
						PublishedDate: "2023-01-01",
						Authors: []struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						}{
							{ID: 1, Name: "Author One"},
							{ID: 2, Name: "Author Two"},
						},
						Narrators: []struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						}{
							{ID: 3, Name: "Narrator One"},
						},
					}

					// Set publisher, language, and country as pointers
					publisher := &struct {
						ID   int    `json:"id"`
						Name string `json:"name"`
					}{
						ID:   1,
						Name: "Test Publisher",
					}
					resp.Book.Publisher = publisher

					language := &struct {
						ID   int    `json:"id"`
						Name string `json:"name"`
					}{
						ID:   1,
						Name: "English",
					}
					resp.Book.Language = language

					country := &struct {
						ID   int    `json:"id"`
						Name string `json:"name"`
					}{
						ID:   1,
						Name: "United States",
					}
					resp.Book.Country = country

					// Assign back to the response pointer
					*respPtr = resp
				})
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			result, err := creator.PrepopulateFromBook(context.Background(), tt.bookID)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.bookID, result.BookID)
				assert.NotEmpty(t, result.Title)
				assert.NotEmpty(t, result.AuthorIDs)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestEditionInput_Validate(t *testing.T) {
	tests := []struct {
		name        string
		input       *edition.EditionInput
		expectError bool
	}{
		{
			name: "valid input",
			input: &edition.EditionInput{
				BookID:    123,
				Title:     "Test Book",
				AuthorIDs: []int{1, 2},
			},
			expectError: false,
		},
		{
			name: "missing book ID",
			input: &edition.EditionInput{
				Title:     "Test Book",
				AuthorIDs: []int{1, 2},
			},
			expectError: true,
		},
		{
			name: "missing title",
			input: &edition.EditionInput{
				BookID:    123,
				AuthorIDs: []int{1, 2},
			},
			expectError: true,
		},
		{
			name: "missing authors",
			input: &edition.EditionInput{
				BookID: 123,
				Title:  "Test Book",
			},
			expectError: true,
		},
		{
			name: "invalid date format",
			input: &edition.EditionInput{
				BookID:      123,
				Title:       "Test Book",
				AuthorIDs:   []int{1, 2},
				ReleaseDate: "2023/01/01", // Invalid format
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEditionInput_JSON(t *testing.T) {
	input := &edition.EditionInput{
		BookID:     123,
		Title:      "Test Book",
		Subtitle:   "A Test Subtitle",
		AuthorIDs:  []int{1, 2},
		NarratorIDs: []int{3, 4},
		PublisherID: 5,
	}

	// Test marshaling
	data, err := json.Marshal(input)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	// Test unmarshaling
	var decoded edition.EditionInput
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, input.BookID, decoded.BookID)
	assert.Equal(t, input.Title, decoded.Title)
	assert.Equal(t, input.Subtitle, decoded.Subtitle)
	assert.ElementsMatch(t, input.AuthorIDs, decoded.AuthorIDs)
	assert.ElementsMatch(t, input.NarratorIDs, decoded.NarratorIDs)
	assert.Equal(t, input.PublisherID, decoded.PublisherID)
}

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()
	os.Exit(code)
}
