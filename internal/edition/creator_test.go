package edition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

// ZeroIDMockHardcoverClient is a specialized mock for testing invalid response formats
// that specifically returns a zero ID for image creation operations
type ZeroIDMockHardcoverClient struct {
	MockHardcoverClient
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

// GetEditionByISBN10 mocks the GetEditionByISBN10 method
func (m *MockHardcoverClient) GetEditionByISBN10(ctx context.Context, isbn10 string) (*models.Edition, error) {
	args := m.Called(ctx, isbn10)
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

// GetBookByID mocks the GetBookByID method
func (m *MockHardcoverClient) GetBookByID(ctx context.Context, bookID string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, bookID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

// GraphQLQuery mocks the GraphQLQuery method
func (m *MockHardcoverClient) GraphQLQuery(ctx context.Context, query string, variables map[string]interface{}, response interface{}) error {
	args := m.Called(ctx, query, variables, response)
	return args.Error(0)
}

// GraphQLMutation implements the interface method for MockHardcoverClient
func (m *MockHardcoverClient) GraphQLMutation(ctx context.Context, mutation string, variables map[string]interface{}, result interface{}) error {
	// Call the mock with any type for the result
	args := m.Called(ctx, mutation, variables, result)

	// Only manipulate the result if we didn't return an error
	if args.Error(0) == nil {
		// We need to handle different operations differently
		if strings.Contains(mutation, "insert_image") {
			// Return the same JSON shape as the Hardcover insert_image response.
			return json.Unmarshal([]byte(`{"insert_image":{"id":456}}`), result)
		}
	}

	return args.Error(0)
}

// GraphQLMutation override for ZeroIDMockHardcoverClient - always sets image ID to 0
func (m *ZeroIDMockHardcoverClient) GraphQLMutation(ctx context.Context, mutation string, variables map[string]interface{}, result interface{}) error {
	// Call the mock with any type for the result
	args := m.Called(ctx, mutation, variables, result)

	// Only manipulate the result if we didn't return an error
	if args.Error(0) == nil {
		// For the ZeroID mock, return an image response with ID 0 to test validation logic.
		if strings.Contains(mutation, "insert_image") {
			// Return the same JSON shape as the Hardcover insert_image response.
			return json.Unmarshal([]byte(`{"insert_image":{"id":0}}`), result)
		}
	}

	return args.Error(0)
}

func decodeMockGraphQLResponse(t *testing.T, args mock.Arguments, payload string) {
	t.Helper()
	if err := json.Unmarshal([]byte(payload), args.Get(3)); err != nil {
		t.Fatalf("decode GraphQL response fixture: %v", err)
	}
}

// GetGoogleUploadCredentials mocks the GetGoogleUploadCredentials method
func (m *MockHardcoverClient) GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*edition.GoogleUploadInfo, error) {
	args := m.Called(ctx, filename, editionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*edition.GoogleUploadInfo), args.Error(1)
}

// UpdateUserBookEdition mocks the UpdateUserBookEdition method
func (m *MockHardcoverClient) UpdateUserBookEdition(ctx context.Context, userBookID, editionID int) error {
	args := m.Called(ctx, userBookID, editionID)
	return args.Error(0)
}

func newTestCreator(t *testing.T, client edition.HardcoverClient) *edition.Creator {
	t.Helper()

	// Create a test server to handle image uploads
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/upload") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "test-image-id",
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			return
		}

		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	// Create a test HTTP client that uses our test server
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				return url.Parse(ts.URL)
			},
		},
	}

	// Create a new creator with the mock HTTP client
	creator := edition.NewCreatorWithHTTPClient(
		client,
		logger.Get(),
		false,
		"",
		httpClient,
	)
	creator.EnableCoverUpload()
	return creator
}

