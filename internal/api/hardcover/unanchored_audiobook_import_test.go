package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cross the real GraphQL HTTP boundary. The API contract is that
// an unanchored import omits book_id entirely; a zero-valued book_id would
// still ask Hardcover to attach the Audible mapping to a particular book.
func TestImportRegionalAudiobookUnanchoredOmitsBookIDAndResolvesIdentity(t *testing.T) {
	for _, status := range []RegionalAudiobookStatus{RegionalAudiobookLoaded, RegionalAudiobookCreated} {
		t.Run(string(status), func(t *testing.T) {
			var mutationBook map[string]interface{}
			var mutations, statusReads, editionReads int
			client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
					mutations++
					mutationBook = request.Variables["book"].(map[string]interface{})
					_, _ = w.Write([]byte(`{"data":{"upsert_book":{"id":77,"status":"` + string(status) + `","book":{"id":73},"edition":{"id":900,"book_id":73,"reading_format_id":2},"edition_id":900,"errors":[]}}}`))
				case strings.Contains(request.Query, "RegionalAudibleImport"):
					statusReads++
					require.Equal(t, "B0ABCDE123:uk", request.Variables["externalId"])
					payload, err := json.Marshal(map[string]interface{}{"data": map[string]interface{}{
						"book_import_statuses": []map[string]interface{}{{
							"status": status, "book_id": 73, "edition_id": 900,
							"external_id": "B0ABCDE123:uk", "platform_id": 32,
						}},
						"book_mappings": []map[string]interface{}{},
					}})
					require.NoError(t, err)
					_, _ = w.Write(payload)
				case strings.Contains(request.Query, "GetEdition"):
					editionReads++
					_, _ = w.Write([]byte(`{"data":{"editions":[{"id":900,"book_id":73,"reading_format_id":2}]}}`))
				default:
					t.Errorf("unexpected GraphQL request: %s", request.Query)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))

			result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
				ASIN: "b0abcde123", Region: " UK ", Unanchored: true,
			})

			require.NoError(t, err)
			require.Equal(t, 1, mutations)
			require.NotContains(t, mutationBook, "book_id")
			require.Equal(t, "B0ABCDE123:uk", mutationBook["external_id"])
			require.Equal(t, float64(32), mutationBook["platform_id"])
			require.Equal(t, 1, statusReads)
			require.Equal(t, 1, editionReads, "success requires a fresh read of the returned edition")
			require.Equal(t, status, result.Status)
			require.Equal(t, 73, result.BookID, "the resolved book ID comes from Hardcover")
			require.Equal(t, 900, result.EditionID)
			require.Equal(t, 2, result.ReadingFormatID)
			require.Equal(t, "B0ABCDE123:uk", result.RegionalExternalID)
		})
	}
}

func TestImportRegionalAudiobookUnanchoredResolvesNullableTerminalStatusIDs(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		pendingBeforeTerminal bool
		upsertBook            string
		statusBookID          string
		statusEditionID       string
		mapping               bool
	}{
		{name: "status book ID after pending response", pendingBeforeTerminal: true, upsertBook: "null", statusBookID: "73", statusEditionID: "900"},
		{name: "terminal mapping book ID after pending response", pendingBeforeTerminal: true, upsertBook: "null", statusBookID: "null", statusEditionID: "null", mapping: true},
		{name: "mapping resolves IDs missing from status after upsert book", upsertBook: `{"id":73}`, statusBookID: "null", statusEditionID: "null", mapping: true},
		{name: "mapping resolves missing status edition after upsert book", upsertBook: `{"id":73}`, statusBookID: "73", statusEditionID: "null", mapping: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var mutations, statusReads, editionReads int
				client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						Query string `json:"query"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
						mutations++
						_, _ = w.Write([]byte(`{"data":{"upsert_book":{"id":77,"status":"fetching","book":` + tc.upsertBook + `,"errors":[]}}}`))
					case strings.Contains(request.Query, "RegionalAudibleImport"):
						statusReads++
						status, bookID, editionID := "created", tc.statusBookID, tc.statusEditionID
						mappings := `[]`
						if tc.pendingBeforeTerminal && statusReads == 1 {
							status, bookID, editionID = "fetching", "null", "null"
						} else if tc.mapping {
							mappings = `[{"id":77,"state":"created","book_id":73,"platform_id":32,"external_id":"B0ABCDE123:uk","edition_id":900,"edition":{"id":900,"book_id":73,"reading_format_id":2}}]`
						}
						_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[{"status":"` + status + `","book_id":` + bookID + `,"edition_id":` + editionID + `,"external_id":"B0ABCDE123:uk","platform_id":32}],"book_mappings":` + mappings + `}}`))
					case strings.Contains(request.Query, "GetEdition"):
						editionReads++
						_, _ = w.Write([]byte(`{"data":{"editions":[{"id":900,"book_id":73,"reading_format_id":2}]}}`))
					default:
						t.Errorf("unexpected GraphQL request: %s", request.Query)
						http.Error(w, "unexpected request", http.StatusBadRequest)
					}
				}))

				result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
					ASIN: "B0ABCDE123", Region: "uk", Unanchored: true,
				})

				require.NoError(t, err)
				require.Equal(t, 1, mutations, "the catalogue mutation is never replayed")
				wantStatusReads := 1
				if tc.pendingBeforeTerminal {
					wantStatusReads = 2
				}
				require.Equal(t, wantStatusReads, statusReads, "the status is polled until its trusted book and edition IDs appear")
				require.Equal(t, 1, editionReads, "success requires a fresh edition read")
				require.Equal(t, RegionalAudiobookCreated, result.Status)
				require.Equal(t, 73, result.BookID)
				require.Equal(t, 900, result.EditionID)
				require.Equal(t, 2, result.ReadingFormatID)
			})
		})
	}
}

