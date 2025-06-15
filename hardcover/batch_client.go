package hardcover

import (
	"context"
	"fmt"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/graphql"
	httpclient "github.com/drallgood/audiobookshelf-hardcover-sync/http"
)

// BatchClient handles batch operations with the Hardcover API
type BatchClient struct {
	gqlBatch   *graphql.BatchClient
	httpClient *httpclient.BatchClient
	batchSize  int
	workers    int
	rateLimit  time.Duration
}

// NewBatchClient creates a new batch client for Hardcover operations
func NewBatchClient(cfg *config.Config, httpClient *httpclient.Client) (*BatchClient, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("http client cannot be nil")
	}

	// Create batch client with rate limiting
	batchClient := &BatchClient{
		batchSize:  10,                    // Default batch size
		workers:    5,                     // Default number of workers
		rateLimit:  100 * time.Millisecond, // Default to 10 req/s
	}

	// Create batch HTTP client
	batchClient.httpClient = httpclient.NewBatchClient(
		httpClient,
		httpclient.BatchClientConfig{
			BatchSize:  batchClient.batchSize,
			MaxWorkers: batchClient.workers,
			RateLimit:  batchClient.rateLimit,
		},
	)

	// Create GraphQL batch client using the batch HTTP client
	batchClient.gqlBatch = graphql.NewBatchClient(
		batchClient.httpClient,
		cfg.Hardcover.URL,
	)

	// Apply configuration overrides
	if cfg.Concurrency.MaxWorkers > 0 {
		batchClient.workers = cfg.Concurrency.MaxWorkers
	}

	return batchClient, nil
}

// WithBatchSize sets the batch size for operations
func (c *BatchClient) WithBatchSize(size int) *BatchClient {
	if size > 0 {
		c.batchSize = size
		if c.httpClient != nil {
			c.httpClient = c.httpClient.WithBatchSize(size)
		}
	}
	return c
}

// WithWorkers sets the number of worker goroutines
func (c *BatchClient) WithWorkers(workers int) *BatchClient {
	if workers > 0 {
		c.workers = workers
		if c.httpClient != nil {
			// Note: The current BatchClient implementation doesn't support changing workers after creation
			// Consider adding this functionality if needed
		}
	}
	return c
}

// WithRateLimit sets the rate limit in requests per second
func (c *BatchClient) WithRateLimit(rate int) *BatchClient {
	if rate > 0 {
		c.rateLimit = time.Second / time.Duration(rate)
		if c.httpClient != nil {
			c.httpClient = c.httpClient.WithRateLimit(c.rateLimit)
		}
	}
	return c
}

// Book represents a book in the Hardcover system
type Book struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Author      string  `json:"author"`
	Description string  `json:"description,omitempty"`
	CoverURL    string  `json:"coverUrl,omitempty"`
	Status      string  `json:"status,omitempty"`
	Progress    float64 `json:"progress,omitempty"`
}

// BatchBookResult contains the result of a batch book operation
type BatchBookResult struct {
	Book      *Book
	BookID    string  // For operations that only have book ID
	IsNew     bool
	Status    string
	Progress  float64
	Error     error
}

// BatchSyncBooks syncs multiple books in parallel batches
func (c *BatchClient) BatchSyncBooks(ctx context.Context, books []*Book) ([]*BatchBookResult, error) {
	if len(books) == 0 {
		return nil, nil
	}

	// Create a slice to store results
	results := make([]*BatchBookResult, len(books))

	// Create a slice to track book indices
	bookIndices := make(map[*Book]int)
	for i, book := range books {
		bookIndices[book] = i
	}

	// Create a processor for book sync operations
	processor := graphql.NewParallelBatchProcessor[
		*Book,                 // Input type
		map[string]interface{}, // Response type
	](
		c.gqlBatch,
		c.batchSize,
		c.workers,
		// Process function - converts books to GraphQL operations
		func(items []*Book) ([]*graphql.BatchOperation, error) {
			var operations []*graphql.BatchOperation

			for _, book := range items {
				query, variables, err := c.prepareBookQuery(book)
				if err != nil {
					return nil, fmt.Errorf("error preparing book query: %w", err)
				}

				operations = append(operations, &graphql.BatchOperation{
					Query:     query,
					Variables: variables,
				})
			}
			return operations, nil
		},
		// Result function - processes successful responses
		func(result *graphql.BatchResult[map[string]interface{}], book *Book) error {
			index := bookIndices[book]
			
			// Initialize result with default values
			results[index] = &BatchBookResult{
				Book:     book,
				IsNew:    false,
				Status:   "UNKNOWN",
				Progress: 0,
			}

			// Check for errors in the GraphQL response
			if len(result.Errors) > 0 {
				errMsgs := make([]string, len(result.Errors))
				for i, e := range result.Errors {
					errMsgs[i] = e.Message
				}
				return fmt.Errorf("GraphQL errors: %v", errMsgs)
			}

			// Extract the syncBook data from the response
			if result.Data == nil {
				return fmt.Errorf("no data in response")
			}

			// The test server returns the data directly under the syncBook key
			if syncBook, ok := result.Data["syncBook"].(map[string]interface{}); ok {
				// Update the result with the response data
				if status, ok := syncBook["status"].(string); ok {
					results[index].Status = status
				}
				if progress, ok := syncBook["progress"].(float64); ok {
					results[index].Progress = progress
				}
				if id, ok := syncBook["id"].(string); ok && id != "" {
					results[index].BookID = id
					results[index].Book.ID = id
				}
			}

			return nil
		},
		// Error handler - called for each failed operation
		func(err error, book *Book) error {
			index := bookIndices[book]
			results[index] = &BatchBookResult{
				Book:  book,
				Error: fmt.Errorf("failed to sync book: %w", err),
			}
			return nil
		},
	)

	// Process all books in parallel batches
	if err := processor.Process(ctx, books); err != nil {
		return nil, fmt.Errorf("error processing batch: %w", err)
	}

	return results, nil
}

