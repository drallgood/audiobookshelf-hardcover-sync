package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/errors"
	httpclient "github.com/drallgood/audiobookshelf-hardcover-sync/http"
)

// Location represents a location in a GraphQL document
type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// BatchGraphQLRequest represents a single GraphQL operation in a batch
type BatchGraphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// BatchGraphQLResponse represents a single GraphQL response in a batch
type BatchGraphQLResponse struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors []*Error        `json:"errors,omitempty"`
}

// BatchClient handles batch GraphQL operations
type BatchClient struct {
	httpClient *httpclient.BatchClient
	endpoint   string
	batchSize  int
}

// BatchClientOption is a functional option for BatchClient
type BatchClientOption func(*BatchClient)

// WithBatchSize sets the maximum number of operations per batch
func WithBatchSize(size int) BatchClientOption {
	return func(c *BatchClient) {
		c.batchSize = size
	}
}

const defaultBatchSize = 10

// NewBatchClient creates a new batch GraphQL client
func NewBatchClient(httpClient *httpclient.BatchClient, endpoint string, opts ...BatchClientOption) *BatchClient {
	c := &BatchClient{
		httpClient: httpClient,
		endpoint:   endpoint,
		batchSize:  defaultBatchSize,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// NewBatchClientFromHTTP creates a new BatchClient from a standard HTTP client
func NewBatchClientFromHTTP(httpClient *httpclient.Client, endpoint string, opts ...BatchClientOption) *BatchClient {
	// Create a batch client with default config
	batchClient := httpclient.NewBatchClient(
		httpClient,
		httpclient.BatchClientConfig{
			BatchSize:  10,
			MaxWorkers: 5,
			RateLimit:  100 * time.Millisecond, // 10 req/s
		},
	)

	return NewBatchClient(batchClient, endpoint, opts...)
}

// BatchOperation represents a single GraphQL operation in a batch
type BatchOperation struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// Error represents a GraphQL error
// https://spec.graphql.org/October2021/#sec-Errors.Error-result-format
type Error struct {
	Message    string     `json:"message"`
	Locations []Location `json:"locations,omitempty"`
	Path      []string   `json:"path,omitempty"`
	Extensions any        `json:"extensions,omitempty"`
}

// BatchResult represents the result of a single batch operation
type BatchResult[T any] struct {
	Data   T                      `json:"data"`
	Errors []*Error              `json:"errors,omitempty"`
	Index  int                   `json:"-"` // Original request index
	Error  error                 `json:"-"` // Transport error
}

// toHTTPBatchRequest converts a slice of BatchGraphQLRequest to http.BatchGraphQLRequest
func toHTTPBatchRequest(requests []*BatchGraphQLRequest) []*httpclient.BatchGraphQLRequest {
	result := make([]*httpclient.BatchGraphQLRequest, len(requests))
	for i, req := range requests {
		result[i] = &httpclient.BatchGraphQLRequest{
			Query:     req.Query,
			Variables: req.Variables,
		}
	}
	return result
}

// fromHTTPBatchResponse converts a slice of http.BatchGraphQLResponse to BatchGraphQLResponse
func fromHTTPBatchResponse(responses []*httpclient.BatchGraphQLResponse) []*BatchGraphQLResponse {
	result := make([]*BatchGraphQLResponse, len(responses))
	for i, resp := range responses {
		var errors []*Error
		if resp.Errors != nil {
			errors = make([]*Error, len(resp.Errors))
			for j, e := range resp.Errors {
				errors[j] = &Error{
					Message:   e.Message,
					Locations: make([]Location, len(e.Locations)),
				}
				for k, loc := range e.Locations {
					errors[j].Locations[k] = Location{
						Line:   loc.Line,
						Column: loc.Column,
					}
				}
			}
		}

		result[i] = &BatchGraphQLResponse{
			Data:   resp.Data,
			Errors: errors,
		}
	}
	return result
}

// ExecuteBatch executes multiple GraphQL operations in a single batch request
func (c *BatchClient) ExecuteBatch(ctx context.Context, operations []*BatchOperation) ([]*BatchGraphQLResponse, error) {
	if len(operations) == 0 {
		return nil, nil
	}

	// Convert operations to batch requests
	var batchRequests []*BatchGraphQLRequest
	for _, op := range operations {
		batchRequests = append(batchRequests, &BatchGraphQLRequest{
			Query:     op.Query,
			Variables: op.Variables,
		})
	}

	// Convert to HTTP batch requests
	httpRequests := toHTTPBatchRequest(batchRequests)

	// Execute the batch
	httpResponses, err := c.httpClient.DoGraphQLBatch(ctx, c.endpoint, httpRequests)
	if err != nil {
		return nil, errors.NewWithCause(errors.APIError, err, "batch request failed")
	}

	// Convert from HTTP batch responses
	return fromHTTPBatchResponse(httpResponses), nil
}

// ExecuteBatchTyped executes multiple GraphQL operations with typed responses
func ExecuteBatchTyped[T any](c *BatchClient, ctx context.Context, operations []*BatchOperation) ([]*BatchResult[T], error) {
	if len(operations) == 0 {
		return []*BatchResult[T]{}, nil
	}

	responses, err := c.ExecuteBatch(ctx, operations)
	if err != nil {
		return nil, err
	}

	results := make([]*BatchResult[T], len(responses))
	for i, resp := range responses {
		result := &BatchResult[T]{
			Index:  i,
			Errors: resp.Errors,
		}

		if len(resp.Errors) > 0 {
			result.Error = fmt.Errorf("GraphQL errors: %v", resp.Errors)
		} else if resp.Data != nil {
			if err := json.Unmarshal(resp.Data, &result.Data); err != nil {
				result.Error = errors.NewWithCause(errors.APIError, err, "failed to decode response")
			}
		} else {
			result.Error = errors.NewWithCause(errors.APIError, nil, "no data in response")
		}

		results[i] = result
	}

	return results, nil
}

// batchProcessor processes batch operations for a specific type T
type batchProcessor[T any] struct {
	client *BatchClient
}

// ProcessBatch executes a batch of operations and returns typed results for type T
func (p *batchProcessor[T]) ProcessBatch(ctx context.Context, operations []*BatchOperation) ([]*BatchResult[any], error) {
	responses, err := p.client.ExecuteBatch(ctx, operations)
	if err != nil {
		return nil, err
	}

	results := make([]*BatchResult[any], len(responses))
	for i, resp := range responses {
		result := &BatchResult[any]{
			Data:   new(T),
			Errors: resp.Errors,
		}

		if len(resp.Errors) > 0 {
			result.Error = fmt.Errorf("GraphQL errors: %v", resp.Errors)
		} else if resp.Data != nil {
			if err := json.Unmarshal(resp.Data, result.Data); err != nil {
				result.Error = errors.NewWithCause(errors.APIError, err, "failed to decode response")
			}
		} else {
			result.Error = errors.NewWithCause(errors.APIError, nil, "no data in response")
		}

		results[i] = result
	}

	return results, nil
}

// BatchQueryBuilder helps build batch queries
type BatchQueryBuilder struct {
	operations []*BatchOperation
}

// NewBatchQueryBuilder creates a new batch query builder
func NewBatchQueryBuilder() *BatchQueryBuilder {
	return &BatchQueryBuilder{
		operations: make([]*BatchOperation, 0),
	}
}

// Add adds a new query to the batch
func (b *BatchQueryBuilder) Add(query string, variables map[string]interface{}) *BatchQueryBuilder {
	b.operations = append(b.operations, &BatchOperation{
		Query:     query,
		Variables: variables,
	})
	return b
}

// Execute executes the batch of queries
func (b *BatchQueryBuilder) Execute(c *BatchClient, ctx context.Context) ([]*BatchGraphQLResponse, error) {
	return c.ExecuteBatch(ctx, b.operations)
}

// Execute executes the batch of queries with typed responses
func ExecuteTyped[T any](b *BatchQueryBuilder, c *BatchClient, ctx context.Context) ([]*BatchResult[T], error) {
	return ExecuteBatchTyped[T](c, ctx, b.operations)
}

// BatchProcessor processes items in batches using GraphQL operations
type BatchProcessor[T any, R any] struct {
	client     *BatchClient
	batchSize  int
	processFn  func([]T) ([]*BatchOperation, error)
	resultFn   func(*BatchResult[R], T) error
	errHandler func(error, T) error
}

// NewBatchProcessor creates a new batch processor
func NewBatchProcessor[T any, R any](
	client *BatchClient,
	batchSize int,
	processFn func([]T) ([]*BatchOperation, error),
	resultFn func(*BatchResult[R], T) error,
	errHandler func(error, T) error,
) *BatchProcessor[T, R] {
	return &BatchProcessor[T, R]{
		client:     client,
		batchSize:  batchSize,
		processFn:  processFn,
		resultFn:   resultFn,
		errHandler: errHandler,
	}
}

// Process processes items in batches
func (p *BatchProcessor[T, R]) Process(ctx context.Context, items []T) error {
	for i := 0; i < len(items); i += p.batchSize {
		end := i + p.batchSize
		if end > len(items) {
			end = len(items)
		}

		batch := items[i:end]
		operations, err := p.processFn(batch)
		if err != nil {
			for _, item := range batch {
				if err := p.errHandler(err, item); err != nil {
					return err
				}
			}
			continue
		}

		results, err := ExecuteBatchTyped[R](p.client, ctx, operations)
		if err != nil {
			for _, item := range batch {
				if err := p.errHandler(err, item); err != nil {
					return err
				}
			}
			continue
		}

		// Map results back to original items
		for _, result := range results {
			if result.Index >= len(batch) {
				continue // Shouldn't happen if batch is processed correctly
			}
			item := batch[result.Index]
			if err := p.resultFn(result, item); err != nil {
				if err := p.errHandler(err, item); err != nil {
					return err
				}
			}
		}

		// Respect rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// ParallelBatchProcessor processes items in parallel batches
type ParallelBatchProcessor[T any, R any] struct {
	client     *BatchClient
	batchSize  int
	workers    int
	processFn  func([]T) ([]*BatchOperation, error)
	resultFn   func(*BatchResult[R], T) error
	errHandler func(error, T) error
}

// NewParallelBatchProcessor creates a new parallel batch processor
func NewParallelBatchProcessor[T any, R any](
	client *BatchClient,
	batchSize int,
	workers int,
	processFn func([]T) ([]*BatchOperation, error),
	resultFn func(*BatchResult[R], T) error,
	errHandler func(error, T) error,
) *ParallelBatchProcessor[T, R] {
	return &ParallelBatchProcessor[T, R]{
		client:     client,
		batchSize:  batchSize,
		workers:    workers,
		processFn:  processFn,
		resultFn:   resultFn,
		errHandler: errHandler,
	}
}

// Process processes items in parallel batches
func (p *ParallelBatchProcessor[T, R]) Process(ctx context.Context, items []T) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	batchCh := make(chan []T, len(items)/p.batchSize+1)

	// Start worker goroutines
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range batchCh {
				if err := p.processBatch(ctx, batch); err != nil {
					select {
					case errCh <- err:
					default: // Don't block if error already set
					}
					return
				}
			}
		}()
	}

	// Distribute work in batches
	for i := 0; i < len(items); i += p.batchSize {
		end := i + p.batchSize
		if end > len(items) {
			end = len(items)
		}
		select {
		case batchCh <- items[i:end]:
		case err := <-errCh:
			close(batchCh)
			return err
		}
	}

	close(batchCh)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (p *ParallelBatchProcessor[T, R]) processBatch(ctx context.Context, batch []T) error {
	operations, err := p.processFn(batch)
	if err != nil {
		for _, item := range batch {
			if err := p.errHandler(err, item); err != nil {
				return err
			}
		}
		return nil
	}

	results, err := ExecuteBatchTyped[R](p.client, ctx, operations)
	if err != nil {
		for _, item := range batch {
			if err := p.errHandler(err, item); err != nil {
				return err
			}
		}
		return nil
	}

	// Map results back to original items
	for _, result := range results {
		if result.Index >= len(batch) {
			continue // Shouldn't happen if batch is processed correctly
		}
		item := batch[result.Index]
		if err := p.resultFn(result, item); err != nil {
			if err := p.errHandler(err, item); err != nil {
				return err
			}
		}
	}

	return nil
}