func TestImportRegionalAudiobookUnanchoredRejectsNonpositiveStatusIDsDespiteMapping(t *testing.T) {
	var mutations, editionReads int
	client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
			mutations++
			_, _ = w.Write([]byte(`{"data":{"upsert_book":{"status":"fetching","book":null,"errors":[]}}}`))
		case strings.Contains(request.Query, "RegionalAudibleImport"):
			_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[{"status":"created","book_id":73,"edition_id":0,"external_id":"B0ABCDE123:uk","platform_id":32}],"book_mappings":[{"id":77,"state":"created","book_id":73,"platform_id":32,"external_id":"B0ABCDE123:uk","edition_id":900,"edition":{"id":900,"book_id":73,"reading_format_id":2}}]}}`))
		case strings.Contains(request.Query, "GetEdition"):
			editionReads++
			t.Errorf("a nonpositive terminal edition ID cannot be repaired by a mapping")
		default:
			t.Errorf("unexpected GraphQL request: %s", request.Query)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))

	result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
		ASIN: "B0ABCDE123", Region: "uk", Unanchored: true,
	})

	require.ErrorIs(t, err, ErrRegionalAudiobookIdentityConflict)
	require.Nil(t, result)
	require.Equal(t, 1, mutations)
	require.Zero(t, editionReads)
}

func TestImportRegionalAudiobookRequiresExplicitUnanchoredModeForZeroBookID(t *testing.T) {
	for _, input := range []RegionalAudiobookInput{
		{ASIN: "B0ABCDE123", Region: "uk"},
		{ASIN: "B0ABCDE123", Region: "uk", Unanchored: true, BookID: 42},
	} {
		t.Run(map[bool]string{true: "unanchored with book ID", false: "anchored without book ID"}[input.Unanchored], func(t *testing.T) {
			called := false
			client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				http.Error(w, "unexpected request", http.StatusBadRequest)
			}))

			result, err := client.ImportRegionalAudiobook(context.Background(), input)

			require.ErrorIs(t, err, ErrRegionalAudiobookInvalidInput)
			require.Nil(t, result)
			require.False(t, called, "invalid anchor mode must be rejected before any request")
		})
	}
}

func TestImportRegionalAudiobookUnanchoredDryRunDoesNotReachGraphQL(t *testing.T) {
	var requests int
	client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "dry-run import must not make a request", http.StatusBadRequest)
	}))
	client.SetDryRun(true)

	result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
		ASIN: "B0ABCDE123", Region: "uk", Unanchored: true,
	})

	require.ErrorIs(t, err, ErrRegionalAudiobookDryRun)
	require.Nil(t, result)
	require.Zero(t, requests)
}

func TestImportRegionalAudiobookUnanchoredDistinguishesFailedAndTransientStatus(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          string
		statusHTTP      int
		wantErr         error
		wantStatusReads int
	}{
		{name: "terminal failed status", status: "failed", wantErr: ErrRegionalAudiobookImportFailed, wantStatusReads: 1},
		{name: "temporary status outage", statusHTTP: http.StatusServiceUnavailable, wantStatusReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations, statusReads, editionReads int
			client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query string `json:"query"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
					mutations++
					_, _ = w.Write([]byte(`{"data":{"upsert_book":{"status":"fetching","book":{"id":73},"errors":[]}}}`))
				case strings.Contains(request.Query, "RegionalAudibleImport"):
					statusReads++
					if tc.statusHTTP != 0 {
						w.WriteHeader(tc.statusHTTP)
						_, _ = w.Write([]byte(`{"errors":[{"message":"temporary outage"}]}`))
						return
					}
					_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[{"status":"` + tc.status + `","book_id":73,"external_id":"B0ABCDE123:uk","platform_id":32}],"book_mappings":[]}}`))
				case strings.Contains(request.Query, "GetEdition"):
					editionReads++
					t.Errorf("terminal failure must not be reported as a verified edition")
				default:
					t.Errorf("unexpected GraphQL request: %s", request.Query)
				}
			}))
			client.maxRetries = 0

			result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
				ASIN: "B0ABCDE123", Region: "uk", Unanchored: true,
			})

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.Error(t, err)
				require.NotErrorIs(t, err, ErrRegionalAudiobookImportFailed, "a transport failure is not a terminal import failure")
			}
			require.Nil(t, result)
			require.Equal(t, 1, mutations, "a status failure must not resubmit the mutation")
			require.Equal(t, tc.wantStatusReads, statusReads)
			require.Zero(t, editionReads)
		})
	}
}

func TestImportRegionalAudiobookUnanchoredPendingImportTimesOutWithoutResubmitting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mutations, statusReads, editionReads int
		client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Query string `json:"query"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
				mutations++
				_, _ = w.Write([]byte(`{"data":{"upsert_book":{"status":"fetching","book":null,"errors":[]}}}`))
			case strings.Contains(request.Query, "RegionalAudibleImport"):
				statusReads++
				_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[{"status":"fetching","book_id":null,"external_id":"B0ABCDE123:uk","platform_id":32}],"book_mappings":[]}}`))
			case strings.Contains(request.Query, "GetEdition"):
				editionReads++
			default:
				t.Errorf("unexpected GraphQL request: %s", request.Query)
			}
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()

		result, err := client.ImportRegionalAudiobook(ctx, RegionalAudiobookInput{
			ASIN: "B0ABCDE123", Region: "uk", Unanchored: true,
		})

		require.ErrorIs(t, err, ErrRegionalAudiobookImportTimeout)
		require.Nil(t, result)
		require.Equal(t, 1, mutations, "polling a pending import never submits a second mutation")
		require.Greater(t, statusReads, 0)
		require.Zero(t, editionReads, "a pending import has no verified edition to read")
	})
}

func TestImportRegionalAudiobookUnanchoredRejectsConflictingBookIdentity(t *testing.T) {
	var statusReads int
	client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
			_, _ = w.Write([]byte(`{"data":{"upsert_book":{"status":"created","book":{"id":73},"edition":{"id":900,"book_id":74,"reading_format_id":2},"edition_id":900,"errors":[]}}}`))
		case strings.Contains(request.Query, "RegionalAudibleImport"):
			statusReads++
			_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[],"book_mappings":[]}}`))
		default:
			t.Errorf("unexpected GraphQL request: %s", request.Query)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))

	result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
		ASIN: "B0ABCDE123", Region: "uk", Unanchored: true,
	})

	require.ErrorIs(t, err, ErrRegionalAudiobookIdentityConflict)
	require.Nil(t, result)
	require.Zero(t, statusReads, "a conflicting mutation result must be rejected before polling")
}

