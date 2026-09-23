package multiuser

import (
	"context"
	"io"
	"net/http"
	"strings"
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

type editionFixture struct {
	service   *MultiUserService
	hardcover *editiontest.HardcoverRequestCounter
	abs       *editiontest.AudiobookshelfFake
}

type editionRoundTripFunc func(*http.Request) (*http.Response, error)

func (f editionRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func stubAudnexTransport(t *testing.T, roundTrip func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = editionRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.audnex.us" {
			return roundTrip(request)
		}
		return previous.RoundTrip(request)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
}

// newEditionFixture builds a service with one profile whose run "run-1"
// contains the given outcome records.
func newEditionFixture(t *testing.T, dryRun bool, records []syncsvc.BookOutcomeRecord, items map[string]map[string]interface{}) *editionFixture {
	t.Helper()
	service, _ := newStatusLookupService(t)
	hardcover := editiontest.NewHardcoverRequestCounter(t)
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
			require.Zero(t, f.hardcover.RequestCount())
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
	require.Zero(t, f.hardcover.RequestCount())
}

func TestEditionRequestsAreRejectedAfterShutdown(t *testing.T) {
	f := newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": editionItem("item-1", "A Title", "An Author")},
	)
	require.NoError(t, f.service.Shutdown(context.Background()))

	_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	require.ErrorIs(t, err, ErrServiceShuttingDown)
	require.Zero(t, f.hardcover.RequestCount())
}

func TestPrepareEditionDraftUsesABSNamesWithoutHardcoverRequests(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		f := newEditionFixture(t, dryRun,
			[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
			map[string]map[string]interface{}{"item-1": editionItem("item-1", "Draft Service Title", "Draft Service Author")},
		)
		built, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
		require.NoError(t, err)
		require.Equal(t, 4242, built.HardcoverBookID)
		require.Equal(t, "Draft Service Title", built.Title)
		require.Equal(t, "Draft Service Author", built.AuthorNames)
		require.Equal(t, dryRun, built.DryRun)
		require.Equal(t, 3600, built.AudioSeconds)
		require.Zero(t, f.hardcover.RequestCount(), "previewing must not send any Hardcover request")
	}
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
	}{
		{name: "neither ASIN nor ISBN", items: item(func(m map[string]interface{}) { delete(m, "isbn") })},
		{name: "blank ASIN and ISBN", items: item(func(m map[string]interface{}) { m["isbn"], m["asin"] = " ", "  " })},
		{name: "an ISBN that is not an ISBN", items: item(func(m map[string]interface{}) { m["isbn"] = "not-an-isbn" })},
		{name: "an ISBN only", items: item(func(m map[string]interface{}) {}), allowed: true},
		{name: "an ASIN only", items: item(func(m map[string]interface{}) { delete(m, "isbn"); m["asin"] = "B0EXISTING1" }), allowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "an ASIN only" {
				stubAudnexTransport(t, func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"releaseDate":"2024-04-05"}`)),
						Request:    request,
					}, nil
				})
			}
			f := newEditionFixture(t, false, []syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")}, tt.items)

			built, draftErr := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")

			if tt.allowed {
				require.NoError(t, draftErr)
				if tt.name == "an ASIN only" {
					require.Equal(t, "B0EXISTING1", built.ASIN)
				}
				require.Zero(t, f.hardcover.RequestCount(), "previewing must not send any Hardcover request")
				return
			}
			require.ErrorIs(t, draftErr, ErrEditionNoIdentifier)
			require.Zero(t, f.hardcover.RequestCount(), "no Hardcover request may be made for a book without an identifier")
		})
	}
}
