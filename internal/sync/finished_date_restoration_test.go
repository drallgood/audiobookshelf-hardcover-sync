package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Model the hosted API's status side effect even when last_read_date is supplied.
// Exercise durable recovery through the concrete client rather than a mutation mock.
func TestFinishedDateRestorationAcrossStatusAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		existing      bool
		closing       bool
		nextFinished  bool
	}{
		{name: "new read"},
		{name: "existing history out of date order", existing: true},
		{name: "closing older unfinished read", existing: true, closing: true},
		{name: "status failure survives restart", failure: "status"},
		{name: "repair failure survives restart", failure: "repair"},
		{name: "pending recovery closes current finished reread", failure: "repair", nextFinished: true},
		{name: "readback failure survives restart", failure: "fetch"},
		{name: "verification fetch failure survives restart", failure: "verify"},
		{name: "verification mismatch survives restart", failure: "mismatch"},
		{name: "missing read remains pending", failure: "missing"},
		{name: "state save failure prevents status", failure: "save"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := createTestService()
			path := filepath.Join(t.TempDir(), "state.json")
			svc.statePath = path
			if tc.failure == "save" {
				svc.statePath = filepath.Join(path, "child")
				require.NoError(t, svc.state.Save(path))
			}
			book := convertTestBookToModel(createTestFinishedBook("restoration", "Title", "Author", "ASIN", ""))
			book.Progress.FinishedAt = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
			dates := map[int64]string{}
			if tc.existing {
				dates[99] = "2024-01-01"
				dates[1] = "2025-08-12"
			}
			if tc.closing {
				dates[10] = ""
			}
			status, inserts, transitions, repairs, postStatusFetches := 1, 0, 0, 0, 0
			fail := tc.failure
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				data := map[string]interface{}{}
				switch {
				case strings.Contains(req.Query, "GetUserBook("):
					data["user_books"] = []interface{}{map[string]interface{}{"id": 789, "book_id": 123, "status_id": status}}
				case strings.Contains(req.Query, "GetUserBookReads"):
					if transitions > 0 {
						postStatusFetches++
						if fail == "fetch" || (fail == "verify" && postStatusFetches == 2) {
							_, _ = w.Write([]byte(`{"errors":[{"message":"readback unavailable"}]}`))
							return
						}
					}
					rows := []interface{}{}
					for id, date := range dates {
						if fail == "missing" && transitions > 0 && id == 100 {
							continue
						}
						rows = append(rows, map[string]interface{}{"id": id, "finished_at": date, "progress_seconds": 3600})
					}
					data["user_book_reads"] = rows
				case strings.Contains(req.Query, "InsertUserBookRead"):
					inserts++
					dates[100] = req.Variables["user_book_read"].(map[string]interface{})["finished_at"].(string)
					data["insert_user_book_read"] = map[string]interface{}{"id": 100}
				case strings.Contains(req.Query, "UpdateUserBookStatus"):
					persisted, err := state.LoadState(path)
					require.NoError(t, err)
					_, ok := persisted.GetFinishedDateRestoration("789")
					require.True(t, ok, "intent must be durable before the server can overwrite dates")
					if fail == "status" {
						_, _ = w.Write([]byte(`{"errors":[{"message":"status unavailable"}]}`))
						return
					}
					transitions++
					status = 3
					for id := range dates {
						dates[id] = "2026-10-02"
					}
					// An unrelated read arriving after our snapshot must remain untouched.
					dates[777] = "2026-09-01"
					data["update_user_book"] = map[string]interface{}{"id": 789}
				case strings.Contains(req.Query, "UpdateUserBookRead"):
					repairs++
					if fail == "repair" && transitions > 0 {
						_, _ = w.Write([]byte(`{"errors":[{"message":"repair unavailable"}]}`))
						return
					}
					if fail != "mismatch" {
						dates[int64(req.Variables["id"].(float64))] = req.Variables["object"].(map[string]interface{})["finished_at"].(string)
					}
					data["update_user_book_read"] = map[string]interface{}{"id": int(req.Variables["id"].(float64))}
				default:
					t.Errorf("unexpected operation %s", req.Query)
				}
				require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{"data": data}))
			}))
			defer server.Close()
			cfg := hardcover.DefaultClientConfig()
			cfg.BaseURL = server.URL
			cfg.RateLimit = time.Nanosecond
			cfg.MaxRetries = 0
			svc.hardcover = hardcover.NewClientWithConfig(cfg, "test-token", logger.Get())
			err := svc.HandleFinishedBook(context.Background(), book, "456", 789)
			if tc.failure == "save" {
				require.Error(t, err)
				assert.Zero(t, transitions)
				_, ok := svc.state.GetBookState("restoration:456")
				assert.False(t, ok)
				return
			}
			if tc.failure != "" {
				require.Error(t, err)
				_, ok := svc.state.GetBookState("restoration:456")
				assert.False(t, ok)
				persisted, err := state.LoadState(path)
				require.NoError(t, err)
				intended, ok := persisted.GetFinishedDateRestoration("789")
				require.True(t, ok)
				assert.Equal(t, "2025-06-01", intended[100])
				// A dry run neither repairs nor discards the durable recovery intent.
				dry, _ := createTestService()
				dry.state = persisted
				dry.statePath = path
				dry.hardcover = svc.hardcover
				dry.config.Sync.DryRun = true
				fail = ""
				before := repairs
				require.NoError(t, dry.HandleFinishedBook(context.Background(), book, "456", 789))
				assert.Equal(t, before, repairs)
				assert.True(t, persisted.HasFinishedDateRestoration(book.ID))
				resumed, _ := createTestService()
				resumed.state = persisted
				resumed.statePath = path
				resumed.hardcover = svc.hardcover
				if tc.nextFinished {
					dates[101] = ""
					book.Progress.FinishedAt = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC).UnixMilli()
				}
				require.NoError(t, resumed.HandleFinishedBook(context.Background(), book, "456", 789))
				require.NoError(t, resumed.state.Save(path))
				loaded, err := state.LoadState(path)
				require.NoError(t, err)
				assert.False(t, loaded.HasFinishedDateRestoration(book.ID))
				checkpoint, ok := loaded.GetBookState("restoration:456")
				require.True(t, ok)
				assert.Equal(t, "FINISHED", checkpoint.Status)
			} else {
				require.NoError(t, err)
			}
			if tc.existing {
				assert.Equal(t, "2024-01-01", dates[99])
				assert.Equal(t, "2025-08-12", dates[1])
				assert.Zero(t, inserts)
				if tc.closing {
					assert.Equal(t, "2025-06-01", dates[10])
				}
			} else {
				assert.Equal(t, "2025-06-01", dates[100])
				assert.Equal(t, 1, inserts)
			}
			if tc.nextFinished {
				assert.Equal(t, 2, transitions)
				assert.Equal(t, "2026-10-03", dates[101])
			} else {
				assert.Equal(t, 1, transitions)
			}
			assert.Equal(t, "2026-09-01", dates[777])
		})
	}
}