func TestImportRegionalAudiobookUnanchoredRejectsNonAudiobookFreshRead(t *testing.T) {
	client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
			_, _ = w.Write([]byte(`{"data":{"upsert_book":{"status":"loaded","book":{"id":73},"edition":{"id":900,"book_id":73,"reading_format_id":2},"edition_id":900,"errors":[]}}}`))
		case strings.Contains(request.Query, "RegionalAudibleImport"):
			_, _ = w.Write([]byte(`{"data":{"book_import_statuses":[{"status":"loaded","book_id":73,"edition_id":900,"external_id":"B0ABCDE123:uk","platform_id":32}],"book_mappings":[]}}`))
		case strings.Contains(request.Query, "GetEdition"):
			_, _ = w.Write([]byte(`{"data":{"editions":[{"id":900,"book_id":73,"reading_format_id":1}]}}`))
		default:
			t.Errorf("unexpected GraphQL request: %s", request.Query)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))

	result, err := client.ImportRegionalAudiobook(context.Background(), RegionalAudiobookInput{
		ASIN: "B0ABCDE123", Region: "uk", Unanchored: true,
	})

	require.ErrorIs(t, err, ErrRegionalAudiobookWrongFormat)
	require.ErrorIs(t, err, ErrRegionalAudiobookIdentityConflict)
	require.Nil(t, result)
}

