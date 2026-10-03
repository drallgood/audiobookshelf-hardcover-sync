package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
		unfinished    bool
	}{
		{name: "new read"},
		{name: "existing history out of date order", existing: true},
		{name: "closing older unfinished read", existing: true, closing: true},
		{name: "preserve other unfinished history", existing: true, closing: true, unfinished: true},
		{name: "status failure survives restart", failure: "status"},
		{name: "repair failure survives restart", failure: "repair"},
		{name: "pending recovery closes current finished reread", failure: "repair", nextFinished: true},
		{name: "readback failure survives restart", failure: "fetch"},
		{name: "verification fetch failure survives restart", failure: "verify"},
		{name: "verification mismatch survives restart", failure: "mismatch"},
		{name: "transient missing read recovered", failure: "transient"},
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
			if tc.unfinished {
				dates[11] = ""
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
						if fail == "transient" && postStatusFetches == 1 && id == 100 {
							continue
						}
						var finishedAt interface{}
						if date != "" {
							finishedAt = date
						}
						row := map[string]interface{}{"id": id, "finished_at": finishedAt, "progress_seconds": 3600}
						if id == 11 {
							row["progress_seconds"] = nil
							row["started_at"] = "2023-01-01"
						}
						rows = append(rows, row)
					}
					// Hardcover returns newest reads first; close 10, preserving 11.
					if tc.unfinished {
						for i, row := range rows {
							if row.(map[string]interface{})["id"] == int64(10) {
								rows[0], rows[i] = rows[i], rows[0]
								break
							}
						}
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
					if tc.unfinished && int64(req.Variables["id"].(float64)) == 11 {
						require.Equal(t, map[string]interface{}{"finished_at": nil}, req.Variables["object"], "restoration must preserve the unfinished read's start and progress")
					}
					if fail != "mismatch" {
						date, _ := req.Variables["object"].(map[string]interface{})["finished_at"].(string)
						dates[int64(req.Variables["id"].(float64))] = date
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
			if tc.failure != "" && tc.failure != "transient" {
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
			if tc.unfinished {
				assert.Equal(t, "", dates[11], "unselected blank history must stay unfinished and survive cleanup")
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

func TestDryRunPendingFinishedDatesPreviewBeforeCurrentTargetGuards(t *testing.T) {
	for _, target := range []string{"missing completion date", "unread progress reset"} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pending=%t", target, pending), func(t *testing.T) {
				svc, hc := createTestService()
				svc.config.Sync.DryRun = true
				svc.config.Sync.ProcessUnreadBooks = false
				book := convertTestBookToModel(createTestFinishedBook("dry-pending-guard", "Title", "Author", "ASIN", ""))
				book.Progress.FinishedAt = 0
				if target == "unread progress reset" {
					book.Progress.IsFinished = false
					book.Progress.CurrentTime = 0
				}
				if pending {
					svc.state.SetFinishedDateRestoration("789", book.ID, map[int64]string{100: "2025-06-01"})
				}
				svc.statePath = filepath.Join(t.TempDir(), "state.json")
				require.NoError(t, svc.state.Save(svc.statePath))
				before, err := os.ReadFile(svc.statePath)
				require.NoError(t, err)
				beforeState, err := json.Marshal(svc.state)
				require.NoError(t, err)

				require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
				expectedOutcome := OutcomeSkipped
				if pending {
					expectedOutcome = OutcomeWouldSync
				}
				assert.Equal(t, expectedOutcome, recordedOutcome(svc, book.ID).Outcome)
				after, err := os.ReadFile(svc.statePath)
				require.NoError(t, err)
				assert.Equal(t, before, after, "dry run must preserve persisted recovery intent and checkpoints")
				afterState, err := json.Marshal(svc.state)
				require.NoError(t, err)
				assert.Equal(t, beforeState, afterState, "dry run must preserve in-memory recovery intent and checkpoints")
				assert.Empty(t, hc.Calls, "current-target guards must still prevent Hardcover calls")
			})
		}
	}
}

func TestPendingFinishedDatesRecoverThenProcessReread(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.Incremental = true
	svc.config.Sync.PreserveDNF = true
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
	hc.On("GetUserBook", mock.Anything, "300").Return(&models.HardcoverBook{
		ID: "100", EditionID: "200", BookStatusID: 5,
	}, nil).Once()
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
	// A DNF found while repairing an older read must defer this item's matching
	// until a later attempt observes that the user book is no longer DNF.
	beforeDNFAttempt, err := os.ReadFile(svc.statePath)
	require.NoError(t, err)
	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))
	assert.Equal(t, OutcomeSkipped, recordedOutcome(svc, book.ID).Outcome)
	assert.True(t, svc.state.HasFinishedDateRestoration(book.ID))
	_, hasCheckpoint := svc.state.GetBookState(book.ID + ":200")
	assert.False(t, hasCheckpoint, "a preserved DNF must not checkpoint the current target")
	afterDNFAttempt, err := os.ReadFile(svc.statePath)
	require.NoError(t, err)
	assert.Equal(t, beforeDNFAttempt, afterDNFAttempt, "a DNF retry must leave persisted intent and checkpoints unchanged")
	assertNoHardcoverBookSearches(t, hc)
	hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)

	// The next attempt fetches the current Hardcover status again. Once DNF is
	// removed, it repairs the old read and processes the current reread target.
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