func TestPendingFinishedDatesRecoverBeforeCurrentTargetGuards(t *testing.T) {
	for _, target := range []string{"missing completion date", "unread progress reset"} {
		t.Run(target, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = false
			svc.config.Sync.ProcessUnreadBooks = false
			book := convertTestBookToModel(createTestFinishedBook("pending-guard", "Title", "Author", "ASIN", ""))
			book.Progress.FinishedAt = 0
			if target == "unread progress reset" {
				book.Progress.IsFinished = false
				book.Progress.CurrentTime = 0
			}
			svc.state.SetFinishedDateRestoration("789", book.ID, map[int64]string{100: "2025-06-01"})
			svc.statePath = filepath.Join(t.TempDir(), "state.json")
			require.NoError(t, svc.state.Save(svc.statePath))
			loaded, err := state.LoadState(svc.statePath)
			require.NoError(t, err)
			svc.state = loaded
			reads := []hardcover.UserBookRead{{ID: 100, FinishedAt: stringPointer("2026-10-02")}}
			hc.On("GetUserBook", mock.Anything, "789").Return(&models.HardcoverBook{BookStatusID: 3}, nil).Once()
			hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 789}).Return(reads, nil).Twice()
			hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
				return input.ID == 100 && input.Object["finished_at"] == "2025-06-01"
			})).Run(func(mock.Arguments) { reads[0].FinishedAt = stringPointer("2025-06-01") }).Return(true, nil).Once()
			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			assert.False(t, svc.state.HasFinishedDateRestoration(book.ID))
			assert.Equal(t, "2025-06-01", *reads[0].FinishedAt)
			hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
			assertNoHardcoverBookSearches(t, hc)
			hc.AssertExpectations(t)
		})
	}
}

