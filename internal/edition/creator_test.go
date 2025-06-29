package edition_test

import (
	"context"
	"encoding/json"
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

	// Handle the response in the Run function of the mock expectation
	// The actual response handling is done in the test case's Run function

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

func newTestCreator(t *testing.T, mockClient *MockHardcoverClient) *edition.Creator {
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
		mockClient,
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

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()
	os.Exit(code)
}
