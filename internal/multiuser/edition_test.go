package multiuser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition/editiontest"
	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// editionItem builds an Audiobookshelf item with a hyphenated ISBN but no cover
// or ASIN, so no image upload or Audnex lookup is attempted.
func editionItem(id, title, author string) map[string]interface{} {
	return map[string]interface{}{
		"id":        id,
		"libraryId": "library",
		"mediaType": "book",
		"media": map[string]interface{}{
			"metadata": map[string]interface{}{"title": title, "authorName": author, "isbn": "978-0-306-40615-7"},
			"duration": 3600.0,
		},
	}
}

// editionItemWithCover is editionItem with a cover, so creating its edition
// downloads the cover from Audiobookshelf and uploads it to Hardcover.
type editionFixture struct {
	service   *MultiUserService
	hardcover *editiontest.HardcoverFake
	abs       *editiontest.AudiobookshelfFake
}

// newEditionFixture builds a service with one profile whose run "run-1"
// contains the given outcome records.
func newEditionFixture(t *testing.T, dryRun bool, records []syncsvc.BookOutcomeRecord, items map[string]map[string]interface{}) *editionFixture {
	t.Helper()
	service, _ := newStatusLookupService(t)
	hardcover := editiontest.NewHardcoverFake(t)
	abs := editiontest.NewAudiobookshelfFake(t, items)
	service.globalConfig.Hardcover.BaseURL = hardcover.URL
	service.globalConfig.RateLimit.Rate = time.Nanosecond
	service.globalConfig.RateLimit.MaxConcurrent = 4
	t.Cleanup(func() { require.NoError(t, service.Shutdown(context.Background())) })

	require.NoError(t, service.repository.CreateProfile(
		"profile-1", "Profile", abs.URL, "abs-token", "hc-token", database.SyncConfigData{DryRun: dryRun},
	))
	service.updateProfileStatus("profile-1", &SyncProfileStatus{
		ProfileID: "profile-1",
		Snapshot: &syncsvc.SyncSnapshot{
			ProfileID:    "profile-1",
			RunID:        "run-1",
			State:        string(syncsvc.RunPhaseCompleted),
			BookOutcomes: records,
		},
	})
	return &editionFixture{service: service, hardcover: hardcover, abs: abs}
}

func needsReview(bookID, hardcoverBookID string) syncsvc.BookOutcomeRecord {
	return syncsvc.BookOutcomeRecord{BookID: bookID, Outcome: syncsvc.OutcomeNeedsReview, HardcoverBookID: hardcoverBookID}
}

func TestEditionRequestsRequireAnEligibleRecord(t *testing.T) {
	records := []syncsvc.BookOutcomeRecord{
		needsReview("ok-item", "4242"),
		{BookID: "synced-item", Outcome: syncsvc.OutcomeSynced, HardcoverBookID: "4242"},
		needsReview("no-candidate", ""),
		needsReview("text-id", "not-a-number"),
		needsReview("zero-id", "0"),
		needsReview("negative-id", "-5"),
	}
	items := map[string]map[string]interface{}{}
	for _, r := range records {
		items[r.BookID] = editionItem(r.BookID, "A Title", "An Author")
	}

	tests := []struct {
		name      string
		profileID string
		runID     string
		bookID    string
		want      error
	}{
		{"not a needs-review outcome", "profile-1", "run-1", "synced-item", ErrEditionNotEligible},
		{"no Hardcover book recorded", "profile-1", "run-1", "no-candidate", ErrEditionNotEligible},
		{"non-numeric Hardcover book", "profile-1", "run-1", "text-id", ErrEditionNotEligible},
		{"zero Hardcover book", "profile-1", "run-1", "zero-id", ErrEditionNotEligible},
		{"negative Hardcover book", "profile-1", "run-1", "negative-id", ErrEditionNotEligible},
		{"book absent from the run", "profile-1", "run-1", "missing-item", ErrEditionNotFound},
		{"unknown run", "profile-1", "other-run", "ok-item", ErrEditionNotFound},
		{"unknown profile", "profile-x", "run-1", "ok-item", ErrProfileNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newEditionFixture(t, false, records, items)

			_, draftErr := f.service.PrepareEditionDraft(context.Background(), tt.profileID, tt.runID, tt.bookID)
			require.ErrorIs(t, draftErr, tt.want)
			require.Empty(t, f.hardcover.RecordedMutations())
		})
	}
}

func TestEditionRequests_AudiobookshelfFailures(t *testing.T) {
	f := newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{}, // Audiobookshelf no longer has the item
	)
	_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	require.ErrorIs(t, err, ErrEditionItemNotFound)

	f.abs.Status = http.StatusInternalServerError
	var upstream *EditionUpstreamError
	_, err = f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	require.ErrorAs(t, err, &upstream)
	require.Equal(t, "audiobookshelf", upstream.Service)
	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestEditionRequestsAreRejectedAfterShutdown(t *testing.T) {
	f := newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": editionItem("item-1", "A Title", "An Author")},
	)
	require.NoError(t, f.service.Shutdown(context.Background()))

	_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	require.ErrorIs(t, err, ErrServiceShuttingDown)
	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestPrepareEditionDraft_ResolvesFromTheRunRecord(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		f := newEditionFixture(t, dryRun,
			[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
			map[string]map[string]interface{}{"item-1": editionItem("item-1", "Draft Service Title", "Draft Service Author")},
		)
		f.hardcover.Authors["Draft Service Author"] = 55

		built, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
		require.NoError(t, err)
		require.Equal(t, 4242, built.HardcoverBookID)
		require.Equal(t, "Draft Service Title", built.Title)
		require.Equal(t, []int{55}, built.AuthorIDs)
		require.Equal(t, dryRun, built.DryRun)
		require.Equal(t, 3600, built.AudioSeconds)
		require.Empty(t, f.hardcover.RecordedMutations(), "previewing must never mutate Hardcover")
	}
}

