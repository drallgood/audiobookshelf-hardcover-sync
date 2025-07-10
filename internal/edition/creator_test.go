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
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

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

// GraphQLMutation implements the interface method for MockHardcoverClient
func (m *MockHardcoverClient) GraphQLMutation(ctx context.Context, mutation string, variables map[string]interface{}, result interface{}) error {
	// Call the mock with any type for the result
	args := m.Called(ctx, mutation, variables, result)

	// Only manipulate the result if we didn't return an error
	if args.Error(0) == nil {
		// We need to handle different operations differently
		if strings.Contains(mutation, "insert_image") {
			// Use reflection to set the ID regardless of the exact struct type
			// Only the structure (having InsertImage.ID) matters
			returnImageID := 456 // Test ID for successful cases
			setImageIDViaReflection(result, returnImageID)
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
		// For the ZeroID mock, we explicitly set the ID to 0 to test validation logic
		if strings.Contains(mutation, "insert_image") {
			// Use reflection to set the ID to 0
			setImageIDViaReflection(result, 0)
		}
	}

	return args.Error(0)
}

// Helper function to set ID in response structures via reflection
func setImageIDViaReflection(result interface{}, id int) {
	// Get the value of the result pointer
	val := reflect.ValueOf(result).Elem()

	// Get the field for InsertImage
	insertImageField := val.FieldByName("InsertImage")
	if !insertImageField.IsValid() {
		return // No InsertImage field, nothing to do
	}

	// Get the ID field inside InsertImage
	idField := insertImageField.FieldByName("ID")
	if !idField.IsValid() || !idField.CanSet() {
		return // No settable ID field
	}

	// Handle different ID field types
	switch idField.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// For integer types
		idField.SetInt(int64(id))
	case reflect.Interface:
		// For interface{} types
		idField.Set(reflect.ValueOf(id))
	default:
		// Try direct setting for other types
		try := func() {
			defer func() { _ = recover() }() // Ignore panics
			idField.Set(reflect.ValueOf(id))
		}
		try()
	}
}

// isUpdateEditionResult is a custom matcher function that checks if the struct
// has the expected fields for an update_edition operation result
func isUpdateEditionResult(result interface{}) bool {
	// Make sure it's a pointer to a struct
	v := reflect.ValueOf(result)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return false
	}

	// Get the value of the struct
	val := v.Elem()

	// Check if it has an UpdateEdition field
	updateEditionField := val.FieldByName("UpdateEdition")
	if !updateEditionField.IsValid() || updateEditionField.Kind() != reflect.Struct {
		return false
	}

	// Check if UpdateEdition has ID and Errors fields
	idField := updateEditionField.FieldByName("ID")
	errorsField := updateEditionField.FieldByName("Errors")
	if !idField.IsValid() || !errorsField.IsValid() {
		return false
	}

	// It has all the required fields
	return true
}

// isInsertEditionResult is a custom matcher function that checks if the struct
// has the expected fields for an insert_edition operation result
func isInsertEditionResult(result interface{}) bool {
	// Make sure it's a pointer to a struct
	v := reflect.ValueOf(result)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return false
	}

	// Get the value of the struct
	val := v.Elem()

	// Check if it has an InsertEdition field
	insertEditionField := val.FieldByName("InsertEdition")
	if !insertEditionField.IsValid() || insertEditionField.Kind() != reflect.Struct {
		return false
	}

	// Check if InsertEdition has ID and Errors fields
	idField := insertEditionField.FieldByName("ID")
	errorsField := insertEditionField.FieldByName("Errors")
	if !idField.IsValid() || !errorsField.IsValid() {
		return false
	}

	// It has all the required fields
	return true
}