func TestEditionCreator_CreateEdition(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	// Common mock setup for all tests
	setupCommonMocks := func(m *MockHardcoverClient) {
		// Mock GetAuthHeader to be called multiple times
		m.On("GetAuthHeader").Return("Bearer test-token").Maybe()
	}

	tests := []struct {
		name        string
		input       *edition.EditionInput
		setupMock   func(*testing.T, *MockHardcoverClient)
		expectError bool
	}{
		{
			name: "API error",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        999,
				Title:         "Error Book",
				AuthorIDs:     []int{1},
			},
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				setupCommonMocks(m)

				// Setup expectations for the error case
				expectedBookID := 999

				// Mock the GraphQL mutation to return an error
				m.On("GraphQLMutation", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(assert.AnError).
					Run(func(args mock.Arguments) {
						// Verify the mutation variables
						variables := args.Get(2).(map[string]interface{})
						// Handle both int and float64 for ID fields
						switch v := variables["bookId"].(type) {
						case int:
							assert.Equal(t, expectedBookID, v)
						case float64:
							assert.Equal(t, float64(expectedBookID), v)
						default:
							assert.Fail(t, "Unexpected type for bookId: %T", v)
						}
						editionInput := variables["edition"].(map[string]interface{})
						dto := editionInput["dto"].(map[string]interface{})
						assert.Equal(t, "Error Book", dto["title"])
						assert.Equal(t, "Ebook", dto["edition_format"])
						// Handle both int and float64 for reading_format_id
						switch v := dto["reading_format_id"].(type) {
						case int:
							assert.Equal(t, 4, v)
						case float64:
							assert.Equal(t, float64(4), v)
						default:
							assert.Fail(t, "Unexpected type for reading_format_id: %T", v)
						}
					}).
					Once()
			},
			expectError: true,
		},
		{
			name: "missing required fields",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        789,
				// Missing required fields
			},
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				setupCommonMocks(m)
			},
			expectError: true,
		},
		{
			name: "valid input without image",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        123,
				Title:         "Test Book",
				AuthorIDs:     []int{1, 2},
				NarratorIDs:   []int{3},
				PublisherID:   1,
				ReleaseDate:   "2020-01-01",
			},
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				setupCommonMocks(m)

				// Mock the GraphQL mutation to return a successful response
				m.On("GraphQLMutation", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil).
					Run(func(args mock.Arguments) {
						// Verify the mutation variables
						variables := args.Get(2).(map[string]interface{})
						// Handle both int and float64 for bookId
						switch v := variables["bookId"].(type) {
						case int:
							assert.Equal(t, 123, v)
						case float64:
							assert.Equal(t, float64(123), v)
						default:
							assert.Fail(t, "Unexpected type for bookId: %T", v)
						}

						editionInput := variables["edition"].(map[string]interface{})
						dto := editionInput["dto"].(map[string]interface{})

						assert.Equal(t, "Test Book", dto["title"])
						assert.Equal(t, "Ebook", dto["edition_format"])

						// Handle both int and float64 for publisher_id
						switch v := dto["publisher_id"].(type) {
						case int:
							assert.Equal(t, 1, v)
						case float64:
							assert.Equal(t, float64(1), v)
						default:
							assert.Fail(t, "Unexpected type for publisher_id: %T", v)
						}

						assert.Equal(t, "2020-01-01", dto["release_date"])

						decodeMockGraphQLResponse(t, args, `{"insert_edition":{"id":123,"errors":[]}}`)
					}).
					Once()
			},
			expectError: false,
		},
		{
			name: "valid input with image",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        456,
				Title:         "Test Book with Image",
				AuthorIDs:     []int{4, 5},
				NarratorIDs:   []int{6},
				PublisherID:   2,
				ReleaseDate:   "2021-01-01",
				ImageURL:      "http://example.com/cover.jpg",
			},
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				setupCommonMocks(m)

				// Mock the GraphQL mutation to return a successful response
				m.On("GraphQLMutation", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil).
					Run(func(args mock.Arguments) {
						// Verify the mutation variables
						variables := args.Get(2).(map[string]interface{})
						// Handle both int and float64 for bookId
						switch v := variables["bookId"].(type) {
						case int:
							assert.Equal(t, 456, v)
						case float64:
							assert.Equal(t, float64(456), v)
						default:
							assert.Fail(t, "Unexpected type for bookId: %T", v)
						}

						_, ok := variables["edition"].(map[string]interface{})
						if !ok {
							t.Error("edition input is not a map")
						}

						decodeMockGraphQLResponse(t, args, `{"insert_edition":{"id":456,"errors":[]}}`)
					}).Once()

				// The actual implementation makes an HTTP request to get upload credentials
				// We'll let this happen but mock the HTTP response
				// The test will fail with a 404 since we're not setting up an HTTP mock server
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new mock client with test logger
			mockClient := &MockHardcoverClient{}

			// Create a new creator with the mock client
			creator := newTestCreator(t, mockClient)

			// Setup mock expectations after creating the creator
			if tt.setupMock != nil {
				tt.setupMock(t, mockClient)
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
				// Mock GraphQLQuery call
				m.On("GraphQLQuery", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil).
					Run(func(args mock.Arguments) {
						decodeMockGraphQLResponse(t, args, `{"book":{"id":123,"title":"Test Book","subtitle":"A Test Subtitle","coverImageUrl":"http://example.com/cover.jpg","isbn10":"1234567890","isbn13":"9781234567890","asin":"B00TEST123","publishedDate":"2023-01-01","authors":[{"id":1,"name":"Author One"},{"id":2,"name":"Author Two"}],"narrators":[{"id":3,"name":"Narrator One"}],"publisher":{"id":1,"name":"Test Publisher"},"language":{"id":1,"name":"English"},"country":{"id":1,"name":"United States"}}}`)
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
				assert.Equal(t, models.ReadingFormatAudiobook, result.ReadingFormat)
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
				ReadingFormat: "ebook",
				BookID:        123,
				Title:         "Test Book",
				AuthorIDs:     []int{1, 2},
			},
			expectError: false,
		},
		{
			name: "missing book ID",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				Title:         "Test Book",
				AuthorIDs:     []int{1, 2},
			},
			expectError: true,
		},
		{
			name: "negative book ID",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        -1,
				Title:         "Test Book",
				AuthorIDs:     []int{1, 2},
			},
			expectError: true,
		},
		{
			name: "missing title",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        123,
				AuthorIDs:     []int{1, 2},
			},
			expectError: true,
		},
		{
			name: "missing authors",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        123,
				Title:         "Test Book",
			},
			expectError: true,
		},
		{
			name: "invalid date format",
			input: &edition.EditionInput{
				ReadingFormat: "ebook",
				BookID:        123,
				Title:         "Test Book",
				AuthorIDs:     []int{1, 2},
				ReleaseDate:   "2023/01/01", // Invalid format
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
		ReadingFormat: "ebook",
		BookID:        123,
		Title:         "Test Book",
		Subtitle:      "A Test Subtitle",
		AuthorIDs:     []int{1, 2},
		NarratorIDs:   []int{3, 4},
		PublisherID:   5,
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

func TestEditionCreator_CreateImageRecord(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	tests := []struct {
		name            string
		editionID       int
		imageURL        string
		useZeroIDMock   bool                          // Flag to indicate that we should use the ZeroIDMockHardcoverClient
		setupMock       func(interface{}, *testing.T) // Change parameter to interface{} to handle both mock types
		expectedImageID int
		expectError     bool
		errorContains   string
	}{
		{
			name:          "successful image record creation",
			editionID:     123,
			imageURL:      "https://example.com/test.jpg",
			useZeroIDMock: false,
			setupMock: func(m interface{}, t *testing.T) {
				// Cast to MockHardcoverClient
				mockClient := m.(*MockHardcoverClient)

				// The mock returns a successful insert_image response.
				mockClient.On("GraphQLMutation",
					mock.Anything,
					mock.MatchedBy(func(query string) bool {
						return strings.Contains(query, "insert_image")
					}),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(nil)
			},
			expectedImageID: 456, // The default mock sets this ID
			expectError:     false,
		},
		{
			name:          "graphql mutation error",
			editionID:     123,
			imageURL:      "https://example.com/test.jpg",
			useZeroIDMock: false,
			setupMock: func(m interface{}, t *testing.T) {
				// Cast to MockHardcoverClient
				mockClient := m.(*MockHardcoverClient)

				mockClient.On("GraphQLMutation",
					mock.Anything,
					mock.AnythingOfType("string"),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(errors.New("graphql mutation failed"))
			},
			expectError:   true,
			errorContains: "graphql mutation failed",
		},
		{
			name:          "invalid response format",
			editionID:     123,
			imageURL:      "https://example.com/test.jpg",
			useZeroIDMock: true, // Use our specialized mock that sets ID to 0
			setupMock: func(m interface{}, t *testing.T) {
				// Cast to ZeroIDMockHardcoverClient
				mockClient := m.(*ZeroIDMockHardcoverClient)

				// Setup the expectation - ZeroIDMockHardcoverClient.GraphQLMutation will set ID to 0
				mockClient.On("GraphQLMutation",
					mock.Anything,
					mock.AnythingOfType("string"),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(nil)
			},
			expectError:   true,
			errorContains: "API response did not contain a valid image ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the appropriate mock client based on the test case
			var clientInterface edition.HardcoverClient

			if tt.useZeroIDMock {
				// Use our specialized mock that always sets ID to 0
				mockClient := new(ZeroIDMockHardcoverClient)
				clientInterface = mockClient

				// Setup mock expectations
				if tt.setupMock != nil {
					tt.setupMock(mockClient, t)
				}
			} else {
				// Use the standard mock
				mockClient := new(MockHardcoverClient)
				clientInterface = mockClient

				// Setup mock expectations
				if tt.setupMock != nil {
					tt.setupMock(mockClient, t)
				}
			}

			// Create creator with the appropriate mock client
			creator := newTestCreator(t, clientInterface)

			// Call the method
			imageID, err := creator.CreateImageRecord(context.Background(), tt.editionID, tt.imageURL)

			// Check expectations
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedImageID, imageID)
			}

			// Verify all mock expectations were met
			switch mockClient := clientInterface.(type) {
			case *MockHardcoverClient:
				mockClient.AssertExpectations(t)
			case *ZeroIDMockHardcoverClient:
				mockClient.AssertExpectations(t)
			}
		})
	}
}

// mockImageTransport is a custom http.RoundTripper that mocks image download responses
type mockImageTransport struct {
	test          string
	testServerURL string
}

// RoundTrip implements the http.RoundTripper interface
func (m *mockImageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Redirect Hardcover API requests to our test server
	if req.URL.Host == "hardcover.app" && m.testServerURL != "" {
		// Clone the request
		reqCopy := req.Clone(req.Context())

		// Parse the test server URL
		testURL, err := url.Parse(m.testServerURL)
		if err != nil {
			return nil, err
		}

		// Preserve the path and query
		reqCopy.URL.Scheme = testURL.Scheme
		reqCopy.URL.Host = testURL.Host

		// Add test case header for the test server to identify which test case is running
		reqCopy.Header.Set("X-Test-Case", m.test)

		// Use default transport to send to our test server
		return http.DefaultTransport.RoundTrip(reqCopy)
	}

	// Check if this is an image download request
	if req.Method == http.MethodGet {
		// For the upload_image_error test, we want the image download to succeed
		// so the code can proceed to call upload credentials endpoint (which will return an error)
		if m.test == "upload_image_error" && strings.Contains(req.URL.String(), "error.jpg") {
			// Return a successful response with fake image data
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("\xff\xd8\xff\xe0 fake JPEG data")), // JPEG magic bytes: covers must sniff as PNG or JPEG
				Header:     make(http.Header),
			}, nil
		}

		// For all other cases, return a mock image response
		header := make(http.Header)
		header.Set("Content-Type", "image/jpeg")

		// Return a small fake image (just some bytes that look like an image header)
		fakeImageBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(fakeImageBytes)),
			Header:     header,
		}, nil
	}

	// Handle GCS upload request (this is the POST to upload.example.com)
	if req.Method == http.MethodPost && strings.Contains(req.URL.String(), "upload.example.com") {
		// Return a successful response for uploads
		return &http.Response{
			StatusCode: http.StatusNoContent, // GCS returns 204 on successful upload
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	}

	return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL)
}