func TestHandleFinishedBookStopsWhenRecoveryFindsPreservedDNF(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.PreserveDNF = true
	book := convertTestBookToModel(createTestFinishedBook("pending-direct-dnf", "Title", "Author", "ASIN", ""))
	svc.state.SetFinishedDateRestoration("789", book.ID, map[int64]string{100: "2025-06-01"})
	hc.On("GetUserBook", mock.Anything, "789").Return(&models.HardcoverBook{
		ID: "100", EditionID: "200", BookStatusID: 3,
	}, nil).Once()
	hc.On("GetUserBook", mock.Anything, "789").Return(&models.HardcoverBook{
		ID: "100", EditionID: "200", BookStatusID: 5,
	}, nil).Once()

	require.NoError(t, svc.HandleFinishedBook(context.Background(), book, "200", 789))
	assert.True(t, svc.state.HasFinishedDateRestoration(book.ID))
	_, hasCheckpoint := svc.state.GetBookState(book.ID + ":200")
	assert.False(t, hasCheckpoint, "a DNF found during recovery must not checkpoint the current target")
	hc.AssertNotCalled(t, "GetUserBookReads", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	hc.AssertExpectations(t)
}

func TestFinishedDateRestorationCanonicalReadback(t *testing.T) {
	for _, tc := range []struct {
		name, intent, actual, want string
		invalid                    bool
	}{
		{name: "equivalent date timestamp", intent: "2025-06-01", actual: "2025-06-01T23:59:59.123456789-04:00", want: "2025-06-01"},
		{name: "legacy timestamp", intent: "2025-06-01T23:59:59-04:00", actual: "2026-10-02", want: "2025-06-01"},
		{name: "unfinished", intent: "", actual: "2026-10-02"},
		{name: "invalid persisted", intent: "2025-99-01", invalid: true},
		{name: "invalid remote", intent: "2025-06-01", actual: "not a date", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.state.SetFinishedDateRestoration("789", "item", map[int64]string{100: tc.intent})
			hc.On("GetUserBook", mock.Anything, "789").Return(&models.HardcoverBook{BookStatusID: 3}, nil).Once()
			reads := []hardcover.UserBookRead{{ID: 100, FinishedAt: stringPointer(tc.actual)}}
			fetches := 1
			if !tc.invalid && tc.actual == "2026-10-02" {
				fetches = 2
			}
			if tc.name != "invalid persisted" {
				hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 789}).Return(reads, nil).Times(fetches)
			}
			if fetches == 2 {
				hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
					if tc.want == "" {
						value, present := input.Object["finished_at"]
						return present && value == nil
					}
					return input.Object["finished_at"] == tc.want
				})).Run(func(mock.Arguments) {
					if tc.want == "" {
						reads[0].FinishedAt = nil
					} else {
						reads[0].FinishedAt = stringPointer(tc.want)
					}
				}).Return(true, nil).Once()
			}
			_, err := svc.recoverFinishedReadDates(context.Background(), 789, map[int64]string{100: tc.intent})
			if tc.invalid {
				require.Error(t, err)
				assert.True(t, svc.state.HasFinishedDateRestoration("item"))
			} else {
				require.NoError(t, err)
				assert.False(t, svc.state.HasFinishedDateRestoration("item"))
			}
			hc.AssertExpectations(t)
		})
	}
}

