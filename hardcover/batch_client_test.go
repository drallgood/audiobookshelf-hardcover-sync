package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/config"
	httpclient "github.com/drallgood/audiobookshelf-hardcover-sync/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchClient_Integration(t *testing.T) {
	// Skip if running short tests
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a test server with detailed logging
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Log the incoming request
		t.Logf("Received request: %s %s", r.Method, r.URL.Path)
		
		switch r.URL.Path {
		case "/graphql":
			handleGraphQLBatch(t, w, r)
		default:
			t.Logf("Path not found: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))

	// Ensure the server is shut down when the test completes
	t.Cleanup(func() {
		ts.Close()
	})

	// Create a test config with required fields
	// The GraphQL client will append /graphql to the base URL
	cfg := &config.Config{
		HTTP: config.HTTPConfig{
			MaxRetries:       3,
			RetryWaitMin:    100 * time.Millisecond,
			RetryWaitMax:    1 * time.Second,
			Timeout:         30 * time.Second,
			MaxIdleConns:    100,
			IdleConnTimeout: 90 * time.Second,
		},
		Hardcover: config.HardcoverConfig{
			URL: ts.URL + "/graphql", // Ensure the URL includes the /graphql path
		},
	}

	// Create HTTP client with test config
	httpClient := httpclient.NewClient(cfg)

	// Create batch client with test configuration
	batchClient, err := NewBatchClient(cfg, httpClient)
	require.NoError(t, err)

	// Set a small batch size and rate limit for testing
	batchClient = batchClient.WithBatchSize(2).WithWorkers(2).WithRateLimit(10)

	t.Run("BatchLookupBooks", func(t *testing.T) {
		t.Parallel()

		// Test data
		queries := []BookQuery{
			{
				Title:  "Test Book 1",
				Author: "Test Author 1",
			},
			{
				Title:  "Test Book 2",
				Author: "Test Author 2",
			},
		}

		// Execute batch lookup
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		books, err := batchClient.BatchLookupBooks(ctx, queries)
		require.NoError(t, err)
		require.Len(t, books, len(queries))

		// Verify results
		for i, book := range books {
			assert.Equal(t, queries[i].Title, book.Title)
			assert.Equal(t, queries[i].Author, book.Author)
		}
	})

	t.Run("BatchUpdateBookStatus", func(t *testing.T) {
		t.Parallel()

		// Test data
		updates := []BookStatusUpdate{
			{
				BookID:   "book-1",
				Status:   "IN_PROGRESS",
				Progress: 50.0,
			},
			{
				BookID:   "book-2",
				Status:   "COMPLETED",
				Progress: 100.0,
			},
		}

		// Execute batch update
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		results, err := batchClient.BatchUpdateBookStatus(ctx, updates)
		require.NoError(t, err)
		require.Len(t, results, len(updates))

		// Verify results
		for i, result := range results {
			assert.Equal(t, updates[i].BookID, result.BookID)
			// The test server returns the same status and progress we sent
			assert.Equal(t, updates[i].Status, result.Status)
			assert.Equal(t, updates[i].Progress, result.Progress)
			assert.NoError(t, result.Error)
		}
	})

	t.Run("BatchSyncBooks", func(t *testing.T) {
		t.Log("Starting BatchSyncBooks test...")
		// Test data
		books := []*Book{
			{
				Title:    "New Book 1",
				Author:   "New Author 1",
				Progress: 0.5,
				Status:   "COMPLETED",
			},
			{
				Title:    "New Book 2",
				Author:   "New Author 2",
				Progress: 1.0,
				Status:   "IN_PROGRESS",
			},
		}

		t.Logf("Calling BatchSyncBooks with %d books", len(books))
		// Call the method
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		results, err := batchClient.BatchSyncBooks(ctx, books)

		t.Log("BatchSyncBooks call completed")
		// Verify results
		if !assert.NoError(t, err, "BatchSyncBooks should not return an error") {
			return
		}

		if !assert.Len(t, results, 2, "Should return results for all books") {
			return
		}

		t.Log("Checking first book result...")
		t.Logf("Result[0]: Status=%s, Progress=%.2f", results[0].Status, results[0].Progress)
		assert.Equal(t, "IN_PROGRESS", results[0].Status, "First book should be IN_PROGRESS")
		assert.InDelta(t, 50.0, results[0].Progress, 0.01, "First book progress should be ~50%")

		t.Log("Checking second book result...")
		t.Logf("Result[1]: Status=%s, Progress=%.2f", results[1].Status, results[1].Progress)
		assert.Equal(t, "COMPLETED", results[1].Status, "Second book should be COMPLETED")
		assert.InDelta(t, 100.0, results[1].Progress, 0.01, "Second book progress should be 100%")

		t.Log("BatchSyncBooks test completed")
	})
}

// handleGraphQLBatch handles batch GraphQL requests
func handleGraphQLBatch(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Logf("Handling GraphQL batch request")
	
	// Read the request body for logging
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		t.Logf("Error reading request body: %v", err)
		http.Error(w, "error reading request body", http.StatusBadRequest)
		return
	}
	
	// Log the raw request body
	t.Logf("Request body: %s", string(bodyBytes))
	
	// Parse the JSON request body
	var requests []map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requests); err != nil {
		t.Logf("Error parsing request body: %v", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	
	t.Logf("Processing %d GraphQL operations", len(requests))
	
	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	// Process each request in the batch
	responses := make([]interface{}, 0, len(requests))
	for i, req := range requests {
		// Extract query and variables
		query, _ := req["query"].(string)
		variables, _ := req["variables"].(map[string]interface{})
		
		// Log operation details
		operationName := "unknown"
		if op, ok := req["operationName"].(string); ok && op != "" {
			operationName = op
		}
		t.Logf("Processing operation %d: %s", i, operationName)
		t.Logf("Query: %s", query)
		t.Logf("Variables: %+v", variables)

		// Process based on query type
		var response interface{}
		switch {
		case strings.Contains(query, "LookupBook"):
			// Mock response for book lookup
			title, _ := variables["title"].(string)
			author, _ := variables["author"].(string)
			response = map[string]interface{}{
				"data": map[string]interface{}{
					"searchBooks": []map[string]interface{}{{
						"id":       "book-1",
						"title":    title,
						"author":   author,
						"status":   "COMPLETED",
						"progress": 100.0,
					}},
				},
			}

		case strings.Contains(query, "UpdateBookStatus"):
			// Mock response for status update
			bookID, _ := variables["bookId"].(string)
			status, _ := variables["status"].(string)
			progress, _ := variables["progress"].(float64)
			
			response = map[string]interface{}{
				"data": map[string]interface{}{
					"updateBookStatus": map[string]interface{}{
						"id":       bookID,
						"status":   status,
						"progress": progress,
					},
				},
			}

		case strings.Contains(query, "SyncBook"):
			// Mock response for book sync
			input, _ := variables["input"].(map[string]interface{})
			title, _ := input["title"].(string)
			progress, _ := input["progress"].(float64)
			
			// Convert progress to percentage (0-100)
			progressPct := progress * 100
			
			// Determine status based on progress
			var status string
			switch {
			case progressPct >= 100:
				status = "COMPLETED"
			case progressPct <= 0:
				status = "NOT_STARTED"
			default:
				status = "IN_PROGRESS"
			}

			response = map[string]interface{}{
				"data": map[string]interface{}{
					"syncBook": map[string]interface{}{
						"id":       fmt.Sprintf("book-%d", i+1),
						"title":    title,
						"status":   status,
						"progress": progressPct,
					},
				},
			}

		default:
			t.Logf("Unexpected query: %s", query)
			response = map[string]interface{}{
				"errors": []map[string]interface{}{{
					"message": fmt.Sprintf("Unexpected query: %s", query),
				}},
			}
		}
		
		responses = append(responses, response)
	}
	
	// Write response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responses)
}

func handleLookupBook(t *testing.T, req map[string]interface{}) map[string]interface{} {
	variables, _ := req["variables"].(map[string]interface{})
	title, _ := variables["title"].(string)
	author, _ := variables["author"].(string)

	return map[string]interface{}{
		"data": map[string]interface{}{
			"book": map[string]interface{}{
				"id":       "book-" + title,
				"title":    title,
				"author":   author,
				"coverUrl": "https://example.com/cover.jpg",
			},
		},
	}
}

func handleUpdateBookStatus(t *testing.T, req map[string]interface{}) map[string]interface{} {
	variables, _ := req["variables"].(map[string]interface{})
	bookID, _ := variables["bookId"].(string)
	status, _ := variables["status"].(string)
	progress, _ := variables["progress"].(float64)

	return map[string]interface{}{
		"data": map[string]interface{}{
			"updateBookStatus": map[string]interface{}{
				"id":       bookID,
				"status":   status,
				"progress": progress,
			},
		},
	}
}

func handleSyncBook(t *testing.T, req map[string]interface{}) map[string]interface{} {
	variables, _ := req["variables"].(map[string]interface{})
	input, _ := variables["input"].(map[string]interface{})
	title, _ := input["title"].(string)
	author, _ := input["author"].(string)
	progress, _ := input["progress"].(float64)

	return map[string]interface{}{
		"data": map[string]interface{}{
			"syncBook": map[string]interface{}{
				"id":       "book-" + title,
				"title":    title,
				"author":   author,
				"status":   mapProgressToStatus(progress),
				"progress": progress,
			},
		},
	}
}

func mapProgressToStatus(progress float64) string {
	switch {
	case progress >= 1.0:
		return "COMPLETED"
	case progress > 0:
		return "IN_PROGRESS"
	default:
		return "TO_READ"
	}
}
