package multiuser

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

func newHeldDraftFixture(t *testing.T) *editionFixture {
	t.Helper()
	return newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": editionItem("item-1", "A Title", "An Author")},
	)
}

func TestInFlightEditionDraftDoesNotDelayShutdown(t *testing.T) {
	f := newHeldDraftFixture(t)
	holdReads := make(chan struct{})
	f.abs.HoldReads(holdReads)
	t.Cleanup(f.abs.ReleaseReads)

	draftDone := make(chan error, 1)
	go func() {
		_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
		draftDone <- err
	}()
	select {
	case <-f.abs.ReadEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the draft never reached Audiobookshelf")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	require.NoError(t, f.service.Shutdown(shutdownCtx), "an in-flight draft must not block shutdown")
	require.Less(t, time.Since(started), 2*time.Second)

	_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	require.ErrorIs(t, err, ErrServiceShuttingDown)

	f.abs.ReleaseReads()
	select {
	case <-draftDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the draft did not finish after release")
	}
}

func TestPrepareEditionDraftStopsOnCallerCancellation(t *testing.T) {
	f := newHeldDraftFixture(t)
	holdReads := make(chan struct{})
	f.abs.HoldReads(holdReads)
	t.Cleanup(f.abs.ReleaseReads)

	ctx, cancel := context.WithCancel(context.Background())
	draftDone := make(chan error, 1)
	go func() {
		_, err := f.service.PrepareEditionDraft(ctx, "profile-1", "run-1", "item-1")
		draftDone <- err
	}()

	select {
	case <-f.abs.ReadEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the draft never reached Audiobookshelf")
	}
	cancel()

	select {
	case err := <-draftDone:
		require.ErrorIs(t, err, context.Canceled)
		var upstream *EditionUpstreamError
		require.False(t, errors.As(err, &upstream), "caller cancellation must not become an upstream failure")
	case <-time.After(2 * time.Second):
		t.Fatal("the draft did not stop after its caller canceled")
	}
}

func TestPrepareEditionDraftReturnsCallerDeadlineWithoutUpstreamClassification(t *testing.T) {
	f := newHeldDraftFixture(t)
	holdReads := make(chan struct{})
	f.abs.HoldReads(holdReads)
	t.Cleanup(f.abs.ReleaseReads)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.service.PrepareEditionDraft(ctx, "profile-1", "run-1", "item-1")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var upstream *EditionUpstreamError
	require.False(t, errors.As(err, &upstream), "caller deadline must not become an upstream failure")
}

func TestPrepareEditionDraftOverallTimeoutStopsBlockedAudiobookshelfRead(t *testing.T) {
	f := newHeldDraftFixture(t)
	holdReads := make(chan struct{})
	f.abs.HoldReads(holdReads)
	t.Cleanup(f.abs.ReleaseReads)

	const timeout = 50 * time.Millisecond
	draftDone := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := f.service.prepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1", timeout)
		draftDone <- err
	}()

	select {
	case <-f.abs.ReadEntered:
	case <-time.After(time.Second):
		t.Fatal("the draft never reached Audiobookshelf")
	}

	select {
	case err := <-draftDone:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		var upstream *EditionUpstreamError
		require.ErrorAs(t, err, &upstream)
		require.Equal(t, "audiobookshelf", upstream.Service)
		require.Less(t, time.Since(started), time.Second, "the overall draft timeout must cancel a blocked lookup")
	case <-time.After(time.Second):
		t.Fatal("the draft did not stop at its overall timeout")
	}
}

func TestPrepareEditionDraftFallsBackWhenAudnexExceedsItsBudget(t *testing.T) {
	item := editionItem("item-1", "A Title", "An Author")
	metadata := item["media"].(map[string]interface{})["metadata"].(map[string]interface{})
	metadata["asin"] = "B0SLOWAUDNEX1"
	metadata["publishedDate"] = "2024-06-01"
	f := newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": item},
	)
	stubAudnexTransport(t, func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	started := time.Now()
	built, err := f.service.prepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1", 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, "2024-06-01", built.ReleaseDate, "slow optional enrichment must fall back to the Audiobookshelf date")
	require.Less(t, time.Since(started), 2*time.Second, "optional enrichment must leave time before the overall deadline")
	require.Zero(t, f.hardcover.RequestCount())
}

func TestPrepareEditionDraftCallerCancellationStopsAudnexLookup(t *testing.T) {
	item := editionItem("item-1", "A Title", "An Author")
	metadata := item["media"].(map[string]interface{})["metadata"].(map[string]interface{})
	metadata["asin"] = "B0CANCELAUDNEX"
	f := newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": item},
	)
	audnexEntered := make(chan struct{})
	stubAudnexTransport(t, func(request *http.Request) (*http.Response, error) {
		close(audnexEntered)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	draftDone := make(chan error, 1)
	go func() {
		_, err := f.service.PrepareEditionDraft(ctx, "profile-1", "run-1", "item-1")
		draftDone <- err
	}()

	select {
	case <-audnexEntered:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("the draft never reached Audnex")
	}
	select {
	case err := <-draftDone:
		require.ErrorIs(t, err, context.Canceled)
		var upstream *EditionUpstreamError
		require.False(t, errors.As(err, &upstream), "caller cancellation must not become an upstream failure")
	case <-time.After(2 * time.Second):
		t.Fatal("the draft did not stop after its caller canceled")
	}
}