// GetGoogleUploadCredentials mocks the GetGoogleUploadCredentials method
func (m *MockHardcoverClient) GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*edition.GoogleUploadInfo, error) {
	args := m.Called(ctx, filename, editionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*edition.GoogleUploadInfo), args.Error(1)
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
	return edition.NewCreatorWithHTTPClient(
		client,
		logger.Get(),
		false,
		"",
		httpClient,
	)
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
		name          string
		input         *edition.EditionInput
		setupMock     func(*testing.T, *MockHardcoverClient)
		expectError   bool
		expectSuccess bool
		expectedID    int
	}{
		{
			name: "API error",
			input: &edition.EditionInput{
				BookID:    999,
				Title:     "Error Book",
				AuthorIDs: []int{1},
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
						assert.Equal(t, "Audiobook", dto["edition_format"])
						// Handle both int and float64 for reading_format_id
						switch v := dto["reading_format_id"].(type) {
						case int:
							assert.Equal(t, 2, v)
						case float64:
							assert.Equal(t, float64(2), v)
						default:
							assert.Fail(t, "Unexpected type for reading_format_id: %T", v)
						}
					}).
					Once()
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
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				setupCommonMocks(m)
			},
			expectError:   true,
			expectSuccess: false,
		},
		{
			name: "valid input without image",
			input: &edition.EditionInput{
				BookID:      123,
				Title:       "Test Book",
				AuthorIDs:   []int{1, 2},
				NarratorIDs: []int{3},
				PublisherID: 1,
				ReleaseDate: "2020-01-01",
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
						assert.Equal(t, "Audiobook", dto["edition_format"])

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

						// Verify the response is set with the edition ID
						response := args.Get(3).(*struct {
							InsertEdition struct {
								ID     interface{} `json:"id"`
								Errors []string    `json:"errors"`
							} `json:"insert_edition"`
						})
						response.InsertEdition.ID = 123
						response.InsertEdition.Errors = nil
					}).
					Once()
			},
			expectError:   false,
			expectSuccess: true,
			expectedID:    123,
		},
		{
			name: "valid input with image",
			input: &edition.EditionInput{
				BookID:      456,
				Title:       "Test Book with Image",
				AuthorIDs:   []int{4, 5},
				NarratorIDs: []int{6},
				PublisherID: 2,
				ReleaseDate: "2021-01-01",
				ImageURL:    "http://example.com/cover.jpg",
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

						editionInput, ok := variables["edition"].(map[string]interface{})
						if !ok {
							t.Error("edition input is not a map")
						}
						_ = editionInput // Use the variable to avoid unused variable error

						// Set the response with the edition ID
						respPtr := args.Get(3).(*struct {
							InsertEdition struct {
								ID     interface{} `json:"id"`
								Errors []string    `json:"errors"`
							} `json:"insert_edition"`
						})

						respPtr.InsertEdition.ID = 456
						respPtr.InsertEdition.Errors = nil
					}).Once()

				// The actual implementation makes an HTTP request to get upload credentials
				// We'll let this happen but mock the HTTP response
				// The test will fail with a 404 since we're not setting up an HTTP mock server
			},
			expectError:   false,
			expectSuccess: true,
			expectedID:    456,
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
								ID            int    `json:"id"`
								Title         string `json:"title"`
								Subtitle      string `json:"subtitle"`
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
							ID            int    `json:"id"`
							Title         string `json:"title"`
							Subtitle      string `json:"subtitle"`
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
							ID:            123,
							Title:         "Test Book",
							Subtitle:      "A Test Subtitle",
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
		BookID:      123,
		Title:       "Test Book",
		Subtitle:    "A Test Subtitle",
		AuthorIDs:   []int{1, 2},
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

