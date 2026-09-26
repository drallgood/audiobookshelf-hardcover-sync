package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
	"github.com/stretchr/testify/require"
)

func TestClient_ImportRegionalAudiobook(t *testing.T) {
	tests := []struct {
		name            string
		status          string
		mutationStatus  string
		statusBook      int
		statusEdition   int
		includeMapping  bool
		mappingState    string
		mappingBook     int
		mappingEdition  int
		readbackEdition int
		formatID        int
		wantStatus      RegionalAudiobookStatus
		wantErr         error
	}{
		{name: "created import with mapping", status: "created", includeMapping: true, mappingState: "created", mappingBook: 42, formatID: 2, wantStatus: RegionalAudiobookCreated},
		{name: "loaded import without mapping", status: "loaded", formatID: 2, wantStatus: RegionalAudiobookLoaded},
		{name: "failed import", status: "failed", wantErr: ErrRegionalAudiobookImportFailed},
		{name: "not found import", status: "not_found", wantErr: ErrRegionalAudiobookImportFailed},
		{name: "wrong status book", status: "created", statusBook: 43, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "wrong mapping book", status: "created", includeMapping: true, mappingState: "created", mappingBook: 43, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "mapping and status edition disagree", status: "created", includeMapping: true, mappingState: "created", mappingBook: 42, mappingEdition: 901, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "mutation and status edition disagree", status: "created", statusEdition: 901, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "mutation and polled status disagree", status: "created", mutationStatus: "loaded", wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "wrong readback edition", status: "created", readbackEdition: 901, formatID: 2, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "wrong format readback", status: "created", formatID: 4, wantErr: ErrRegionalAudiobookIdentityConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mutationVariables map[string]interface{}
			var mutationQuery string
			var mappingQueries int
			statusBook := tt.statusBook
			if statusBook == 0 {
				statusBook = 42
			}
			statusEdition := tt.statusEdition
			if statusEdition == 0 {
				statusEdition = 900
			}
			mappingBook := tt.mappingBook
			if mappingBook == 0 {
				mappingBook = 42
			}
			mappingEdition := tt.mappingEdition
			if mappingEdition == 0 {
				mappingEdition = 900
			}
			mappingState := tt.mappingState
			if mappingState == "" {
				mappingState = tt.status
			}
			mutationStatus := tt.mutationStatus
			if mutationStatus == "" {
				mutationStatus = "fetching"
			}
			readbackEdition := tt.readbackEdition
			if readbackEdition == 0 {
				readbackEdition = 900
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
					mutationQuery = request.Query
					mutationVariables = request.Variables
					_, _ = w.Write([]byte(`{"data":{"upsert_book":{"id":77,"status":"` + mutationStatus + `","book":{"id":42},"edition":{"id":900,"book_id":42,"reading_format_id":2},"edition_id":900,"errors":[]}}}`))
				case strings.Contains(request.Query, "RegionalAudibleImport"):
					require.Contains(t, request.Query, "book_mappings(where:")
					require.Contains(t, request.Query, "platform_id: {_eq: $platformId}")
					require.Contains(t, request.Query, "external_id: {_eq: $externalId}")
					require.NotContains(t, request.Query, "book_mappings_by_pk")
					require.Contains(t, request.Query, "book_import_statuses(entries: $entries)")
					mappingQueries++
					var mappings []map[string]interface{}
					if tt.includeMapping {
						mappings = []map[string]interface{}{{
							"id": 77, "state": mappingState, "book_id": mappingBook, "platform_id": 32,
							"external_id": "B0ABCDE123:uk", "edition_id": mappingEdition,
							"edition": map[string]interface{}{"id": mappingEdition, "book_id": mappingBook, "reading_format_id": 2},
						}}
					}
					response, marshalErr := json.Marshal(map[string]interface{}{"data": map[string]interface{}{
						"book_import_statuses": []map[string]interface{}{{
							"status": tt.status, "book_id": statusBook, "edition_id": statusEdition,
							"external_id": "B0ABCDE123:uk", "platform_id": 32,
						}},
						"book_mappings": mappings,
					}})
					require.NoError(t, marshalErr)
					_, _ = w.Write(response)
				case strings.Contains(request.Query, "GetEdition"):
					_, _ = w.Write([]byte(`{"data":{"editions":[{"id":` + intString(readbackEdition) + `,"book_id":42,"reading_format_id":` + intString(tt.formatID) + `}]}}`))
				default:
					t.Errorf("unexpected query: %s", request.Query)
					http.Error(w, "unexpected query", http.StatusBadRequest)
				}
			}))
			defer server.Close()

			client := regionalImportTestClient(server.URL)
			result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
				BookID: 42, ASIN: "b0abcde123", Region: " UK ",
			})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, result)
				if tt.status == "failed" || tt.status == "not_found" {
					require.Equal(t, 1, mappingQueries)
				}
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, result.Status)
			require.Equal(t, 42, result.BookID)
			require.Equal(t, 900, result.EditionID)
			require.Equal(t, 2, result.ReadingFormatID)
			require.Equal(t, "B0ABCDE123:uk", result.RegionalExternalID)
			require.Equal(t, "B0ABCDE123:uk", mutationVariables["book"].(map[string]interface{})["external_id"])
			require.Equal(t, float64(32), mutationVariables["book"].(map[string]interface{})["platform_id"])
			require.Equal(t, float64(42), mutationVariables["book"].(map[string]interface{})["book_id"])
			require.Contains(t, mutationQuery, "upsert_book")
			require.Contains(t, mutationQuery, "status")
			require.NotContains(t, mutationQuery, "insert_edition")
		})
	}
}

func TestClient_ImportRegionalAudiobookRejectsUnknownRegionBeforeMutation(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()

	result, err := regionalImportTestClient(server.URL).ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
		BookID: 42, ASIN: "B0ABCDE123", Region: "",
	})
	require.ErrorIs(t, err, ErrRegionalAudiobookInvalidInput)
	require.Nil(t, result)
	require.False(t, called)
}

func TestClient_ImportRegionalAudiobookDryRunDoesNotMutate(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := regionalImportTestClient(server.URL)
	client.dryRun = true
	result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
		BookID: 42, ASIN: "B0ABCDE123", Region: "uk",
	})
	require.ErrorIs(t, err, ErrRegionalAudiobookDryRun)
	require.Nil(t, result)
	require.False(t, called)
}

func TestClient_ImportRegionalAudiobookStopsWhenContextExpires(t *testing.T) {
	mappingQueries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "UpsertRegionalAudibleBook") {
			_, _ = w.Write([]byte(`{"data":{"upsert_book":{"id":77,"status":"fetching","book":{"id":42},"edition_id":900,"errors":[]}}}`))
			return
		}
		mappingQueries++
		_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[{"status":"fetching","book_id":null,"edition_id":null,"external_id":"B0ABCDE123:uk","platform_id":32}],"book_mappings":[]}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := regionalImportTestClient(server.URL).ImportRegionalAudiobook(ctx, RegionalAudiobookInput{
		BookID: 42, ASIN: "B0ABCDE123", Region: "uk",
	})
	require.Error(t, err)
	require.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, mappingQueries)
}

func regionalImportTestClient(baseURL string) *Client {
	log := logger.Get()
	return &Client{
		baseURL:     baseURL,
		authToken:   "test-token",
		httpClient:  &http.Client{},
		logger:      log,
		rateLimiter: util.NewRateLimiter(time.Millisecond, 1, log),
		maxRetries:  0,
		retryDelay:  time.Millisecond,
	}
}

func intString(value int) string {
	return strconv.Itoa(value)
}
