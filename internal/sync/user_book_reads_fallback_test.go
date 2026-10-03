package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A hosted API that rejects the nested reads relation must still sync through
// the established separate queries, including after an edition correction.
func TestProcessBookFallsBackWhenCombinedReadsQueryIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name               string
		finished, dryRun   bool
		correctEdition     bool
		cancelCombined     bool
		failure            string
		wantOutcome        SyncOutcome
		wantProgressWrites int32
	}{
		{name: "in progress", wantOutcome: OutcomeSynced, wantProgressWrites: 1},
		{name: "finished", finished: true, wantOutcome: OutcomeAlreadyCurrent},
		{name: "dry run in progress", dryRun: true, wantOutcome: OutcomeWouldSync},
		{name: "dry run finished", finished: true, dryRun: true, wantOutcome: OutcomeAlreadyCurrent},
		{name: "edition correction", correctEdition: true, wantOutcome: OutcomeSynced, wantProgressWrites: 1},
		{name: "dry run edition correction", correctEdition: true, dryRun: true, wantOutcome: OutcomeWouldSync},
		{name: "plain user book query fails", failure: "book", wantOutcome: OutcomeFailed},
		{name: "separate reads query fails", failure: "reads", wantOutcome: OutcomeFailed},
		{name: "canceled combined query", cancelCombined: true, failure: "canceled", wantOutcome: OutcomeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var combinedQueries, plainQueries, readQueries, progressWrites, editionWrites atomic.Int32
			currentEdition := atomic.Int64{}
			currentEdition.Store(200)
			if tc.correctEdition {
				currentEdition.Store(201)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				data := map[string]interface{}{}
				switch {
				case strings.Contains(req.Query, "GetUserBookWithReads("):
					combinedQueries.Add(1)
					if tc.cancelCombined {
						cancel()
					}
					_, _ = w.Write([]byte(`{"errors":[{"message":"field user_book_reads not found in type user_books","extensions":{"code":"validation-failed"}}]}`))
					return
				case strings.Contains(req.Query, "GetUserBook("):
					plainQueries.Add(1)
					if tc.failure == "book" {
						_, _ = w.Write([]byte(`{"errors":[{"message":"plain lookup unavailable"}]}`))
						return
					}
					status := 2
					if tc.finished {
						status = 3
					}
					data["user_books"] = []interface{}{map[string]interface{}{
						"id": 300, "book_id": 100, "status_id": status,
						"book":       map[string]interface{}{"id": 100, "title": "Fallback"},
						"edition_id": currentEdition.Load(), "edition": map[string]interface{}{"id": currentEdition.Load()},
					}}
				case strings.Contains(req.Query, "GetEdition("):
					data["editions"] = []interface{}{map[string]interface{}{"id": 200, "book_id": 100, "asin": "B0ONEREQ01"}}
				case strings.Contains(req.Query, "GetUserBookReadsAll("):
					readQueries.Add(1)
					if tc.failure == "reads" {
						_, _ = w.Write([]byte(`{"errors":[{"message":"reads unavailable"}]}`))
						return
					}
					var finishedAt interface{}
					if tc.finished {
						finishedAt = "2025-02-03"
					}
					data["user_book_reads"] = []interface{}{map[string]interface{}{
						"id": 400, "user_book_id": 300, "edition_id": currentEdition.Load(),
						"progress_seconds": 100, "started_at": "2025-01-01", "finished_at": finishedAt,
					}}
				case strings.Contains(req.Query, "UpdateUserBookEdition("):
					editionWrites.Add(1)
					currentEdition.Store(200)
					data["update_user_book"] = map[string]interface{}{"id": 300, "error": nil}
				case strings.Contains(req.Query, "UpdateUserBookRead("):
					progressWrites.Add(1)
					assert.EqualValues(t, 200, currentEdition.Load(), "reads and progress must follow the edition correction")
					assert.EqualValues(t, 400, req.Variables["id"])
					assert.EqualValues(t, 300, req.Variables["object"].(map[string]interface{})["progress_seconds"])
					data["update_user_book_read"] = map[string]interface{}{"id": 400, "error": nil}
				default:
					t.Errorf("unexpected GraphQL operation: %s", req.Query)
					http.Error(w, "unexpected operation", http.StatusBadRequest)
					return
				}
				if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": data}); err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer server.Close()

			clientConfig := hardcover.DefaultClientConfig()
			clientConfig.BaseURL = server.URL
			clientConfig.RateLimit = time.Nanosecond
			clientConfig.MaxRetries = 0
			client := hardcover.NewClientWithConfig(clientConfig, "test-token", logger.Get())
			client.SetDryRun(tc.dryRun)
			svc, _ := createTestService()
			svc.hardcover = client
			svc.config.Sync.SyncOwned = false
			svc.config.Sync.DryRun = tc.dryRun
			svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 300, nil }
			book := toAudiobookshelfBook(createTestBook("fallback", "Fallback", "Author", "B0ONEREQ01", ""))
			book.Media.Duration = 1000
			book.Progress.CurrentTime = 300
			book.Progress.IsFinished = tc.finished
			if tc.finished {
				book.Progress.FinishedAt = time.Date(2025, 2, 3, 0, 0, 0, 0, time.UTC).UnixMilli()
			}
			require.NoError(t, svc.state.SetAssociation(state.Association{
				ABSItemID: book.ID, SourceASIN: "B0ONEREQ01", HardcoverBookID: "100", HardcoverEditionID: "200",
				ReadingFormat: models.ReadingFormatAudiobook, Provenance: "edition_asin",
			}))

			err := svc.processBook(ctx, *book, &models.AudiobookshelfUserProgress{})

			if tc.failure != "" {
				require.Error(t, err)
				if tc.cancelCombined {
					assert.ErrorIs(t, err, context.Canceled)
				} else if tc.failure == "book" {
					assert.ErrorContains(t, err, "plain lookup unavailable")
				} else {
					assert.ErrorContains(t, err, "reads unavailable")
				}
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantOutcome, recordedOutcome(svc, book.ID).Outcome)
			assert.EqualValues(t, 1, combinedQueries.Load())
			if tc.cancelCombined {
				assert.Zero(t, plainQueries.Load(), "cancellation must not start a fallback")
			} else {
				assert.EqualValues(t, 1, plainQueries.Load())
			}
			if tc.failure == "book" || tc.cancelCombined {
				assert.Zero(t, readQueries.Load())
			} else {
				assert.EqualValues(t, 1, readQueries.Load())
			}
			assert.Equal(t, tc.wantProgressWrites, progressWrites.Load())
			if tc.correctEdition && !tc.dryRun {
				assert.EqualValues(t, 1, editionWrites.Load())
			} else {
				assert.Zero(t, editionWrites.Load())
			}
		})
	}
}