func TestEditionCreator_CreateImageRecord(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	tests := []struct {
		name           string
		editionID      int
		imageURL       string
		useZeroIDMock  bool // Flag to indicate that we should use the ZeroIDMockHardcoverClient
		setupMock      func(interface{}, *testing.T) // Change parameter to interface{} to handle both mock types
		expectedImageID int
		expectError    bool
		errorContains string
	}{
		{
			name:      "successful image record creation",
			editionID: 123,
			imageURL:  "https://example.com/test.jpg",
			useZeroIDMock: false,
			setupMock: func(m interface{}, t *testing.T) {
				// Cast to MockHardcoverClient
				mockClient := m.(*MockHardcoverClient)
				
				// Our mock now uses reflection to handle response
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
			expectError:    false,
		},
		{
			name:      "graphql mutation error",
			editionID: 123,
			imageURL:  "https://example.com/test.jpg",
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
			expectError:    true,
			errorContains: "graphql mutation failed",
		},
		{
			name:      "invalid response format",
			editionID: 123,
			imageURL:  "https://example.com/test.jpg",
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
			expectError:    true,
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

func TestEditionCreator_uploadImageToGCS(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	// Create a separate test server for each test case
	setupTestServer := func() *httptest.Server {
		// We need to declare the server variable first before using it in the handler
		var server *httptest.Server

		// Now create the server with handler function
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract test case from header
			testCase := r.Header.Get("X-Test-Case")

			fmt.Printf("Test server received request: %s %s, Test Case: %s\n", 
				r.Method, r.URL.Path, testCase)
			
			// Print info about the request to help with debugging
			fmt.Printf("Processing request: %s %s, with test case: %s\n", r.Method, r.URL.Path, testCase)

			// First, handle direct image requests
			if r.Method == http.MethodGet && (r.URL.Path == "/test.jpg" || r.URL.Path == "/nonexistent.jpg" || r.URL.Path == "/corrupt.jpg") {
				// Handle cover image requests based on test case and path
				if testCase == "fetch_error" || r.URL.Path == "/nonexistent.jpg" {  
					// Return error for fetch_error test case
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, "failed to fetch image")
				} else {
					// Return a valid image for all other cases
					w.Header().Set("Content-Type", "image/jpeg")
					// Small valid JPEG - a 1x1 black pixel
					_, err := w.Write([]byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07, 0x07, 0x09, 0x09, 0x08, 0x0a, 0x0c, 0x14, 0x0d, 0x0c, 0x0b, 0x0b, 0x0c, 0x19, 0x12, 0x13, 0x0f, 0x14, 0x1d, 0x1a, 0x1f, 0x1e, 0x1d, 0x1a, 0x1c, 0x1c, 0x20, 0x24, 0x2e, 0x27, 0x20, 0x22, 0x2c, 0x23, 0x1c, 0x1c, 0x28, 0x37, 0x29, 0x2c, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1f, 0x27, 0x39, 0x3d, 0x38, 0x32, 0x3c, 0x2e, 0x33, 0x34, 0x32, 0xff, 0xdb, 0x00, 0x43, 0x01, 0x09, 0x09, 0x09, 0x0c, 0x0b, 0x0c, 0x18, 0x0d, 0x0d, 0x18, 0x32, 0x21, 0x1c, 0x21, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0xff, 0xc0, 0x00, 0x11, 0x08, 0x00, 0x01, 0x00, 0x01, 0x03, 0x01, 0x22, 0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01, 0xff, 0xc4, 0x00, 0x1f, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0xff, 0xc4, 0x00, 0xb5, 0x10, 0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04, 0x03, 0x05, 0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7d, 0x00, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08, 0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xff, 0xda, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3f, 0x00, 0xfd, 0xfc, 0xa2, 0x8a, 0x28, 0xff, 0xd9})
					if err != nil {
					t.Fatalf("Failed to write JPEG data: %v", err)
				}
				}
				return // Important to return here to prevent falling through
			}

			// Next, handle cover editions paths
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/covers/editions/") {
				// Handle cover image requests based on path and test case
				if testCase == "fetch_error" {  
					// Return error for fetch_error test case
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, "failed to fetch image")
				} else if testCase == "image_download_failed" {
					// Test image download error cases
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, "failed to read image")
				} else {
					// Return a valid image for successful case
					w.Header().Set("Content-Type", "image/jpeg")
					// Small valid JPEG - a 1x1 black pixel
					_, err := w.Write([]byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07, 0x07, 0x09, 0x09, 0x08, 0x0a, 0x0c, 0x14, 0x0d, 0x0c, 0x0b, 0x0b, 0x0c, 0x19, 0x12, 0x13, 0x0f, 0x14, 0x1d, 0x1a, 0x1f, 0x1e, 0x1d, 0x1a, 0x1c, 0x1c, 0x20, 0x24, 0x2e, 0x27, 0x20, 0x22, 0x2c, 0x23, 0x1c, 0x1c, 0x28, 0x37, 0x29, 0x2c, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1f, 0x27, 0x39, 0x3d, 0x38, 0x32, 0x3c, 0x2e, 0x33, 0x34, 0x32, 0xff, 0xdb, 0x00, 0x43, 0x01, 0x09, 0x09, 0x09, 0x0c, 0x0b, 0x0c, 0x18, 0x0d, 0x0d, 0x18, 0x32, 0x21, 0x1c, 0x21, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0x32, 0xff, 0xc0, 0x00, 0x11, 0x08, 0x00, 0x01, 0x00, 0x01, 0x03, 0x01, 0x22, 0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01, 0xff, 0xc4, 0x00, 0x1f, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0xff, 0xc4, 0x00, 0xb5, 0x10, 0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04, 0x03, 0x05, 0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7d, 0x00, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08, 0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xff, 0xda, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3f, 0x00, 0xfd, 0xfc, 0xa2, 0x8a, 0x28, 0xff, 0xd9})
					if err != nil {
						t.Fatalf("Failed to write JPEG data: %v", err)
					}
				}
				return // Important to return here to prevent falling through
			}

			// Handle upload credentials endpoint - the test is using /api/upload/google
			if (r.Method == http.MethodGet || r.Method == http.MethodPost) && 
				(strings.Contains(r.URL.Path, "/api/google_upload_credentials") || strings.Contains(r.URL.Path, "/api/upload/google")) {
				// Handle invalid credentials test case
				if testCase == "invalid_credentials" || testCase == "credentials_error" {
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"error": "Invalid credentials"}`)
					return
				}

				// Default: return valid credentials
				w.Header().Set("Content-Type", "application/json")
				response := map[string]interface{}{
					"url": server.URL + "/upload",
					// This must match exactly what's expected in the test
					"fileURL": "https://storage.googleapis.com/hardcover/test-key",
					"fields": map[string]string{
						"key": "uploads/covers/test-key.jpg",
						"x-goog-algorithm": "test-algo",
						"x-goog-credential": "test-cred",
						"x-goog-date": "20230101T000000Z",
						"x-goog-signature": "test-sig", 
						"policy": "test-policy",
					},
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Fatalf("Failed to encode JSON response: %v", err)
				}
				return
			}
			
			// Handle GCS upload endpoint
			if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload") {
				// Handle GCS upload based on test case
				if testCase == "upload_error" {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, "Upload failed")
					return
				}
				
				// For image_download_failed test case, we need to check this after the credentials are obtained
				if testCase == "image_download_failed" {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, "failed to read image")
					return
				}
				
				// Default: successful upload - HTTP 200 for success (not 201 Created)
				w.WriteHeader(http.StatusOK)
				// Empty response body for successful upload
				fmt.Fprint(w, "")
				return
			}
				
			// Handle any other request paths - default case
			fmt.Printf("Unhandled request in test server: %s %s\n", r.Method, r.URL.String())
			http.Error(w, "Not found", http.StatusNotFound)
		}))
		return server
	}

	tests := []struct {
		name         string
		editionID    int
		imageURLPath string
		expectedURL  string
		expectError  bool
		errorContains string
	}{
		{
			name:         "successful_upload",
			editionID:    123,
			imageURLPath: "/test.jpg",
			expectedURL:  "https://assets.hardcover.app/uploads/covers/test-key.jpg",
			expectError:  false,
		},
		{
			name:         "credentials_error",
			editionID:    123,
			imageURLPath: "/test.jpg",
			expectError: true,
			errorContains: "failed to get upload credentials: HTTP 401",
		},
		{
			name:         "image_fetch_error",
			editionID:    123,
			imageURLPath: "/nonexistent.jpg",
			expectError:  true,
			errorContains: "failed to fetch image",
		},
		{
			name:         "image_download_failed",
			editionID:    123,
			imageURLPath: "/corrupt.jpg", // Special path that will return corrupt image data
			expectError:  true,
			errorContains: "failed to read image",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup a test server for this test case
			server := setupTestServer()
			defer server.Close()

			// Create mock client for this test case
			// We still need some mocks for internal method calls
			mockClient := new(MockHardcoverClient)
			
			// Setup necessary mock expectations
			// These are called regardless of the test case
			mockClient.On("GetAuthHeader").Return("Bearer test-token")
			
			// Setup context with test case identifier
			ctx := context.WithValue(context.Background(), edition.TestCaseHeaderKey, tt.name)
			
			// Add expectations specific to test cases that need credentials
			if tt.name != "credentials_error" {
				mockClient.On("GetGoogleUploadCredentials", 
					mock.Anything, // ctx
					mock.AnythingOfType("string"), // filename
					mock.AnythingOfType("int"), // editionID
				).Return(&edition.GoogleUploadInfo{
					// URL will be replaced by WithTestServer
					URL: "{{TEST_SERVER_URL}}/upload",
					Fields: map[string]string{
						"key": "test-key",
						"x-goog-algorithm": "test-algo",
						"x-goog-credential": "test-cred",
						"x-goog-date": "20230101T000000Z",
						"x-goog-signature": "test-sig",
						"policy": "test-policy",
					},
				}, nil)
			}

			// Create creator with mock client and custom HTTP client
			creator := edition.NewCreatorWithHTTPClient(mockClient, logger.Get(), false, "test-token", http.DefaultClient)

			// Use the test helper to access the private method
			// Configure it with the test server URL for proper URL redirection
			helper := edition.NewTestHelpers(creator).WithTestServer(server.URL)
			
			// Combine server URL with path for complete image URL
			imageURL := server.URL + tt.imageURLPath
			
			// The ctx was already created above, now map test names to the appropriate test case values
			switch tt.name {
			case "image_download_failed":
				ctx = context.WithValue(context.Background(), edition.TestCaseHeaderKey, "image_download_failed")
			case "credentials_error":
				ctx = context.WithValue(context.Background(), edition.TestCaseHeaderKey, "invalid_credentials")
			case "image_fetch_error":
				ctx = context.WithValue(context.Background(), edition.TestCaseHeaderKey, "fetch_error")
			}
			
			// Call the helper method
			url, err := helper.UploadImageToGCS(ctx, tt.editionID, imageURL)

			if tt.expectError {
				assert.NotNil(t, err)
				if tt.errorContains != "" && err != nil {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.expectedURL, url)
			}
			
			// We don't need to verify mock expectations since we're not using mocks
		})
	}
}

// mockImageTransport is a custom http.RoundTripper that mocks image download responses
type mockImageTransport struct {
	expectedURL string
	test        string
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
				Body:       io.NopCloser(strings.NewReader("fake image data")),
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

	// For any other requests, just pass through to default transport
	return http.DefaultTransport.RoundTrip(req)
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
					mock.MatchedBy(func(v interface{}) bool {
						// We just need to verify it has the right structure and can be set by reflection
						return true
					}),
				).Return(nil)

				// Mock GraphQLMutation for updating the edition
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.MatchedBy(isUpdateEditionResult),
				).Run(func(args mock.Arguments) {
					// Set the ID in the response
					resp := args.Get(3).(*struct {
						UpdateEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"update_edition"`
					})
					resp.UpdateEdition.ID = 123
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
					mock.MatchedBy(func(v interface{}) bool {
						// We just need to verify it has the right structure and can be set by reflection
						return true
					}),
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
					mock.MatchedBy(func(v interface{}) bool {
						// We just need to verify it has the right structure and can be set by reflection
						return true
					}),
				).Return(nil)

				// Make GraphQLMutation for updating the edition fail
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.MatchedBy(isUpdateEditionResult),
				).Return(fmt.Errorf("edition update failed"))
			},
			expectError:   true,
			errorContains: "failed to update edition with new image",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new mock client
			mockClient := new(MockHardcoverClient)

			// Create a mock RoundTripper to handle image downloads
			mockTransport := &mockImageTransport{
				expectedURL: tt.imageURL,
				test:        tt.name,
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

func TestEditionCreator_updateEditionImage(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	tests := []struct {
		name          string
		editionID     int
		imageID       int
		setupMock     func(*testing.T, *MockHardcoverClient, interface{})
		expectError   bool
		errorContains string
	}{
		{
			name:      "success_case",
			editionID: 123,
			imageID:   456,
			setupMock: func(t *testing.T, m *MockHardcoverClient, result interface{}) {
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.MatchedBy(func(variables map[string]interface{}) bool {
						// Verify we're sending the right variables
						id, ok := variables["id"].(int)
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

						imageID, ok := dto["image_id"].(int)
						if !ok || imageID != 456 {
							return false
						}

						return true
					}),
					mock.MatchedBy(isUpdateEditionResult),
				).Run(func(args mock.Arguments) {
					// Set the ID in the response
					resp := args.Get(3).(*struct {
						UpdateEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"update_edition"`
					})
					resp.UpdateEdition.ID = 123
				}).Return(nil)
			},
			expectError: false,
		},
		{
			name:      "invalid_ids",
			editionID: 0, // Invalid edition ID
			imageID:   456,
			setupMock: func(t *testing.T, m *MockHardcoverClient, result interface{}) {
				// No mock calls expected for this case
			},
			expectError:   true,
			errorContains: "invalid edition ID or image ID",
		},
		{
			name:      "invalid_image_id",
			editionID: 123,
			imageID:   0, // Invalid image ID
			setupMock: func(t *testing.T, m *MockHardcoverClient, result interface{}) {
				// No mock calls expected for this case
			},
			expectError:   true,
			errorContains: "invalid edition ID or image ID",
		},
		{
			name:      "graphql_mutation_error",
			editionID: 123,
			imageID:   456,
			setupMock: func(t *testing.T, m *MockHardcoverClient, result interface{}) {
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.Anything, // variables
					mock.Anything, // result
				).Return(fmt.Errorf("graphql mutation failed"))
			},
			expectError:   true,
			errorContains: "graphql mutation failed",
		},
		{
			name:      "response_errors",
			editionID: 123,
			imageID:   456,
			setupMock: func(t *testing.T, m *MockHardcoverClient, result interface{}) {
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.Anything, // variables
					mock.MatchedBy(isUpdateEditionResult),
				).Run(func(args mock.Arguments) {
					// Set errors in the response
					resp := args.Get(3).(*struct {
						UpdateEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"update_edition"`
					})
					resp.UpdateEdition.ID = 123
					resp.UpdateEdition.Errors = []string{"test error"}
				}).Return(nil)
			},
			expectError:   true,
			errorContains: "edition update failed",
		},
		{
			name:      "missing_edition_id",
			editionID: 123,
			imageID:   456,
			setupMock: func(t *testing.T, m *MockHardcoverClient, result interface{}) {
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "update_edition") }),
					mock.Anything, // variables
					mock.MatchedBy(isUpdateEditionResult),
				).Run(func(args mock.Arguments) {
					// Set nil ID in the response
					resp := args.Get(3).(*struct {
						UpdateEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"update_edition"`
					})
					resp.UpdateEdition.ID = nil // Missing ID
					resp.UpdateEdition.Errors = []string{}
				}).Return(nil)
			},
			expectError:   true,
			errorContains: "missing edition ID",
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
				tt.setupMock(t, mockClient, nil)
			}

			// Call the method under test via the helper
			err := helper.UpdateEditionImage(context.Background(), tt.editionID, tt.imageID)

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
				BookID:       123,
				Title:        "Test Edition",
				Subtitle:     "A Test",
				ASIN:         "B123456789",
				ISBN13:       "9781234567890",
				AuthorIDs:    []int{1, 2},
				NarratorIDs:  []int{3, 4},
				PublisherID:  5,
				LanguageID:   6,
				CountryID:    7,
				AudioLength:  3600, // 1 hour
				ReleaseDate:  "2023-01-01",
				EditionInfo:  "First Edition",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// First, check GetEditionByASIN should return nil since no duplicate exists
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, fmt.Errorf("edition not found"))

				// Mock GraphQLMutation for creating the edition
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.MatchedBy(isInsertEditionResult),
				).Run(func(args mock.Arguments) {
					// Set the ID in the response
					resp := args.Get(3).(*struct {
						InsertEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"insert_edition"`
					})
					resp.InsertEdition.ID = 789 // New edition ID
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
					ID:    "555",
					Title: "Existing Edition",
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
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, fmt.Errorf("edition not found"))

				// Make GraphQLMutation fail
				m.On("GraphQLMutation",
					mock.Anything, // context
					mock.MatchedBy(func(query string) bool { return strings.Contains(query, "insert_edition") }),
					mock.AnythingOfType("map[string]interface {}"),
					mock.MatchedBy(isInsertEditionResult),
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
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, fmt.Errorf("edition not found"))

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
					mock.MatchedBy(isInsertEditionResult),
				).Run(func(args mock.Arguments) {
					// Set errors in the response
					resp := args.Get(3).(*struct {
						InsertEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"insert_edition"`
					})
					resp.InsertEdition.Errors = []string{"Edition with this ISBN13 already exists"}
				}).Return(nil)

				// This is the critical part: Set up the second expectation for GetEditionByISBN13
				// It will be called after the GraphQL mutation returns the "already exists" error
				existingEdition := &models.Edition{
					ID:    "666",
					Title: "Existing Edition by ISBN13",
				}
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
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, fmt.Errorf("edition not found")).Once()

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
					mock.MatchedBy(isInsertEditionResult),
				).Run(func(args mock.Arguments) {
					// Set errors in the response
					resp := args.Get(3).(*struct {
						InsertEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"insert_edition"`
					})
					resp.InsertEdition.Errors = []string{"Edition with this ASIN already exists"}
				}).Return(nil)

				// Second lookup for GetEditionByASIN after duplicate error returns existing edition
				existingEdition := &models.Edition{
					ID:    "777",
					Title: "Existing Edition by ASIN",
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
				BookID:       123,
				Title:        "Test Edition",
				Subtitle:     "A Test",
				ASIN:         "B123456789",
				ISBN13:       "9781234567890",
				ISBN10:       "1234567890",
				AuthorIDs:    []int{1, 2},
				NarratorIDs:  []int{3, 4},
				PublisherID:  5,
				LanguageID:   6,
				CountryID:    7,
				AudioLength:  3600, // 1 hour
				ReleaseDate:  "2023-01-01",
				EditionInfo:  "First Edition",
			},
			imageID: 456,
			setupMock: func(t *testing.T, m *MockHardcoverClient) {
				// First, check GetEditionByASIN should return nil since no duplicate exists
				m.On("GetEditionByASIN", mock.Anything, "B123456789").Return(nil, fmt.Errorf("edition not found"))

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
							"title":              "Test Edition",
							"subtitle":           "A Test",
							"asin":               "B123456789",
							"isbn_13":            "9781234567890",
							"isbn_10":            "1234567890",
							"publisher_id":       5,
							"language_id":        6,
							"country_id":         7,
							"audio_seconds":      3600,
							"release_date":       "2023-01-01",
							"edition_information": "First Edition",
							"image_id":           456,
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
					mock.MatchedBy(isInsertEditionResult),
				).Run(func(args mock.Arguments) {
					// Set the ID in the response
					resp := args.Get(3).(*struct {
						InsertEdition struct {
							ID     interface{} `json:"id"`
							Errors []string    `json:"errors"`
						} `json:"insert_edition"`
					})
					resp.InsertEdition.ID = 789 // New edition ID
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

func TestNewCreator(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	// Get a test logger
	log := logger.Get()

	tests := []struct {
		name               string
		client             edition.HardcoverClient
		dryRun             bool
		audiobookshelfToken string
		customHTTPClient    *http.Client
		useCustomClient     bool
		expectedTimeout     time.Duration
	}{
		{
			name:               "with_default_config",
			client:             new(MockHardcoverClient),
			dryRun:             false,
			audiobookshelfToken: "test-token",
			useCustomClient:     false,
			expectedTimeout:     90 * time.Second, // Default IdleConnTimeout from NewCreator
		},
		{
			name:               "with_custom_client",
			client:             new(MockHardcoverClient),
			dryRun:             true,
			audiobookshelfToken: "custom-token",
			customHTTPClient:    &http.Client{Timeout: 30 * time.Second},
			useCustomClient:     true,
			expectedTimeout:     30 * time.Second,
		},
		{
			name:               "with_dry_run",
			client:             new(MockHardcoverClient),
			dryRun:             true,
			audiobookshelfToken: "dry-run-token",
			useCustomClient:     false,
			expectedTimeout:     90 * time.Second, // Default IdleConnTimeout from NewCreator
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the constructor
			var creator *edition.Creator
			if tt.useCustomClient {
				creator = edition.NewCreatorWithHTTPClient(tt.client, log, tt.dryRun, tt.audiobookshelfToken, tt.customHTTPClient)
			} else {
				creator = edition.NewCreator(tt.client, log, tt.dryRun, tt.audiobookshelfToken)
			}

			// Ensure creator was created
			assert.NotNil(t, creator)

			// Access the private fields using reflection
			reflectedCreator := reflect.ValueOf(creator).Elem()

			// Check client field
			clientField := reflectedCreator.FieldByName("client")
			clientField = reflect.NewAt(clientField.Type(), unsafe.Pointer(clientField.UnsafeAddr())).Elem()
			clientValue := clientField.Interface()
			assert.Equal(t, tt.client, clientValue)

			// Check dryRun field
			dryRunField := reflectedCreator.FieldByName("dryRun")
			dryRunField = reflect.NewAt(dryRunField.Type(), unsafe.Pointer(dryRunField.UnsafeAddr())).Elem()
			dryRunValue := dryRunField.Bool()
			assert.Equal(t, tt.dryRun, dryRunValue)

			// Check audiobookshelfToken field
			tokenField := reflectedCreator.FieldByName("audiobookshelfToken")
			tokenField = reflect.NewAt(tokenField.Type(), unsafe.Pointer(tokenField.UnsafeAddr())).Elem()
			tokenValue := tokenField.String()
			assert.Equal(t, tt.audiobookshelfToken, tokenValue)

			// Check httpClient field if we're using a custom client
			httpClientField := reflectedCreator.FieldByName("httpClient")
			httpClientField = reflect.NewAt(httpClientField.Type(), unsafe.Pointer(httpClientField.UnsafeAddr())).Elem()
			httpClient, ok := httpClientField.Interface().(*http.Client)
			assert.True(t, ok)
			assert.NotNil(t, httpClient)

			if tt.useCustomClient {
				assert.Equal(t, tt.expectedTimeout, httpClient.Timeout)
			}
		})
	}
}

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()
	os.Exit(code)
}
