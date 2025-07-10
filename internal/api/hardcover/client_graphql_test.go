package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphQLQuery_BookByASIN tests the GraphQL query functionality with a mock API server
// This is a unit test that doesn't require a real token
func TestGraphQLQuery_BookByASIN(t *testing.T) {
	// Initialize the logger
	logger.Setup(logger.Config{
		Level:      "debug",
		TimeFormat: "2006-01-02T15:04:05Z07:00",
	})

	// Get the logger
	log := logger.Get()

	// Create a mock server that returns a predefined response for ASIN queries
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body once
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err, "Error reading request body")
		
		// Create a new reader with the body content for parsing
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		
		// Check if it's a GetCurrentUserID query first
		if HandleGetCurrentUserIDQuery(t, w, r) {
			return
		}
		
		// Reuse the body content for our own parsing
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		
		// Parse the request body to check if it's the ASIN query
		var reqBody struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		
		// Decode the request body
		err = json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err, "Error decoding request body")
		
		// Check if this is our ASIN query
		if _, ok := reqBody.Variables["asin"]; ok {
			// Create a simple JSON response that matches the expected structure
			responseJSON := `{
				"data": {
					"books": [
						{
							"id": 123,
							"title": "Test Audiobook Title",
							"book_status_id": 1,
							"canonical_id": 456,
							"editions": [
								{
									"id": 789,
									"asin": "B00I8OW9R2",
									"isbn_13": null,
									"isbn_10": null,
									"reading_format_id": 2,
									"audio_seconds": 12345
								}
							]
						}
					]
				}
			}`
			
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(responseJSON))
			if err != nil {
				t.Fatalf("Failed to write response: %v", err)
			}
			return
		}
		
		// If we get here, it's an unknown query
		http.Error(w, "Unexpected query", http.StatusBadRequest)
	}))
	defer server.Close()

	// Create a client that uses our mock server
	client := CreateTestClient(server)
	client.logger = log

	// Define the query and variables
	query := `query BookByASIN($asin: String!) {
  books(
    where: { 
      editions: { 
        asin: { _eq: $asin }
        reading_format_id: { _eq: 2 }
      }
    }
    limit: 1
  ) {
    id
    title
    book_status_id
    canonical_id
    editions(
      where: { 
        asin: { _eq: $asin }
        reading_format_id: { _eq: 2 }
      }
    ) {
      id
      asin
      isbn_13
      isbn_10
      reading_format_id
      audio_seconds
    }
  }
}`

	variables := map[string]interface{}{
		"asin": "B00I8OW9R2",
	}

	// Define the response structure
	type BookEdition struct {
		ID              int     `json:"id"`
		ASIN            *string `json:"asin"`
		ISBN13          *string `json:"isbn_13"`
		ISBN10          *string `json:"isbn_10"`
		ReadingFormatID int     `json:"reading_format_id"`
		AudioSeconds    *int    `json:"audio_seconds"`
	}

	type Book struct {
		ID           int           `json:"id"`
		Title        string        `json:"title"`
		BookStatusID int           `json:"book_status_id"`
		CanonicalID  int           `json:"canonical_id"`
		Editions     []BookEdition `json:"editions"`
	}

	// Response structure is defined inline below

	// Define a response structure that exactly matches what the client will use for unmarshaling
	// The client will unmarshal only the "data" field from the response,
	// so our struct must match the structure inside the data field
	var response struct {
		Books []Book `json:"books"`
	}

	// Execute the query
	err := client.GraphQLQuery(context.Background(), query, variables, &response)
	require.NoError(t, err, "GraphQL query should not return an error")
	
	t.Logf("Response received: %+v", response)

	// Assert the response
	require.NotEmpty(t, response.Books, "Expected at least one book in the response")
	book := response.Books[0]
	assert.NotEmpty(t, book.Title, "Book title should not be empty")
	assert.Equal(t, "Test Audiobook Title", book.Title, "Book title should match the mock response")
	assert.NotEmpty(t, book.Editions, "Book should have at least one edition")

	edition := book.Editions[0]
	assert.Equal(t, 2, edition.ReadingFormatID, "Reading format ID should be 2 (audiobook)")
	assert.Equal(t, "B00I8OW9R2", *edition.ASIN, "ASIN should match the query parameter")
	assert.Equal(t, 12345, *edition.AudioSeconds, "Audio seconds should match the mock response")
}