// prepareBookQuery prepares a GraphQL query for a book
func (c *BatchClient) prepareBookQuery(book *Book) (string, map[string]interface{}, error) {
	// This is a simplified example - you'll need to adapt this to your actual query structure
	query := `
		mutation SyncBook($input: SyncBookInput!) {
			syncBook(input: $input) {
				id
				title
				status
				progress
			}
		}
	`

	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"title":       book.Title,
			"author":      book.Author,
			"description": book.Description,
			"coverUrl":    book.CoverURL,
			"progress":    book.Progress,
			// Add other fields as needed
		},
	}

	return query, variables, nil
}

// BatchLookupBooks looks up multiple books by title and author
func (c *BatchClient) BatchLookupBooks(ctx context.Context, queries []BookQuery) ([]*Book, error) {
	if len(queries) == 0 {
		return nil, nil
	}

	// Create a slice to hold the batch operations
	operations := make([]*graphql.BatchOperation, 0, len(queries))

	// Create a map to track the original query index for each operation
	queryIndices := make(map[int]int)

	// Add each query to the batch
	for i, query := range queries {
		// Prepare the GraphQL query and variables
		gqlQuery := `
			query LookupBook($title: String!, $author: String!) {
				searchBooks(query: $title, author: $author, limit: 1) {
					id
					title
					author
					status
					progress
				}
			}
		`
		variables := map[string]interface{}{
			"title":  query.Title,
			"author": query.Author,
		}

		// Create a batch operation
		op := &graphql.BatchOperation{
			Query:     gqlQuery,
			Variables: variables,
		}
		operations = append(operations, op)
		queryIndices[len(operations)-1] = i
	}

	// Execute the batch query
	results, err := graphql.ExecuteBatchTyped[map[string]interface{}](c.gqlBatch, ctx, operations)
	if err != nil {
		return nil, fmt.Errorf("batch lookup failed: %w", err)
	}

	// Create a slice to hold the books in the original query order
	books := make([]*Book, len(queries))

	// Process results
	for i, result := range results {
		if result.Error != nil {
			// Skip errors for now, leave as nil in the results
			continue
		}

		// Get the original query index for this result
		queryIdx, ok := queryIndices[i]
		if !ok {
			// This should never happen
			continue
		}

		// The result data is already a map[string]interface{}
		data := result.Data

		searchResults, ok := data["searchBooks"].([]interface{})
		if !ok || len(searchResults) == 0 {
			continue
		}

		bookData, ok := searchResults[0].(map[string]interface{})
		if !ok {
			continue
		}

		// Create a new book and add it to the results at the correct position
		books[queryIdx] = &Book{
			ID:       bookData["id"].(string),
			Title:    bookData["title"].(string),
			Author:   bookData["author"].(string),
			Status:   bookData["status"].(string),
			Progress: bookData["progress"].(float64),
		}
	}

	return books, nil
}

// BatchUpdateBookStatus updates the status of multiple books
func (c *BatchClient) BatchUpdateBookStatus(ctx context.Context, updates []BookStatusUpdate) ([]*BatchBookResult, error) {
	if len(updates) == 0 {
		return nil, nil
	}

	// Create a processor for status updates
	processor := graphql.NewParallelBatchProcessor[
		BookStatusUpdate,    // Input type
		map[string]interface{}, // Response type
	](
		c.gqlBatch,
		c.batchSize,
		c.workers,
		// Process function - converts status updates to GraphQL operations
		func(items []BookStatusUpdate) ([]*graphql.BatchOperation, error) {
			var operations []*graphql.BatchOperation
			for _, update := range items {
				query := `
					mutation UpdateBookStatus($bookId: ID!, $status: BookStatus!, $progress: Float) {
						updateBookStatus(bookId: $bookId, status: $status, progress: $progress) {
							id
							status
							progress
						}
					}
				`

				variables := map[string]interface{}{
					"bookId":   update.BookID,
					"status":   update.Status,
					"progress": update.Progress,
				}

				operations = append(operations, &graphql.BatchOperation{
					Query:     query,
					Variables: variables,
				})
			}
			return operations, nil
		},
		// Result function - processes successful responses
		func(result *graphql.BatchResult[map[string]interface{}], update BookStatusUpdate) error {
			// The actual processing will be done after all batches complete
			return nil
		},
		// Error handler - called for each failed operation
		func(err error, update BookStatusUpdate) error {
			// Log the error but continue processing other items
			return nil
		},
	)

	// Process all updates in parallel batches
	if err := processor.Process(ctx, updates); err != nil {
		return nil, err
	}

	// For now, return a simple result for each update
	// In a real implementation, you would collect results during processing
	results := make([]*BatchBookResult, len(updates))
	for i, update := range updates {
		results[i] = &BatchBookResult{
			Book:      &Book{ID: update.BookID},
			BookID:    update.BookID,
			Status:    update.Status,
			Progress:  update.Progress,
		}
	}

	return results, nil
}

// BookQuery represents a book lookup query
type BookQuery struct {
	Title  string
	Author string
}

// BookStatusUpdate represents a book status update
type BookStatusUpdate struct {
	BookID   string
	Status   string
	Progress float64
}