func TestEditionCreator_UploadEditionImage(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	// Create a test HTTP server to handle API requests
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if this is the upload credentials request
		if r.URL.Path == "/api/upload/google" {
			// Check the test case from the request headers
			testCase := r.Header.Get("X-Test-Case")

			switch testCase {
			case "upload_image_error":
				// Return an error response
				w.WriteHeader(http.StatusInternalServerError)
				_, err := w.Write([]byte(`{"error":"upload credentials error"}`))
				if err != nil {
					t.Fatalf("Failed to write error response: %v", err)
				}
			default:
				// Return a valid response for successful cases
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				response := `{
					"url": "https://upload.example.com",
					"fields": {
						"key": "uploads/covers/test-key.jpg",
						"policy": "test-policy",
						"x-goog-algorithm": "test-algo"
					}
				}`
				_, err := w.Write([]byte(response))
				if err != nil {
					t.Fatalf("Failed to write response data: %v", err)
				}
			}
			return
		}

		// For image upload endpoint, always return success
		if r.URL.Host == "upload.example.com" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// For image download requests
		w.Header().Set("Content-Type", "image/jpeg")
		_, err := w.Write([]byte("test image data"))
		if err != nil {
			t.Fatalf("Failed to write test image data: %v", err)
		}
	}))
	defer testServer.Close()

	tests := []struct {
		name          string
		editionID     int
		imageURL      string
		description   string
		setupMock     func(*testing.T, *MockHardcoverClient)
		expectError   bool
		errorContains string
	}{
		{
			name:        "success_case",
			editionID:   123,
			imageURL:    "https://example.com/test.jpg",
			description: "Test Cover",
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// Mock GetAuthHeader for direct HTTP implementation
				m.On("GetAuthHeader").Return("Bearer test-token")

				// Mock GraphQLMutation for image record creation
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_image") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(nil)

				// Mock GraphQLMutation for updating the edition
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.MatchedBy(func(variables map[string]interface{}) bool {
						id, ok := variables["id"].(int)
						if !ok || id != 123 {
							return false
						}
						editionInput, ok := variables["edition"].(map[string]interface{})
						if !ok {
							return false
						}
						dto, ok := editionInput["dto"].(map[string]interface{})
						if !ok {
							return false
						}
						imageID, ok := dto["image_id"].(int)
						return ok && imageID == 456
					}),
					mock.Anything,
				).Run(func(args mock.Arguments) {
					decodeMockGraphQLResponse(t, args, `{"update_edition":{"id":123,"errors":[]}}`)
				}).Return(nil)
			},
			expectError: false,
		},
		{
			name:        "upload_image_error",
			editionID:   123,
			imageURL:    "https://example.com/error.jpg",
			description: "Test Cover",
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// Mock GetAuthHeader but set up HTTP test server to return an error
				m.On("GetAuthHeader").Return("Bearer test-token")
			},
			expectError:   true,
			errorContains: "failed to upload image to GCS",
		},
		{
			name:        "create_image_record_error",
			editionID:   123,
			imageURL:    "https://example.com/test.jpg",
			description: "Test Cover",
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// Mock GetAuthHeader for direct HTTP implementation
				m.On("GetAuthHeader").Return("Bearer test-token")

				// Make GraphQLMutation for image record creation fail
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_image") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(fmt.Errorf("image record creation failed"))
			},
			expectError:   true,
			errorContains: "failed to create image record",
		},
		{
			name:        "update_edition_error",
			editionID:   123,
			imageURL:    "https://example.com/test.jpg",
			description: "Test Cover",
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// Mock GetAuthHeader for direct HTTP implementation
				m.On("GetAuthHeader").Return("Bearer test-token")

				// Mock GraphQLMutation for image record creation
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_image") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(nil)

				// Make GraphQLMutation for updating the edition fail
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(fmt.Errorf("edition update failed"))
			},
			expectError:   true,
			errorContains: "failed to update edition with new image",
		},
		{
			name:        "update_edition_missing_id",
			editionID:   123,
			imageURL:    "https://example.com/test.jpg",
			description: "Test Cover",
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				m.On("GetAuthHeader").Return("Bearer test-token")
				m.On("GraphQLMutation", mock.Anything,
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_image") }),
					mock.Anything, mock.Anything,
				).Return(nil)
				m.On("GraphQLMutation", mock.Anything,
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.Anything, mock.Anything,
				).Run(func(args mock.Arguments) {
					decodeMockGraphQLResponse(t, args, `{"update_edition":{"id":null,"errors":[]}}`)
				}).Return(nil)
			},
			expectError:   true,
			errorContains: "missing edition ID",
		},
		{
			name:        "update_edition_response_errors",
			editionID:   123,
			imageURL:    "https://example.com/test.jpg",
			description: "Test Cover",
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				m.On("GetAuthHeader").Return("Bearer test-token")
				m.On("GraphQLMutation", mock.Anything,
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_image") }),
					mock.Anything, mock.Anything,
				).Return(nil)
				m.On("GraphQLMutation", mock.Anything,
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.Anything, mock.Anything,
				).Run(func(args mock.Arguments) {
					decodeMockGraphQLResponse(t, args, `{"update_edition":{"id":123,"errors":["not updated"]}}`)
				}).Return(nil)
			},
			expectError:   true,
			errorContains: "edition update failed",
		},
		{
			name:        "invalid_edition_id",
			editionID:   0,
			imageURL:    "https://example.com/test.jpg",
			description: "Test Cover",
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				m.On("GetAuthHeader").Return("Bearer test-token")
				m.On("GraphQLMutation", mock.Anything,
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_image") }),
					mock.Anything, mock.Anything,
				).Return(nil)
			},
			expectError:   true,
			errorContains: "invalid edition ID or image ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new mock client
			mockClient := new(MockHardcoverClient)

			// Create a mock RoundTripper to handle image downloads
			mockTransport := &mockImageTransport{
				test:          tt.name,
				testServerURL: testServer.URL,
			}

			// Create a test HTTP client with our mock transport
			httpClient := &http.Client{
				Transport: mockTransport,
			}

			// Create a creator instance with mocks
			creator := edition.NewCreatorWithHTTPClient(
				mockClient,
				logger.Get(),
				false,
				"test-token",
				httpClient,
			)
			creator.EnableCoverUpload()

			// Setup mocks
			if tt.setupMock != nil {
				tt.setupMock(t, mockClient)
			}

			// Call the method under test
			err := creator.UploadEditionImage(context.Background(), tt.editionID, tt.imageURL, tt.description)

			// Verify results
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}

			// Verify all mocks were called as expected
			mockClient.AssertExpectations(t)
		})
	}
}

