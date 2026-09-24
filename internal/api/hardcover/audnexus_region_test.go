package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchBookByASINUsesConfiguredBrazilRegionForLegacyMapping(t *testing.T) {
	variables := make(chan map[string]interface{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read GraphQL request: %v", err)
			http.Error(w, "failed to read request", http.StatusBadRequest)
			return
		}
		var request struct {
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode GraphQL request: %v", err)
			http.Error(w, "failed to decode request", http.StatusBadRequest)
			return
		}
		variables <- request.Variables
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[]}}`))
	}))
	defer server.Close()

	const asin = "B012345678"
	client := CreateTestClient(server)
	book, err := client.SearchBookByASIN(WithAudnexRegion(context.Background(), "br"), asin)
	require.NoError(t, err)
	require.Nil(t, book)

	requestVariables := <-variables
	require.Equal(t, asin, requestVariables["asin"])
	require.Equal(t, asin+":br", requestVariables["asin_us"], "legacy Hardcover matching should use the saved Brazil region")
}
