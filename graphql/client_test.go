// graphql/client_test.go
package graphql_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/graphql"
	"github.com/drallgood/audiobookshelf-hardcover-sync/http"
	"github.com/drallgood/audiobookshelf-hardcover-sync/logging"
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

		// Prepare response
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"test": "success",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// Create HTTP client
	httpClient := http.NewClient(nil) // Using default config for tests

	// Create GraphQL client
	gqlClient := graphql.NewClient(ts.URL, httpClient)

	// Test query
	t.Run("successful query", func(t *testing.T) {
		var result struct {
			Test string `json:"test"`
		}

		query := `query Test { test }`
		err := gqlClient.Query(context.Background(), query, nil, &result)

		assert.NoError(t, err)
		assert.Equal(t, "success", result.Test)
	})

	// Test with variables
	t.Run("with variables", func(t *testing.T) {
		var result struct {
			Test string `json:"test"`
		}

		query := `query Test($id: ID!) { test }`
		vars := map[string]interface{}{"id": "123"}
		err := gqlClient.Query(context.Background(), query, vars, &result)

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

		client := graphql.NewClient(errorServer.URL, httpClient)
		err := client.Query(context.Background(), "query {}", nil, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "test error")
	})
}

func init() {
	// Initialize logger for tests
	logging.InitForTesting()
}