func TestEditionCreator_updateEditionImageRejectsZeroImageID(t *testing.T) {
	client := new(MockHardcoverClient)
	creator := newTestCreator(t, client)
	err := edition.NewTestHelpers(creator).UpdateEditionImage(context.Background(), 123, 0)

	assert.ErrorContains(t, err, "invalid edition ID or image ID")
	client.AssertNotCalled(t, "GraphQLMutation", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestEditionCreator_createEdition(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	tests := []struct {
		name          string
		input         *edition.EditionInput
		imageID       int
		setupMock     func(*testing.T, *MockHardcoverClient)
		expectedID    int
		expectError   bool
		errorContains string
	}{
		{
			name: "success_case",
			input: &edition.EditionInput{
				BookID:      123,
				Title:       "Test Edition",
				Subtitle:    "A Test",
				ASIN:        "B123456789",
				ISBN13:      "9781234567890",
				AuthorIDs:   []int{1, 2},
				NarratorIDs: []int{3, 4},
				PublisherID: 5,
				LanguageID:  6,
				CountryID:   7,
				AudioLength: 3600, // 1 hour
				ReleaseDate: "2023-01-01",
				EditionInfo: "First Edition",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// First, check GetEditionByASIN should return nil since no duplicate exists
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, models.ErrEditionNotFound)

				// Mock GraphQLMutation for creating the edition
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Run(func(args mock.Arguments) {
					// Set the ID in the response
					decodeMockGraphQLResponse(t, args, `{"insert_edition":{"id":789,"errors":[]}}`)
				}).Return(nil)
			},
			expectedID:  789,
			expectError: false,
		},
		{
			name: "existing_edition_by_asin",
			input: &edition.EditionInput{
				BookID: 123,
				Title:  "Test Edition",
				ASIN:   "B123456789",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// Return an existing edition for the ASIN
				existingEdition := &models.Edition{
					ID:     "555",
					BookID: "123",
					Title:  "Existing Edition",
				}
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(existingEdition, nil)
			},
			expectedID:  555, // Should return the existing edition's ID
			expectError: false,
		},
		{
			name: "mutation_error",
			input: &edition.EditionInput{
				BookID: 123,
				Title:  "Test Edition",
				ASIN:   "B123456789",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// First, check GetEditionByASIN should return nil since no duplicate exists
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, models.ErrEditionNotFound)

				// Make GraphQLMutation fail
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.Anything,
				).Return(fmt.Errorf("GraphQL mutation failed"))
			},
			expectedID:    0,
			expectError:   true,
			errorContains: "GraphQL mutation failed",
		},
		{
			name: "response_errors_with_existing_isbn13",
			input: &edition.EditionInput{
				BookID: 123,
				Title:  "Test Edition",
				ASIN:   "B123456789",
				ISBN13: "9781234567890",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// First, check GetEditionByASIN should return nil since no duplicate exists
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, models.ErrEditionNotFound)

				// Return errors in the GraphQL response suggesting duplication
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
					mock.MatchedBy(func(variables map[string]interface{}) bool {
						// Ensure the ISBN13 is correctly passed to the edition input
						if edition, ok := variables["edition"].(map[string]interface{}); ok {
							if dto, ok := edition["dto"].(map[string]interface{}); ok {
								// Make sure ISBN13 is correctly set in the variables
								isbn13, ok := dto["isbn_13"]
								return ok && isbn13 == "9781234567890"
							}
						}
						return false
					}),
					mock.Anything,
				).Run(func(args mock.Arguments) {
					// Set errors in the response
					decodeMockGraphQLResponse(t, args, `{"insert_edition":{"id":null,"errors":["Edition with this ISBN13 already exists"]}}`)
				}).Return(nil)

				// This is the critical part: Set up the second expectation for GetEditionByISBN13
				// It will be called after the GraphQL mutation returns the "already exists" error
				existingEdition := &models.Edition{
					ID:     "666",
					BookID: "123",
					Title:  "Existing Edition by ISBN13",
				}
				// The lookup before the insert finds nothing; only the one after the
				// duplicate error finds the edition.
				m.On("GetEditionByISBN13", mock.Anything, "9781234567890").Return(nil, models.ErrEditionNotFound).Once()
				m.On("GetEditionByISBN13", mock.Anything, "9781234567890").Return(existingEdition, nil).Once()
			},
			expectedID:  666, // Should return the existing edition's ID found by ISBN13
			expectError: false,
		},
		{
			name: "response_errors_with_existing_asin_in_response",
			input: &edition.EditionInput{
				BookID: 123,
				Title:  "Test Edition",
				ASIN:   "B123456789",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// First, check GetEditionByASIN should return nil for initial check
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, models.ErrEditionNotFound).Once()

				// Return errors in the GraphQL response suggesting duplication
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
					mock.MatchedBy(func(variables map[string]interface{}) bool {
						// Ensure the ASIN is correctly passed to the edition input
						if edition, ok := variables["edition"].(map[string]interface{}); ok {
							if dto, ok := edition["dto"].(map[string]interface{}); ok {
								// Make sure ASIN is correctly set in the variables
								asin, ok := dto["asin"]
								return ok && asin == "B123456789"
							}
						}
						return false
					}),
					mock.Anything,
				).Run(func(args mock.Arguments) {
					// Set errors in the response
					decodeMockGraphQLResponse(t, args, `{"insert_edition":{"id":null,"errors":["Edition with this ASIN already exists"]}}`)
				}).Return(nil)

				// Second lookup for GetEditionByASIN after duplicate error returns existing edition
				existingEdition := &models.Edition{
					ID:     "777",
					BookID: "123",
					Title:  "Existing Edition by ASIN",
				}
				// The critical fix: Set up the right expectation for the second call after error
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(existingEdition, nil).Once()
			},
			expectedID:  777, // Should return the existing edition's ID found by ASIN
			expectError: false,
		},
		{
			name: "all_optional_fields",
			input: &edition.EditionInput{
				BookID:      123,
				Title:       "Test Edition",
				Subtitle:    "A Test",
				ASIN:        "B123456789",
				ISBN13:      "9781234567890",
				ISBN10:      "1234567890",
				AuthorIDs:   []int{1, 2},
				NarratorIDs: []int{3, 4},
				PublisherID: 5,
				LanguageID:  6,
				CountryID:   7,
				AudioLength: 3600, // 1 hour
				ReleaseDate: "2023-01-01",
				EditionInfo: "First Edition",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// First, check GetEditionByASIN should return nil since no duplicate exists
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, models.ErrEditionNotFound)

				// Verify all optional fields are included in the GraphQL mutation
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
					mock.MatchedBy(func(variables map[string]interface{}) bool {
						// Verify mandatory fields
						id, ok := variables["bookId"].(int)
						if !ok || id != 123 {
							return false
						}

						edition, ok := variables["edition"].(map[string]interface{})
						if !ok {
							return false
						}

						dto, ok := edition["dto"].(map[string]interface{})
						if !ok {
							return false
						}

						// Verify all optional fields
						fields := map[string]interface{}{
							"title":               "Test Edition",
							"subtitle":            "A Test",
							"asin":                "B123456789",
							"isbn_13":             "9781234567890",
							"isbn_10":             "1234567890",
							"publisher_id":        5,
							"language_id":         6,
							"country_id":          7,
							"audio_seconds":       3600,
							"release_date":        "2023-01-01",
							"edition_information": "First Edition",
							"image_id":            456,
						}

						// Check all fields are present in the DTO
						for key, expectedValue := range fields {
							actualValue, ok := dto[key]
							if !ok || actualValue != expectedValue {
								return false
							}
						}

						// Check contributions for authors and narrators
						contributions, ok := dto["contributions"].([]map[string]interface{})
						if !ok || len(contributions) != 4 { // 2 authors + 2 narrators
							return false
						}

						return true
					}),
					mock.Anything,
				).Run(func(args mock.Arguments) {
					// Set the ID in the response
					decodeMockGraphQLResponse(t, args, `{"insert_edition":{"id":789,"errors":[]}}`)
				}).Return(nil)
			},
			expectedID:  789,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new mock client
			mockClient := new(MockHardcoverClient)

			// Create a creator instance with mocks
			creator := newTestCreator(t, mockClient)

			// Create test helper
			helper := edition.NewTestHelpers(creator)

			// Setup mocks
			if tt.setupMock != nil {
				tt.setupMock(t, mockClient)
			}
			// ISBN lookups that a case does not script find nothing.
			mockClient.On("GetEditionByISBN13", mock.Anything, mock.Anything).Return(nil, models.ErrEditionNotFound).Maybe()
			mockClient.On("GetEditionByISBN10", mock.Anything, mock.Anything).Return(nil, models.ErrEditionNotFound).Maybe()

			// Call the method under test via the helper
			editionID, err := helper.CreateEdition(context.Background(), tt.input, tt.imageID)

			// Verify results
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedID, editionID)
			}

			// Verify all mocks were called as expected
			mockClient.AssertExpectations(t)
		})
	}
}