func TestFinishedDateRestorationDeletedReads(t *testing.T) {
	for _, confirmationFails := range []bool{false, true} {
		t.Run(fmt.Sprint(confirmationFails), func(t *testing.T) {
			svc, hc := createTestService()
			dates := map[int64]string{100: "2025-06-01", 101: "2024-01-01"}
			svc.state.SetFinishedDateRestoration("789", "item", dates)
			hc.On("GetUserBook", mock.Anything, "789").Return(&models.HardcoverBook{BookStatusID: 3}, nil).Once()
			reads := []hardcover.UserBookRead{{ID: 100, FinishedAt: stringPointer("2026-10-02")}}
			hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 789}).Return(reads, nil).Once()
			if confirmationFails {
				hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 789}).Return(nil, fmt.Errorf("unavailable")).Once()
			} else {
				hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 789}).Return(reads, nil).Twice()
				hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool { return input.ID == 100 })).Run(func(mock.Arguments) { reads[0].FinishedAt = stringPointer("2025-06-01") }).Return(true, nil).Once()
			}
			_, err := svc.recoverFinishedReadDates(context.Background(), 789, dates)
			if confirmationFails {
				require.Error(t, err)
				assert.True(t, svc.state.HasFinishedDateRestoration("item"))
			} else {
				require.NoError(t, err)
				assert.False(t, svc.state.HasFinishedDateRestoration("item"))
			}
			hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
			hc.AssertExpectations(t)
		})
	}
}

func TestFinishedDateRestorationDeletedUserBook(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprint(direct), func(t *testing.T) {
			svc, hc := createTestService()
			ctx := withOperationUserBookSnapshots(context.Background())
			book := convertTestBookToModel(createTestFinishedBook("deleted", "Title", "Author", "ASIN", ""))
			svc.state.SetFinishedDateRestoration("789", book.ID, map[int64]string{100: "2025-06-01"})
			svc.state.SetFinishedDateRestoration("790", "other", map[int64]string{101: "2025-06-01"})
			cached := &models.HardcoverBook{ID: "123", BookStatusID: 3}
			setOperationUserBookSnapshot(ctx, 789, cached)
			svc.userBookCache.SetByUserBook(789, cached)
			hc.On("GetUserBook", mock.Anything, "789").Return(nil, fmt.Errorf("lookup: %w", hardcover.ErrUserBookNotFound)).Once()
			hc.On("ClearUserBookCache").Return().Once()
			if direct {
				require.ErrorIs(t, svc.HandleFinishedBook(ctx, book, "456", 789), hardcover.ErrUserBookNotFound)
			} else {
				_, err := svc.recoverFinishedReadDates(ctx, 789, map[int64]string{100: "2025-06-01"})
				require.NoError(t, err)
			}
			assert.False(t, svc.state.HasFinishedDateRestoration(book.ID))
			assert.True(t, svc.state.HasFinishedDateRestoration("other"))
			_, found := svc.getUserBookSnapshot(ctx, 789)
			assert.False(t, found)
			hc.AssertExpectations(t)
		})
	}
}

func TestDeletedPendingUserBookAllowsReplacementMatching(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.Incremental = true
	book := *inProgressBook("replacement")
	book.Progress.IsFinished = true
	book.Progress.CurrentTime = 1000
	book.Progress.FinishedAt = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	svc.statePath = filepath.Join(t.TempDir(), "state.json")
	svc.state.SetFinishedDateRestoration("789", book.ID, map[int64]string{100: "2025-06-01"})
	ctx := withOperationUserBookSnapshots(context.Background())
	setOperationUserBookSnapshot(ctx, 789, &models.HardcoverBook{ID: "100", EditionID: "200", BookStatusID: 3})
	hc.On("GetUserBook", mock.Anything, "789").Return(nil, hardcover.ErrUserBookNotFound).Once()
	hc.On("ClearUserBookCache").Return().Once()
	expectASINMatch(hc, book.Media.Metadata.ASIN, "100", "200", 300)
	hc.On("GetUserBook", mock.Anything, "300").Return(&models.HardcoverBook{ID: "100", EditionID: "200", BookStatusID: 3}, nil).Once()
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).Return([]hardcover.UserBookRead{{ID: 400, FinishedAt: stringPointer("2025-06-01")}}, nil).Once()
	require.NoError(t, svc.processBook(ctx, book, &models.AudiobookshelfUserProgress{}))
	assert.False(t, svc.state.HasFinishedDateRestoration(book.ID))
	checkpoint, found := svc.state.GetBookState(book.ID + ":200")
	require.True(t, found)
	assert.Equal(t, "300", checkpoint.UserBookID)
	hc.AssertExpectations(t)
}