func TestCheckRegionalAudiobookImportUnanchoredResolvesBookFromStatusOrMapping(t *testing.T) {
	for _, tc := range []struct {
		name             string
		withStatus       bool
		withMapping      bool
		missingStatusIDs bool
		wantStatus       RegionalAudiobookStatus
	}{
		{name: "terminal import status", withStatus: true, wantStatus: RegionalAudiobookCreated},
		{name: "terminal status resolved from mapping", withStatus: true, withMapping: true, missingStatusIDs: true, wantStatus: RegionalAudiobookCreated},
		{name: "mapping after status expired", withMapping: true, wantStatus: RegionalAudiobookLoaded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var importReads, editionReads, mutations int
			client := unanchoredImportTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(request.Query, "RegionalAudibleImport"):
					importReads++
					require.Equal(t, "B0OTHER123:ca", request.Variables["externalId"])
					statuses := []map[string]interface{}{}
					if tc.withStatus {
						bookID, editionID := interface{}(73), interface{}(900)
						if tc.missingStatusIDs {
							bookID, editionID = nil, nil
						}
						statuses = append(statuses, map[string]interface{}{
							"status": tc.wantStatus, "book_id": bookID, "edition_id": editionID,
							"external_id": "B0OTHER123:ca", "platform_id": 32,
						})
					}
					mappings := []map[string]interface{}{}
					if tc.withMapping {
						mappings = append(mappings, map[string]interface{}{
							"id": 77, "state": "normalized", "book_id": 73,
							"platform_id": 32, "external_id": "B0OTHER123:ca", "edition_id": 900,
							"edition": map[string]interface{}{"id": 900, "book_id": 73, "reading_format_id": 2},
						})
					}
					payload, err := json.Marshal(map[string]interface{}{"data": map[string]interface{}{
						"book_import_statuses": statuses, "book_mappings": mappings,
					}})
					require.NoError(t, err)
					_, _ = w.Write(payload)
				case strings.Contains(request.Query, "GetEdition"):
					editionReads++
					_, _ = w.Write([]byte(`{"data":{"editions":[{"id":900,"book_id":73,"reading_format_id":2}]}}`))
				case strings.Contains(request.Query, "mutation"):
					mutations++
					http.Error(w, "unexpected mutation", http.StatusBadRequest)
				default:
					t.Errorf("unexpected GraphQL request: %s", request.Query)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))

			result, confirmed, err := client.CheckRegionalAudiobookImport(context.Background(), RegionalAudiobookInput{
				ASIN: "b0other123", Region: "CA", Unanchored: true,
			})

			require.NoError(t, err)
			require.True(t, confirmed)
			require.Equal(t, tc.wantStatus, result.Status)
			require.Equal(t, 73, result.BookID, "the book ID is resolved from the terminal status or mapping")
			require.Equal(t, 900, result.EditionID)
			require.Equal(t, 2, result.ReadingFormatID)
			require.Equal(t, "B0OTHER123:ca", result.RegionalExternalID)
			require.Equal(t, 1, importReads)
			require.Equal(t, 1, editionReads, "recovery requires a fresh edition read")
			require.Zero(t, mutations, "recovery is read-only")
		})
	}
}

type unanchoredImportRoundTripper func(*http.Request) (*http.Response, error)

func (transport unanchoredImportRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func unanchoredImportTestClient(handler http.Handler) *Client {
	client := regionalImportTestClient("http://hardcover.test")
	client.httpClient.Transport = unanchoredImportRoundTripper(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})
	return client
}
