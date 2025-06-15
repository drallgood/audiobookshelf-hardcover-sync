package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	customhttp "github.com/drallgood/audiobookshelf-hardcover-sync/http"
	errors "github.com/drallgood/audiobookshelf-hardcover-sync/errors"
	"github.com/hashicorp/go-retryablehttp"
)

// Client represents a GraphQL client
// Client represents a GraphQL client
type Client struct {
	client *customhttp.Client
	url    string
	header map[string]string
}

// NewClient creates a new GraphQL client
func NewClient(url string, httpClient *customhttp.Client) *Client {
	return &Client{
		client: httpClient,
		url:    url,
		header: make(map[string]string),
	}
}

// SetHeader sets a header that will be sent with every request
func (c *Client) SetHeader(key, value string) {
	c.header[key] = value
}

// Query executes a GraphQL query
func (c *Client) Query(ctx context.Context, query string, vars map[string]interface{}, result interface{}) error {
	return c.do(ctx, query, vars, result)
}

// Mutation executes a GraphQL mutation
func (c *Client) Mutation(ctx context.Context, mutation string, vars map[string]interface{}, result interface{}) error {
	return c.do(ctx, mutation, vars, result)
}

// do executes a GraphQL operation
func (c *Client) do(ctx context.Context, query string, vars map[string]interface{}, result interface{}) error {
	// Prepare the request body
	body := struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables,omitempty"`
	}{
		Query:     query,
		Variables: vars,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return errors.NewWithCause(
			errors.ValidationError,
			err,
			"failed to marshal GraphQL request",
		)
	}

	// Create a new request
	req, err := retryablehttp.NewRequest("POST", c.url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return errors.NewWithCause(
			errors.APIError,
			err,
			"failed to create GraphQL request",
		)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.header {
		req.Header.Set(key, value)
	}

	// Execute the request
	resp, err := c.client.Do(req)
	if err != nil {
		return errors.NewWithCause(
			errors.APIConnection,
			err,
			"GraphQL request failed",
		)
	}
	defer resp.Body.Close()

	// Read the response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.NewWithCause(
			errors.APIResponseParse,
			err,
			"failed to read GraphQL response",
		)
	}

	// Check for non-200 status code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GraphQL request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse the response
	var gqlResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors,omitempty"`
	}

	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return fmt.Errorf("failed to unmarshal GraphQL response: %w", err)
	}

	// Check for GraphQL errors
	if len(gqlResp.Errors) > 0 {
		errMsgs := make([]string, len(gqlResp.Errors))
		for i, e := range gqlResp.Errors {
			errMsgs[i] = e.Message
		}
		return fmt.Errorf("GraphQL errors: %v", errMsgs)
	}

	// Unmarshal the data into the result
	if result != nil && len(gqlResp.Data) > 0 {
		if err := json.Unmarshal(gqlResp.Data, result); err != nil {
			return fmt.Errorf("failed to unmarshal GraphQL data: %w", err)
		}
	}

	return nil
}
