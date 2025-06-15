package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/config"
	customhttp "github.com/drallgood/audiobookshelf-hardcover-sync/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphQLClient(t *testing.T) {
	// Setup test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var reqBody struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		// Prepare response based on the query
		var resp map[string]interface{}
		if reqBody.Query == `{ test }` {
			resp = map[string]interface{}{
				"data": map[string]interface{}{
					"test": "success",
				},
			}
		} else if reqBody.Query == `query Test($id: ID!) { test }` {
			// For the variables test, just return a success response
			resp = map[string]interface{}{
				"data": map[string]interface{}{
					"test": "success",
				},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// Create a proper HTTP client with default config
	cfg := &config.Config{
		HTTP: config.HTTPConfig{
			Timeout:            30 * time.Second,
			MaxIdleConns:       100,
			IdleConnTimeout:    90 * time.Second,
			DisableCompression: false,
			RetryWaitMin:       1 * time.Second,
			RetryWaitMax:       30 * time.Second,
			MaxRetries:         3,
		},
	}
	httpClient := customhttp.NewClient(cfg)

	// Create GraphQL client
	gqlClient := NewClient(ts.URL, httpClient)

	// Test query
	t.Run("successful query", func(t *testing.T) {
		// Execute query
		var resp struct {
			Test string `json:"test"`
		}
		err := gqlClient.Query(context.Background(), `{ test }`, nil, &resp)
		require.NoError(t, err)

		// Verify the response
		assert.Equal(t, "success", resp.Test)
	})

	// Test with variables
	t.Run("with variables", func(t *testing.T) {
		query := `query Test($id: ID!) { test }`
		vars := map[string]interface{}{"id": "123"}
		err := gqlClient.Query(context.Background(), query, vars, nil)

		assert.NoError(t, err)
	})

	// Test error response
	t.Run("graphql error", func(t *testing.T) {
		errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := map[string]interface{}{
				"errors": []map[string]string{
					{"message": "test error"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer errorServer.Close()

		client := NewClient(errorServer.URL, httpClient)
		err := client.Query(context.Background(), "query {}", nil, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "test error")
	})
}

// Test helper functions can be added here if needed