// TestEditionCreator_createEditionFormat checks the edition_format sent to
// Hardcover: a caller-supplied label is honored (trimmed) and a missing one
// falls back to "Audiobook". The reading format stays Audiobook either way.
func TestEditionCreator_createEditionFormat(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{"provided format is sent", "Audible Audio", "Audible Audio"},
		{"surrounding whitespace is trimmed", "  Audible Audio\t", "Audible Audio"},
		{"empty falls back to Audiobook", "", "Audiobook"},
		{"whitespace only falls back to Audiobook", "  \t ", "Audiobook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent map[string]interface{}
			mockClient := new(MockHardcoverClient)
			mockClient.On("GraphQLMutation",
				mock.Anything,
				mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
				mock.AnythingOfType("map[string]interface {}"),
				mock.Anything,
			).Run(func(args mock.Arguments) {
				variables := args.Get(2).(map[string]interface{})
				sent = variables["edition"].(map[string]interface{})["dto"].(map[string]interface{})
				decodeMockGraphQLResponse(t, args, `{"insert_edition":{"id":789,"errors":[]}}`)
			}).Return(nil).Once()

			creator := newTestCreator(t, mockClient)
			input := &edition.EditionInput{BookID: 123, Title: "T", AuthorIDs: []int{1}, EditionFormat: tt.format}
			id, err := edition.NewTestHelpers(creator).CreateEdition(context.Background(), input, 0)

			assert.NoError(t, err)
			assert.Equal(t, 789, id)
			assert.Equal(t, tt.want, sent["edition_format"])
			assert.Equal(t, 2, sent["reading_format_id"])
			mockClient.AssertExpectations(t)
		})
	}
}

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()
	os.Exit(code)
}
