package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphQLQuery_BookByASIN tests the GraphQL query functionality with a real API call
// This is an integration test that requires HARDCOVER_TOKEN environment variable to be set
func TestGraphQLQuery_BookByASIN(t *testing.T) {
	token := os.Getenv("HARDCOVER_TOKEN")
	if token == "" {
		t.Skip("HARDCOVER_TOKEN environment variable not set, skipping integration test")
	}

	// Initialize the logger
	logger.Setup(logger.Config{
		Level:      "debug",
		TimeFormat: "2006-01-02T15:04:05Z07:00",
	})

	// Get the logger
	log := logger.Get()

	// Create a new client with the token from environment
	client := NewClient(token, log)

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
		ASIN           *string `json:"asin"`
		ISBN13         *string `json:"isbn_13"`
		ISBN10         *string `json:"isbn_10"`
		ReadingFormatID int     `json:"reading_format_id"`
		AudioSeconds    *int    `json:"audio_seconds"`
	}

	type Book struct {
		ID            int            `json:"id"`
		Title         string         `json:"title"`
		BookStatusID  int            `json:"book_status_id"`
		CanonicalID   int            `json:"canonical_id"`
		Editions      []BookEdition  `json:"editions"`
	}

	type ResponseData struct {
		Books []Book `json:"books"`
	}

	var response struct {
		Data ResponseData `json:"data"`
	}

	// Execute the query
	err := client.GraphQLQuery(context.Background(), query, variables, &response)
	require.NoError(t, err, "GraphQL query should not return an error")

	// Log the response for debugging
	jsonData, _ := json.MarshalIndent(response, "", "  ")
	fmt.Printf("Response: %s\n", string(jsonData))

	// Assert the response
	assert.NotEmpty(t, response.Data.Books, "Expected at least one book in the response")
	book := response.Data.Books[0]
	assert.NotEmpty(t, book.Title, "Book title should not be empty")
	assert.NotEmpty(t, book.Editions, "Book should have at least one edition")
	
	edition := book.Editions[0]
	assert.Equal(t, 2, edition.ReadingFormatID, "Reading format ID should be 2 (audiobook)")
	assert.Equal(t, "B00I8OW9R2", *edition.ASIN, "ASIN should match the query parameter")
}