func TestPendingFinishedDatesRecoverThenProcessReread(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.Incremental = true
	book := inProgressBook("pending-reread")
	book.Progress.StartedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	svc.statePath = filepath.Join(t.TempDir(), "state.json")
	svc.state.SetFinishedDateRestoration("300", book.ID, map[int64]string{400: "2025-06-01"})
	require.NoError(t, svc.state.Save(svc.statePath))
	loaded, err := state.LoadState(svc.statePath)
	require.NoError(t, err)
	svc.state = loaded
	editionID := int64(200)
	reads := []hardcover.UserBookRead{{ID: 400, EditionID: &editionID, FinishedAt: stringPointer("2026-10-02"), ProgressSeconds: intPointer(1000)}}
	remote := &models.HardcoverBook{ID: "100", EditionID: "200", BookStatusID: 3}
	hc.On("GetUserBook", mock.Anything, "300").Return(remote, nil)
	getReads := hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300})
	getReads.Run(func(mock.Arguments) { getReads.ReturnArguments[0] = reads }).Return(reads, nil)
	hc.On("UpdateUserBookRead", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		input := args[1].(hardcover.UpdateUserBookReadInput)
		for i := range reads {
			if reads[i].ID == input.ID {
				if date, ok := input.Object["finished_at"].(string); ok {
					reads[i].FinishedAt = stringPointer(date)
				}
			}
		}
	}).Return(true, nil)
	hc.On("InsertUserBookRead", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		input := args[1].(hardcover.InsertUserBookReadInput)
		require.Nil(t, input.DatesRead.FinishedAt, "current reread must begin unfinished")
		reads = append([]hardcover.UserBookRead{{ID: 401, EditionID: &editionID, StartedAt: input.DatesRead.StartedAt, ProgressSeconds: input.DatesRead.ProgressSeconds}}, reads...)
	}).Return(401, nil).Once()
	hc.On("UpdateUserBookStatus", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		input := args[1].(hardcover.UpdateUserBookStatusInput)
		if input.Status == "FINISHED" {
			remote.BookStatusID = 3
			for i := range reads {
				reads[i].FinishedAt = stringPointer("2026-10-02")
			}
		} else {
			require.Equal(t, 2, input.StatusID)
			remote.BookStatusID = 2
		}
	}).Return(nil).Twice()
	expectASINMatch(hc, book.Media.Metadata.ASIN, "100", "200", 300)
	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))
	require.Len(t, reads, 2)
	assert.Nil(t, reads[0].FinishedAt)
	assert.Equal(t, "2025-06-01", *reads[1].FinishedAt)
	assert.False(t, svc.state.HasFinishedDateRestoration(book.ID))
	assert.Equal(t, 2, remote.BookStatusID)
	require.NoError(t, svc.state.Save(svc.statePath))
	later, _ := createTestService()
	later.config.Sync.SyncOwned = false
	later.config.Sync.Incremental = true
	later.hardcover = hc
	later.statePath = svc.statePath
	later.state, err = state.LoadState(svc.statePath)
	require.NoError(t, err)
	book.Progress.IsFinished = true
	book.Progress.CurrentTime = 1000
	book.Progress.FinishedAt = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC).UnixMilli()
	expectASINMatch(hc, book.Media.Metadata.ASIN, "100", "200", 300)
	require.NoError(t, later.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))
	assert.Equal(t, "2026-10-03", *reads[0].FinishedAt)
	assert.Equal(t, "2025-06-01", *reads[1].FinishedAt)
	checkpoint, ok := later.state.GetBookState(book.ID + ":200")
	require.True(t, ok)
	assert.Equal(t, "FINISHED", checkpoint.Status)
	hc.AssertExpectations(t)
}
