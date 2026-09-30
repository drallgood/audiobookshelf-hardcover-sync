package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
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
		// reportedFormat is the reading format in the upsert and mapping
		// responses; zero means audiobook.
		reportedFormat int
		wantStatus     RegionalAudiobookStatus
		wantErr        error
		wantNotErr     error
		wantMessage    string
	}{
		{name: "created import with mapping", status: "created", includeMapping: true, mappingState: "created", mappingBook: 42, formatID: 2, wantStatus: RegionalAudiobookCreated},
		{name: "loaded import without mapping", status: "loaded", formatID: 2, wantStatus: RegionalAudiobookLoaded},
		{name: "failed import", status: "failed", includeMapping: true, mappingState: "failed", wantErr: ErrRegionalAudiobookImportFailed},
		{name: "pending import with failed mapping", status: "fetching", includeMapping: true, mappingState: "failed", wantErr: ErrRegionalAudiobookIdentityConflict, wantNotErr: ErrRegionalAudiobookImportFailed},
		{name: "missing import status with failed mapping", includeMapping: true, mappingState: "failed", wantErr: ErrRegionalAudiobookIdentityConflict, wantNotErr: ErrRegionalAudiobookImportFailed},
		{name: "not found import", status: "not_found", wantErr: ErrRegionalAudiobookImportFailed},
		{name: "unknown import outcome", status: "unknown", wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "wrong status book", status: "created", statusBook: 43, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "wrong mapping book", status: "created", includeMapping: true, mappingState: "created", mappingBook: 43, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "mapping and status edition disagree", status: "created", includeMapping: true, mappingState: "created", mappingBook: 42, mappingEdition: 901, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "mutation and status edition disagree", status: "created", statusEdition: 901, wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "mutation and polled status disagree", status: "created", mutationStatus: "loaded", wantErr: ErrRegionalAudiobookIdentityConflict},
		{name: "wrong readback edition", status: "created", readbackEdition: 901, formatID: 2, wantErr: ErrRegionalAudiobookIdentityConflict, wantMessage: "expected edition 900 on book 42, got edition 901 on book 42"},
		{name: "physical format readback", status: "created", formatID: 1, wantErr: ErrRegionalAudiobookWrongFormat, wantMessage: "edition 900 on book 42 has reading format physical book (ID 1); expected audiobook (ID 2)"},
		{name: "ebook format readback", status: "created", formatID: 4, wantErr: ErrRegionalAudiobookWrongFormat, wantMessage: "edition 900 on book 42 has reading format ebook (ID 4); expected audiobook (ID 2)"},
		{name: "unknown format readback", status: "created", formatID: 99, wantErr: ErrRegionalAudiobookWrongFormat, wantMessage: "edition 900 on book 42 has reading format unknown (ID 99); expected audiobook (ID 2)"},
		{name: "existing physical edition loaded by ISBN-shaped ASIN", status: "loaded", includeMapping: true, mappingState: "loaded", reportedFormat: 1, formatID: 1, wantErr: ErrRegionalAudiobookWrongFormat, wantMessage: "physical book (ID 1)"},
		{name: "reported physical format corrected by readback", status: "loaded", includeMapping: true, mappingState: "loaded", reportedFormat: 1, formatID: 2, wantStatus: RegionalAudiobookLoaded},
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
			reportedFormat := tt.reportedFormat
			if reportedFormat == 0 {
				reportedFormat = 2
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
					_, _ = w.Write([]byte(`{"data":{"upsert_book":{"id":77,"status":"` + mutationStatus + `","book":{"id":42},"edition":{"id":900,"book_id":42,"reading_format_id":` + intString(reportedFormat) + `},"edition_id":900,"errors":[]}}}`))
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
							"edition": map[string]interface{}{"id": mappingEdition, "book_id": mappingBook, "reading_format_id": reportedFormat},
						}}
					}
					statuses := []map[string]interface{}{}
					if tt.status != "" {
						statuses = append(statuses, map[string]interface{}{
							"status": tt.status, "book_id": statusBook, "edition_id": statusEdition,
							"external_id": "B0ABCDE123:uk", "platform_id": 32,
						})
					}
					response, marshalErr := json.Marshal(map[string]interface{}{"data": map[string]interface{}{
						"book_import_statuses": statuses,
						"book_mappings":        mappings,
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
				if tt.wantNotErr != nil {
					require.NotErrorIs(t, err, tt.wantNotErr)
				}
				require.Nil(t, result)
				if tt.wantMessage != "" {
					require.Contains(t, err.Error(), tt.wantMessage)
				}
				if tt.wantErr == ErrRegionalAudiobookWrongFormat {
					var wrongFormat *RegionalAudiobookWrongFormatError
					require.ErrorAs(t, err, &wrongFormat)
					require.Equal(t, RegionalAudiobookWrongFormatError{BookID: 42, EditionID: 900, ReadingFormatID: strconv.Itoa(tt.formatID)}, *wrongFormat)
					require.ErrorIs(t, err, ErrRegionalAudiobookIdentityConflict)
				}
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

type regionalImportPollingTransport func(*http.Request) (*http.Response, error)

func (f regionalImportPollingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Polling is part of the quota budget: pending imports must back off without
// delaying an immediately completed import or retrying the catalogue write.
func TestRegionalImportPollingRequestBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		timeout       time.Duration
		completeAfter int
		wantPolls     int
		wantErr       error
	}{
		{name: "pending import backs off until cancellation", timeout: 2500 * time.Millisecond, wantPolls: 2, wantErr: context.DeadlineExceeded},
		{name: "pending import respects full deadline and quota budget", timeout: 35 * time.Second, wantPolls: 8, wantErr: ErrRegionalAudiobookImportTimeout},
		{name: "pending import eventually verifies edition", timeout: 10 * time.Second, completeAfter: 3, wantPolls: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var mutations, polls, readbacks int
				client := regionalImportTestClient("http://hardcover.test")
				client.httpClient.Transport = regionalImportPollingTransport(func(r *http.Request) (*http.Response, error) {
					w := httptest.NewRecorder()
					var request struct {
						Query string `json:"query"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
						mutations++
						_, _ = w.Write([]byte(`{"data":{"upsert_book":{"status":"fetching","book":{"id":42},"errors":[]}}}`))
					case strings.Contains(request.Query, "RegionalAudibleImport"):
						polls++
						status := "fetching"
						if tc.completeAfter > 0 && polls >= tc.completeAfter {
							status = "created"
						}
						_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[{"status":"` + status + `","book_id":42,"edition_id":900,"external_id":"B0ABCDE123:uk","platform_id":32}],"book_mappings":[]}}`))
					case strings.Contains(request.Query, "GetEdition"):
						readbacks++
						_, _ = w.Write([]byte(`{"data":{"editions":[{"id":900,"book_id":42,"reading_format_id":2}]}}`))
					default:
						t.Errorf("unexpected query: %s", request.Query)
						http.Error(w, "unexpected query", http.StatusBadRequest)
					}
					return w.Result(), nil
				})
				ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
				defer cancel()
				result, err := client.ImportRegionalAudiobook(ctx, RegionalAudiobookInput{BookID: 42, ASIN: "B0ABCDE123", Region: "uk"})
				require.Equal(t, 1, mutations)
				require.Equal(t, tc.wantPolls, polls)
				if tc.completeAfter == 0 {
					require.ErrorIs(t, err, tc.wantErr)
					require.Nil(t, result)
					require.Zero(t, readbacks)
				} else {
					require.NoError(t, err)
					require.Equal(t, 900, result.EditionID)
					require.Equal(t, 1, readbacks)
				}
			})
		})
	}
}

