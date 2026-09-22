package multiuser

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

func newHeldCreateFixture(t *testing.T) *editionFixture {
	t.Helper()
	return newEditionFixture(t, false,
		[]syncsvc.BookOutcomeRecord{needsReview("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": editionItem("item-1", "A Title", "An Author")},
	)
}

func TestInFlightEditionDraftDoesNotDelayShutdown(t *testing.T) {
	f := newHeldCreateFixture(t)
	holdReads := make(chan struct{})
	f.hardcover.HoldReads(holdReads)
	t.Cleanup(f.hardcover.ReleaseHolds)

	draftDone := make(chan error, 1)
	go func() {
		_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
		draftDone <- err
	}()
	select {
	case <-f.hardcover.ReadEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the draft never reached Hardcover")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	require.NoError(t, f.service.Shutdown(shutdownCtx), "an in-flight draft must not block shutdown")
	require.Less(t, time.Since(started), 2*time.Second)

	_, err := f.service.PrepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1")
	require.ErrorIs(t, err, ErrServiceShuttingDown)

	close(holdReads)
	f.hardcover.HoldReads(nil)
	select {
	case <-draftDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the draft did not finish after release")
	}
}

func TestPrepareEditionDraftStopsOnCallerCancellation(t *testing.T) {
	f := newHeldCreateFixture(t)
	holdReads := make(chan struct{})
	f.hardcover.HoldReads(holdReads)
	t.Cleanup(f.hardcover.ReleaseHolds)

	ctx, cancel := context.WithCancel(context.Background())
	draftDone := make(chan error, 1)
	go func() {
		_, err := f.service.PrepareEditionDraft(ctx, "profile-1", "run-1", "item-1")
		draftDone <- err
	}()

	select {
	case <-f.hardcover.ReadEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the draft never reached Hardcover")
	}
	cancel()

	select {
	case err := <-draftDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("the draft did not stop after its caller canceled")
	}
}

func TestPrepareEditionDraftOverallTimeoutStopsBlockedHardcoverRead(t *testing.T) {
	f := newHeldCreateFixture(t)
	holdReads := make(chan struct{})
	f.hardcover.HoldReads(holdReads)
	t.Cleanup(f.hardcover.ReleaseHolds)

	const timeout = 50 * time.Millisecond
	draftDone := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := f.service.prepareEditionDraft(context.Background(), "profile-1", "run-1", "item-1", timeout)
		draftDone <- err
	}()

	select {
	case <-f.hardcover.ReadEntered:
	case <-time.After(time.Second):
		t.Fatal("the draft never reached Hardcover")
	}

	select {
	case err := <-draftDone:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		var upstream *EditionUpstreamError
		require.ErrorAs(t, err, &upstream)
		require.Equal(t, "hardcover", upstream.Service)
		require.Less(t, time.Since(started), time.Second, "the overall draft timeout must cancel a blocked lookup")
	case <-time.After(time.Second):
		t.Fatal("the draft did not stop at its overall timeout")
	}
}