func TestPrepareEditionDraft_PropagatesMetadataLookupFailures(t *testing.T) {
	tests := []struct {
		name       string
		lookup     string
		itemMutate func(map[string]interface{})
	}{
		{
			name:   "author",
			lookup: "author",
		},
		{
			name:   "narrator",
			lookup: "narrator",
			itemMutate: func(item map[string]interface{}) {
				item["media"].(map[string]interface{})["metadata"].(map[string]interface{})["narratorName"] = "Narrator Failure"
			},
		},
		{
			name:   "publisher",
			lookup: "publisher",
			itemMutate: func(item map[string]interface{}) {
				item["media"].(map[string]interface{})["metadata"].(map[string]interface{})["publisher"] = "Publisher Failure"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := editionItem("item-1", "Failure Title", "Author Failure")
			if tt.itemMutate != nil {
				tt.itemMutate(item)
			}
			f := newEditionFixture(t, false,
				[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
				map[string]map[string]interface{}{"item-1": item},
			)
			failureServer := newHardcoverLookupFailureServer(t, tt.lookup)
			t.Cleanup(failureServer.Close)
			f.service.globalConfig.Hardcover.BaseURL = failureServer.URL

			_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
			var upstream *EditionUpstreamError
			require.ErrorAs(t, err, &upstream)
			require.Equal(t, "hardcover", upstream.Service)
			require.Empty(t, f.hardcover.RecordedMutations())
		})
	}
}

func TestPrepareEditionDraft_NoMetadataMatchRemainsSuccessful(t *testing.T) {
	item := editionItem("item-1", "No Match Title", "No Match Author")
	metadata := item["media"].(map[string]interface{})["metadata"].(map[string]interface{})
	metadata["narratorName"] = "No Match Narrator"
	metadata["publisher"] = "No Match Publisher"
	f := newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": item},
	)

	built, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	require.NoError(t, err)
	require.Empty(t, built.AuthorIDs)
	require.Empty(t, built.NarratorIDs)
	require.Zero(t, built.PublisherID)
	require.NotEmpty(t, built.Warnings)
	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestPrepareEditionDraft_DoesNotRetryFailedPublisherLookup(t *testing.T) {
	item := editionItem("item-1", "Publisher Failure Title", "Publisher Failure Author")
	item["media"].(map[string]interface{})["metadata"].(map[string]interface{})["publisher"] = "Publisher Failure"
	f := newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": item},
	)
	failureServer := newPublisherFailOnceServer(t)
	t.Cleanup(failureServer.Close)
	f.service.globalConfig.Hardcover.BaseURL = failureServer.URL

	_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	var upstream *EditionUpstreamError
	require.ErrorAs(t, err, &upstream)
	require.Equal(t, "hardcover", upstream.Service)
	require.Empty(t, f.hardcover.RecordedMutations())
}

func newHardcoverLookupFailureServer(t *testing.T, lookup string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		matches := (lookup == "publisher" && strings.Contains(request.Query, "SearchPublishers")) ||
			(lookup == "author" && strings.Contains(request.Query, "SearchPeopleDirect") && !strings.Contains(request.Query, "contributions:")) ||
			(lookup == "narrator" && strings.Contains(request.Query, "SearchPeopleDirect") && strings.Contains(request.Query, "contributions:"))
		if matches {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]string{{"message": "forced lookup failure"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})
	}))
}

func newPublisherFailOnceServer(t *testing.T) *httptest.Server {
	t.Helper()
	var publisherCalls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "SearchPublishers") && publisherCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]string{{"message": "first publisher lookup failed"}}})
			return
		}
		if strings.Contains(request.Query, "SearchPublishers") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"publishers": []map[string]interface{}{{"id": 303, "name": "Publisher Failure"}},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})
	}))
}

func TestEditionRequestsRequireAnIdentifierOnTheAudiobookshelfItem(t *testing.T) {
	item := func(mutate func(meta map[string]interface{})) map[string]map[string]interface{} {
		it := editionItem("item-1", "A Title", "An Author")
		mutate(it["media"].(map[string]interface{})["metadata"].(map[string]interface{}))
		return map[string]map[string]interface{}{"item-1": it}
	}
	tests := []struct {
		name    string
		items   map[string]map[string]interface{}
		allowed bool
		noDraft bool // a draft of an item with an ASIN would query the public Audnex API
	}{
		{name: "neither ASIN nor ISBN", items: item(func(m map[string]interface{}) { delete(m, "isbn") })},
		{name: "blank ASIN and ISBN", items: item(func(m map[string]interface{}) { m["isbn"], m["asin"] = " ", "  " })},
		{name: "an ISBN that is not an ISBN", items: item(func(m map[string]interface{}) { m["isbn"] = "not-an-isbn" })},
		{name: "an ISBN only", items: item(func(m map[string]interface{}) {}), allowed: true},
		{name: "an ASIN only", items: item(func(m map[string]interface{}) { delete(m, "isbn"); m["asin"] = "B0EXISTING1" }), allowed: true, noDraft: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newEditionFixture(t, false, []syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")}, tt.items)

			var draftErr error
			if !tt.noDraft {
				_, draftErr = f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
			}

			if tt.allowed {
				require.NotErrorIs(t, draftErr, ErrEditionNoIdentifier)
				return
			}
			require.ErrorIs(t, draftErr, ErrEditionNoIdentifier)
			require.Zero(t, f.hardcover.RequestCount(), "no Hardcover request may be made for a book without an identifier")
		})
	}
}