func TestClient_CheckRegionalAudiobookImportIsReadOnlyAndVerifiesCompletedMapping(t *testing.T) {
	tests := []struct {
		name       string
		statuses   []map[string]interface{}
		mappings   []map[string]interface{}
		wantStatus RegionalAudiobookStatus
		confirmed  bool
		wantErr    error
	}{
		{
			name: "pending import", statuses: []map[string]interface{}{{
				"status": "fetching", "external_id": "B0ABCDE123:uk", "platform_id": 32,
			}},
		},
		{
			name: "completed import", statuses: []map[string]interface{}{{
				"status": "created", "book_id": 42, "edition_id": 900, "external_id": "B0ABCDE123:uk", "platform_id": 32,
			}}, mappings: []map[string]interface{}{{
				"id": 77, "state": "created", "book_id": 42, "platform_id": 32,
				"external_id": "B0ABCDE123:uk", "edition_id": 900,
				"edition": map[string]interface{}{"id": 900, "book_id": 42, "reading_format_id": 2},
			}}, wantStatus: RegionalAudiobookCreated, confirmed: true,
		},
		{
			name: "normalized mapping remains after status expiry", mappings: []map[string]interface{}{{
				"id": 77, "state": "normalized", "book_id": 42, "platform_id": 32,
				"external_id": "B0ABCDE123:uk", "edition_id": 900,
				"edition": map[string]interface{}{"id": 900, "book_id": 42, "reading_format_id": 2},
			}}, wantStatus: RegionalAudiobookLoaded, confirmed: true,
		},
		{
			name: "failed import", statuses: []map[string]interface{}{{
				"status": "failed", "external_id": "B0ABCDE123:uk", "platform_id": 32, "error": "rejected",
			}}, wantErr: ErrRegionalAudiobookImportFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var importQueries, editionReads, mutations int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(request.Query, "RegionalAudibleImport"):
					importQueries++
					require.Equal(t, "B0ABCDE123:uk", request.Variables["externalId"])
					payload, err := json.Marshal(map[string]interface{}{"data": map[string]interface{}{
						"book_import_statuses": tt.statuses, "book_mappings": tt.mappings,
					}})
					require.NoError(t, err)
					_, _ = w.Write(payload)
				case strings.Contains(request.Query, "GetEdition"):
					editionReads++
					_, _ = w.Write([]byte(`{"data":{"editions":[{"id":900,"book_id":42,"reading_format_id":2}]}}`))
				case strings.Contains(request.Query, "mutation"):
					mutations++
					http.Error(w, "unexpected mutation", http.StatusBadRequest)
				default:
					t.Errorf("unexpected query: %s", request.Query)
					http.Error(w, "unexpected query", http.StatusBadRequest)
				}
			}))
			defer server.Close()

			result, confirmed, err := regionalImportTestClient(server.URL).CheckRegionalAudiobookImport(context.Background(), RegionalAudiobookInput{
				BookID: 42, ASIN: "b0abcde123", Region: " UK ",
			})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.False(t, confirmed)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.confirmed, confirmed)
				if confirmed {
					require.Equal(t, tt.wantStatus, result.Status)
					require.Equal(t, 42, result.BookID)
					require.Equal(t, 900, result.EditionID)
					require.Equal(t, 2, result.ReadingFormatID)
				} else {
					require.Nil(t, result)
				}
			}
			require.Equal(t, 1, importQueries, "recovery performs one import-status query")
			require.Equal(t, boolInt(tt.confirmed), editionReads, "only terminal results are read back")
			require.Zero(t, mutations, "recovery never submits a Hardcover mutation")
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
