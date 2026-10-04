package multiuser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/require"
)

func newEditionCreateService(t *testing.T) (*MultiUserService, string) {
	t.Helper()
	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir
	profileID := "create-profile"
	require.NoError(t, service.CreateProfile(
		profileID, "Create profile", "http://audiobookshelf", "abs-token", "hc-token",
		database.SyncConfigData{StateFile: "sync.json"},
	))
	return service, profileID
}

func testCreateAssociation(itemID string) statepkg.Association {
	return statepkg.Association{
		ABSItemID: itemID, SourceASIN: "B012345678", SourceISBN13: "9780306406157",
		HardcoverBookID: "41", HardcoverEditionID: "82", ReadingFormat: "ebook",
		Provenance: "api_ebook_inserted",
	}
}

type observedDoneContext struct {
	context.Context
	doneObserved chan struct{}
	once         sync.Once
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.doneObserved) })
	return c.Context.Done()
}

func TestCreateEditionWithAssociationPersistsOnlyAfterOperationSucceeds(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	called := false
	err := service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(profile *database.ProfileWithTokens) (statepkg.Association, error) {
		called = true
		require.Equal(t, profileID, profile.Profile.ID)
		return testCreateAssociation("item-1"), nil
	})
	require.NoError(t, err)
	require.True(t, called)

	path := service.profileSpecificStatePath(profileID, "sync.json")
	stored, err := statepkg.LoadState(path)
	require.NoError(t, err)
	association, exists := stored.GetAssociation("item-1")
	require.True(t, exists)
	require.Equal(t, testCreateAssociation("item-1"), association)
}

func TestCreateEditionWithAssociationInvalidatesExistingItemCheckpoints(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	path := service.profileSpecificStatePath(profileID, "sync.json")
	initial := statepkg.NewState()
	initial.UpdateBook("item-1", 0.4, "IN_PROGRESS")
	initial.UpdateBook("item-1:old-edition", 0.4, "IN_PROGRESS")
	initial.SetHasProgressSeconds("item-1:old-edition")
	initial.UpdateBook("item-other:edition", 0.8, "IN_PROGRESS")
	initial.SetHasProgressSeconds("item-other:edition")
	require.False(t, initial.NeedsSync("item-1", 0.4, "IN_PROGRESS", 0.001))
	require.NoError(t, initial.Save(path))

	err := service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return testCreateAssociation("item-1"), nil
	})
	require.NoError(t, err)

	stored, err := statepkg.LoadState(path)
	require.NoError(t, err)
	association, exists := stored.GetAssociation("item-1")
	require.True(t, exists)
	require.Equal(t, testCreateAssociation("item-1"), association)
	require.True(t, stored.NeedsSync("item-1", 0.4, "IN_PROGRESS", 0.001), "create must force the next incremental sync past its base checkpoint")
	require.NotContains(t, stored.Books, "item-1:old-edition")
	require.Contains(t, stored.Books, "item-other:edition")
	assertedUnrelated, exists := stored.GetBookState("item-other:edition")
	require.True(t, exists)
	require.True(t, assertedUnrelated.HasProgressSeconds)
}

func TestCreateEditionWithAssociationFailurePreservesExistingCheckpoints(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	path := service.profileSpecificStatePath(profileID, "sync.json")
	initial := statepkg.NewState()
	initial.UpdateBook("item-1", 0.4, "IN_PROGRESS")
	initial.SetHasProgressSeconds("item-1")
	initial.UpdateBook("item-1:old-edition", 0.4, "IN_PROGRESS")
	require.NoError(t, initial.Save(path))

	remoteErr := errors.New("remote create rejected")
	err := service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return statepkg.Association{}, remoteErr
	})
	require.ErrorIs(t, err, remoteErr)

	stored, err := statepkg.LoadState(path)
	require.NoError(t, err)
	_, exists := stored.GetAssociation("item-1")
	require.False(t, exists)
	require.False(t, stored.NeedsSync("item-1", 0.4, "IN_PROGRESS", 0.001))
	require.Contains(t, stored.Books, "item-1:old-edition")
}

func TestCreateEditionWithAssociationDoesNotPersistWhenOperationFails(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	remoteErr := errors.New("remote create rejected")
	err := service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return statepkg.Association{}, remoteErr
	})
	require.ErrorIs(t, err, remoteErr)
	_, err = os.Stat(service.profileSpecificStatePath(profileID, "sync.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRecoverEditionAssociationIsIdempotentOnlyForSameVerifiedIdentity(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	original := testCreateAssociation("item-1")
	original.OwnershipVerifiedAt = 1_790_000_000
	original.OwnershipTokenFingerprint = "account-fingerprint"
	require.NoError(t, service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return original, nil
	}))

	verified := original
	verified.Provenance = "api_regional_recovered"
	verified.OwnershipVerifiedAt = 0
	verified.OwnershipTokenFingerprint = ""
	called := false
	err := service.RecoverEditionAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		called = true
		return verified, nil
	})
	require.NoError(t, err)
	require.True(t, called)

	stored, err := statepkg.LoadState(service.profileSpecificStatePath(profileID, "sync.json"))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("item-1")
	require.True(t, exists)
	require.Equal(t, original, association, "recovery preserves the original association provenance")

	conflicts := []struct {
		name   string
		change func(*statepkg.Association)
	}{
		{name: "source identifier", change: func(a *statepkg.Association) { a.SourceASIN = "B999999999" }},
		{name: "Hardcover book", change: func(a *statepkg.Association) { a.HardcoverBookID = "999" }},
		{name: "Hardcover edition", change: func(a *statepkg.Association) { a.HardcoverEditionID = "999" }},
		{name: "reading format", change: func(a *statepkg.Association) { a.ReadingFormat = "audiobook" }},
	}
	for _, conflictCase := range conflicts {
		t.Run(conflictCase.name, func(t *testing.T) {
			conflict := verified
			conflictCase.change(&conflict)
			err := service.RecoverEditionAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
				return conflict, nil
			})
			require.ErrorIs(t, err, ErrEditionAssociationAlreadyExists)

			stored, err := statepkg.LoadState(service.profileSpecificStatePath(profileID, "sync.json"))
			require.NoError(t, err)
			association, exists := stored.GetAssociation("item-1")
			require.True(t, exists)
			require.Equal(t, original, association, "a conflict cannot replace a previously confirmed association")
		})
	}
}

func TestRecoverUnanchoredAudibleAssociationIsIdempotentAcrossConfirmationTimes(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	confirmedAt := time.Date(2026, time.June, 5, 14, 30, 0, 0, time.UTC)
	original := statepkg.Association{
		ABSItemID: "item-1", SourceASIN: "B012345678", SourceISBN13: "9780306406157",
		RegionalExternalID: "B0OTHER123:uk", AudnexusConfirmedRegion: "uk", AudnexusConfirmedAt: confirmedAt,
		HardcoverBookID: "73", HardcoverEditionID: "84", ReadingFormat: "audiobook",
		Provenance: "audible_import_unanchored",
	}
	require.NoError(t, service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return original, nil
	}))

	verifiedAgain := original
	verifiedAgain.AudnexusConfirmedAt = confirmedAt.Add(time.Hour)
	err := service.RecoverEditionAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return verifiedAgain, nil
	})
	require.NoError(t, err, "a repeated status verification must not conflict only because it ran later")

	stored, err := statepkg.LoadState(service.profileSpecificStatePath(profileID, "sync.json"))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("item-1")
	require.True(t, exists)
	require.Equal(t, original, association, "idempotent recovery preserves the first confirmed timestamp and provenance")
}

func TestCreateEditionWithAssociationDistinguishesLocalSaveFailureAfterRemoteSuccess(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	statePath := service.profileSpecificStatePath(profileID, "sync.json")
	called := false
	err := service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		called = true
		// Force atomic rename to fail only after state was loaded and the remote
		// operation has succeeded.
		return testCreateAssociation("item-1"), os.Mkdir(statePath, 0700)
	})
	require.True(t, called)
	require.ErrorIs(t, err, ErrEditionAssociationSaveAfterRemoteSuccess)
	var saveErr *os.LinkError
	require.ErrorAs(t, err, &saveErr)
	require.ErrorIs(t, err, saveErr.Err)
	require.Contains(t, err.Error(), ErrEditionAssociationSaveAfterRemoteSuccess.Error())
	require.Contains(t, err.Error(), "failed to replace state file")
}

func TestCreateEditionWithAssociationClassifiesInvalidAssociationAfterRemoteSuccess(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	err := service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		// A successful operation callback means the remote edition is already
		// verified; state validation failures must therefore be retry-safe too.
		return statepkg.Association{ABSItemID: "item-1"}, nil
	})
	require.ErrorIs(t, err, ErrEditionAssociationSaveAfterRemoteSuccess)
}

func TestCreateEditionWithAssociationReturnsBusyForAnotherStateLock(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	statePath := service.profileSpecificStatePath(profileID, "sync.json")
	lock, err := statepkg.AcquireFileLock(statePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, lock.Close()) }()
	called := false
	err = service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		called = true
		return testCreateAssociation("item-1"), nil
	})
	require.ErrorIs(t, err, ErrProfileStateBusy)
	require.False(t, called)
}

func TestCreateEditionWithAssociationRejectsAnActiveSyncBeforeCallback(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	service.syncMutex.Lock()
	service.activeSyncs[profileID] = func() {}
	service.syncMutex.Unlock()
	called := false
	err := service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		called = true
		return testCreateAssociation("item-1"), nil
	})
	require.ErrorIs(t, err, ErrSyncAlreadyActive)
	require.False(t, called)
	service.syncMutex.Lock()
	delete(service.activeSyncs, profileID)
	service.syncMutex.Unlock()
}

func TestCreateEditionWithAssociationRefusesDryRunProfile(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	profile, err := service.GetProfile(profileID)
	require.NoError(t, err)
	profile.SyncConfig.DryRun = true
	require.NoError(t, service.repository.UpdateUserConfig(
		profileID, profile.AudiobookshelfURL, profile.AudiobookshelfToken, profile.HardcoverToken, profile.SyncConfig,
	))
	called := false
	err = service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		called = true
		return testCreateAssociation("item-1"), nil
	})
	require.ErrorIs(t, err, ErrEditionCreateDryRun)
	require.False(t, called)
}

func TestProfileABSURLIsNormalizedAndValidatedWithGlobalTrust(t *testing.T) {
	service, _ := newEditionCreateService(t)
	profileID := "normalized-profile"
	require.NoError(t, service.CreateProfile(
		profileID, "Normalized", "http://localhost:8089/abs/", "abs-token", "hc-token", database.SyncConfigData{},
	))
	profile, err := service.GetProfile(profileID)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8089/abs", profile.AudiobookshelfURL)
	profile.SyncConfig.StateFile = ""
	require.NoError(t, service.UpdateProfileConfig(profileID, "http://localhost:8089/new/", "abs-token", "hc-token", profile.SyncConfig))
	profile, err = service.GetProfile(profileID)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8089/new", profile.AudiobookshelfURL)

	service.globalConfig.Audiobookshelf.NetworkTrust = "public_only"
	err = service.UpdateProfileConfig(profileID, "http://localhost:8089/", "abs-token", "hc-token", profile.SyncConfig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must use https")
}

func TestCreateEditionWithAssociationOperationHoldsProfileGate(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	createDone := make(chan error, 1)
	go func() {
		createDone <- service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
			close(started)
			<-finish
			return testCreateAssociation("item-1"), nil
		})
	}()
	<-started

	gate := service.profileGate(profileID)
	if gate.mu.TryLock() {
		gate.mu.Unlock()
		t.Fatal("profile run gate was not held during remote operation")
	}
	close(finish)
	require.NoError(t, <-createDone)
	require.NoError(t, service.Shutdown(context.Background()))
}

func TestCreateEditionWithAssociationStopsWaitingWhenContextEnds(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	gate := service.profileGate(profileID)
	gate.mu.Lock()
	defer gate.mu.Unlock()

	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedDoneContext{Context: baseCtx, doneObserved: make(chan struct{})}
	called := false
	done := make(chan error, 1)
	go func() {
		done <- service.CreateEditionWithAssociation(ctx, profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
			called = true
			return testCreateAssociation("item-1"), nil
		})
	}()

	select {
	case <-ctx.doneObserved:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("edition create did not reach the blocked profile gate wait")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("edition create did not stop waiting after cancellation")
	}
	require.False(t, called)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shutdownCancel()
	require.NoError(t, service.Shutdown(shutdownCtx), "canceled gate wait must release shutdown admission accounting")
}

func TestCreateEditionWithAssociationUsesProfileScopedStatePath(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	otherPath := filepath.Join(t.TempDir(), "other.json")
	require.NoError(t, os.WriteFile(otherPath, []byte(`{"version":"4.0"}`), 0600))
	require.NoError(t, service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return testCreateAssociation("item-1"), nil
	}))
	otherState, err := statepkg.LoadState(otherPath)
	require.NoError(t, err)
	_, exists := otherState.GetAssociation("item-1")
	require.False(t, exists)
}

func TestCreateEditionWithAssociationSavesToLockedTargetAfterSymlinkRetarget(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	dataDir := service.globalConfig.Paths.DataDir
	firstPath := filepath.Join(dataDir, "state-first.json")
	secondPath := filepath.Join(dataDir, "state-second.json")
	aliasPath := service.profileSpecificStatePath(profileID, "state-alias.json")
	require.NoError(t, statepkg.NewState().Save(firstPath))
	require.NoError(t, statepkg.NewState().Save(secondPath))
	if err := os.Symlink(firstPath, aliasPath); err != nil {
		t.Skipf("symlink support unavailable: %v", err)
	}
	profile, err := service.GetProfile(profileID)
	require.NoError(t, err)
	profile.SyncConfig.StateFile = "state-alias.json"
	require.NoError(t, service.UpdateProfileConfig(
		profileID, profile.AudiobookshelfURL, "", "", profile.SyncConfig,
	))

	err = service.CreateEditionWithAssociation(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		require.NoError(t, os.Remove(aliasPath))
		require.NoError(t, os.Symlink(secondPath, aliasPath))
		return testCreateAssociation("item-1"), nil
	})
	require.NoError(t, err)

	first, err := statepkg.LoadState(firstPath)
	require.NoError(t, err)
	_, foundInFirst := first.GetAssociation("item-1")
	require.True(t, foundInFirst)
	second, err := statepkg.LoadState(secondPath)
	require.NoError(t, err)
	_, foundInSecond := second.GetAssociation("item-1")
	require.False(t, foundInSecond)
}

func TestCreateEditionWithAssociationAndResyncRunsAfterSaveUnderGateAndStateLock(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	statePath := service.profileSpecificStatePath(profileID, "sync.json")
	resynced := false
	err := service.CreateEditionWithAssociationAndResync(context.Background(), profileID, "item-1",
		func(*database.ProfileWithTokens) (statepkg.Association, error) {
			return testCreateAssociation("item-1"), nil
		},
		func(profile *database.ProfileWithTokens, syncState *statepkg.State, lockedPath string) {
			resynced = true
			require.Equal(t, profileID, profile.Profile.ID)
			_, exists := syncState.GetAssociation("item-1")
			require.True(t, exists, "resync must see the just-created association")
			stored, loadErr := statepkg.LoadState(lockedPath)
			require.NoError(t, loadErr)
			_, exists = stored.GetAssociation("item-1")
			require.True(t, exists, "association must be durable before the resync starts")

			gate := service.profileGate(profileID)
			if gate.mu.TryLock() {
				gate.mu.Unlock()
				t.Error("profile run gate was not held during resync")
			}
			if lock, lockErr := statepkg.AcquireFileLock(statePath); lockErr == nil {
				_ = lock.Close()
				t.Error("state-file lock was not held during resync")
			} else {
				require.ErrorIs(t, lockErr, statepkg.ErrStateFileLocked)
			}
		})
	require.NoError(t, err)
	require.True(t, resynced)
}

func TestCreateEditionWithAssociationAndResyncSkipsResyncWhenNothingWasCreated(t *testing.T) {
	noResync := func(t *testing.T) EditionResyncOperation {
		return func(*database.ProfileWithTokens, *statepkg.State, string) {
			t.Error("resync must not run when no edition was created")
		}
	}
	create := func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return testCreateAssociation("item-1"), nil
	}

	t.Run("full sync active", func(t *testing.T) {
		service, profileID := newEditionCreateService(t)
		service.syncMutex.Lock()
		service.activeSyncs[profileID] = func() {}
		service.syncMutex.Unlock()
		err := service.CreateEditionWithAssociationAndResync(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
			t.Error("edition must not be created while a full sync is active")
			return statepkg.Association{}, nil
		}, noResync(t))
		require.ErrorIs(t, err, ErrSyncAlreadyActive)
	})

	t.Run("create fails", func(t *testing.T) {
		service, profileID := newEditionCreateService(t)
		failure := errors.New("hardcover rejected the edition")
		err := service.CreateEditionWithAssociationAndResync(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
			return statepkg.Association{}, failure
		}, noResync(t))
		require.ErrorIs(t, err, failure)
	})

	t.Run("dry run", func(t *testing.T) {
		service, profileID := newEditionCreateService(t)
		profile, err := service.GetProfile(profileID)
		require.NoError(t, err)
		profile.SyncConfig.DryRun = true
		require.NoError(t, service.repository.UpdateUserConfig(
			profileID, profile.AudiobookshelfURL, profile.AudiobookshelfToken, profile.HardcoverToken, profile.SyncConfig,
		))
		err = service.CreateEditionWithAssociationAndResync(context.Background(), profileID, "item-1", create, noResync(t))
		require.ErrorIs(t, err, ErrEditionCreateDryRun)
	})

	t.Run("association save fails", func(t *testing.T) {
		service, profileID := newEditionCreateService(t)
		err := service.CreateEditionWithAssociationAndResync(context.Background(), profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
			return testCreateAssociation("other-item"), nil
		}, noResync(t))
		require.ErrorIs(t, err, ErrEditionAssociationSaveAfterRemoteSuccess)
	})
}

func TestAnnotateEditionAdditionsRequiresMatchingSavedAPIAssociation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		recordFormat string
		recordASIN   string
		recordISBN   string
		change       func(*statepkg.Association)
		want         bool
	}{
		{name: "confirmed ebook create", recordFormat: "Ebook", change: func(*statepkg.Association) {}, want: true},
		{name: "confirmed audiobook create", recordFormat: "Audiobook", change: func(a *statepkg.Association) { a.ReadingFormat = "audiobook" }, want: true},
		{name: "normalized source identifiers", recordFormat: "Ebook", recordASIN: " b012345678 ", recordISBN: "978-0-306-40615-7", change: func(*statepkg.Association) {}, want: true},
		{name: "other book", recordFormat: "Ebook", change: func(a *statepkg.Association) { a.HardcoverBookID = "99" }, want: false},
		{name: "other source", recordFormat: "Ebook", change: func(a *statepkg.Association) { a.SourceASIN = "B099999999" }, want: false},
		{name: "changed source identifier", recordFormat: "Ebook", recordASIN: "B099999999", change: func(*statepkg.Association) {}, want: false},
		{name: "other format", recordFormat: "Ebook", change: func(a *statepkg.Association) { a.ReadingFormat = "audiobook" }, want: false},
		{name: "ordinary sync", recordFormat: "Ebook", change: func(a *statepkg.Association) { a.Provenance = "regional_mapping" }, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, profileID := newEditionCreateService(t)
			a := testCreateAssociation("item")
			tc.change(&a)
			require.NoError(t, service.CreateEditionWithAssociation(context.Background(), profileID, "item", func(*database.ProfileWithTokens) (statepkg.Association, error) { return a, nil }))
			recordASIN, recordISBN := tc.recordASIN, tc.recordISBN
			if recordASIN == "" {
				recordASIN = "B012345678"
			}
			if recordISBN == "" {
				recordISBN = "9780306406157"
			}
			snapshot := syncsvc.SyncSnapshot{State: "canceled", BookOutcomes: []syncsvc.BookOutcomeRecord{{BookID: "item", Outcome: syncsvc.OutcomeNeedsReview, Format: tc.recordFormat, ASIN: recordASIN, ISBN: recordISBN, HardcoverBookID: "41"}}}
			require.NoError(t, service.AnnotateEditionAdditions(profileID, &snapshot))
			require.Equal(t, tc.want, snapshot.BookOutcomes[0].EditionAdded)
		})
	}
}

func TestAnnotateUnanchoredAudibleImportAfterJournalCleanup(t *testing.T) {
	confirmedAt := time.Date(2026, time.June, 5, 14, 30, 0, 0, time.UTC)
	baseRecord := syncsvc.BookOutcomeRecord{
		BookID: "item", Outcome: syncsvc.OutcomeNeedsReview, Reason: mismatch.ReasonAudibleImportAvailable,
		ASIN: "B012345678", SourceASIN: "B012345678", ISBN: "978-0-306-40615-7",
		SourceISBN13: "9780306406157", Format: models.ReadingFormatAudiobook,
	}
	baseAssociation := statepkg.Association{
		ABSItemID: "item", SourceASIN: "B012345678", SourceISBN13: "978-0-306-40615-7",
		Correction: "B0OTHER123:uk", RegionalExternalID: "B0OTHER123:uk", AudnexusConfirmedRegion: "uk",
		AudnexusConfirmedAt: confirmedAt, HardcoverBookID: "73", HardcoverEditionID: "84",
		ReadingFormat: "audiobook", Provenance: "audible_import_unanchored",
	}
	tests := []struct {
		name         string
		mutateRecord func(*syncsvc.BookOutcomeRecord)
		mutateAssoc  func(*statepkg.Association)
		wantAdded    bool
	}{
		{name: "confirmed unanchored import remains visible after journal cleanup", wantAdded: true},
		{name: "ordinary needs review item", mutateRecord: func(r *syncsvc.BookOutcomeRecord) { r.Reason = "identifier mismatch" }},
		{name: "changed source ASIN", mutateRecord: func(r *syncsvc.BookOutcomeRecord) { r.ASIN, r.SourceASIN = "B099999999", "B099999999" }},
		{name: "run is no longer unanchored", mutateRecord: func(r *syncsvc.BookOutcomeRecord) { r.HardcoverBookID = "73" }},
		{name: "run already has an edition candidate", mutateRecord: func(r *syncsvc.BookOutcomeRecord) { r.EditionID = "84" }},
		{name: "non-audiobook source", mutateRecord: func(r *syncsvc.BookOutcomeRecord) { r.Format = "Ebook" }},
		{name: "changed source ISBN", mutateAssoc: func(a *statepkg.Association) { a.SourceISBN13 = "9781861972712" }},
		{name: "unrelated ordinary sync association", mutateAssoc: func(a *statepkg.Association) { a.Provenance = "regional_mapping" }},
		{name: "confirmed region differs from imported identifier", mutateAssoc: func(a *statepkg.Association) { a.AudnexusConfirmedRegion = "us" }},
		{name: "correction differs from imported region", mutateAssoc: func(a *statepkg.Association) { a.Correction = "B0OTHER123:ca" }},
		{name: "missing confirmation time", mutateAssoc: func(a *statepkg.Association) { a.AudnexusConfirmedAt = time.Time{} }},
		{name: "nonpositive Hardcover book ID", mutateAssoc: func(a *statepkg.Association) { a.HardcoverBookID = "0" }},
		{name: "invalid Hardcover edition ID", mutateAssoc: func(a *statepkg.Association) { a.HardcoverEditionID = "invalid" }},
		{name: "wrong associated format", mutateAssoc: func(a *statepkg.Association) { a.ReadingFormat = "ebook" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, association := baseRecord, baseAssociation
			if tt.mutateRecord != nil {
				tt.mutateRecord(&record)
			}
			if tt.mutateAssoc != nil {
				tt.mutateAssoc(&association)
			}
			service, profileID := newEditionCreateService(t)
			require.NoError(t, service.CreateEditionWithAssociation(context.Background(), profileID, "item", func(*database.ProfileWithTokens) (statepkg.Association, error) {
				return association, nil
			}))
			snapshot := syncsvc.SyncSnapshot{State: "completed", BookOutcomes: []syncsvc.BookOutcomeRecord{record}}
			require.NoError(t, service.AnnotateEditionAdditions(profileID, &snapshot))
			require.Equal(t, tt.wantAdded, snapshot.BookOutcomes[0].EditionAdded)
		})
	}
}

func TestAnnotateEditionAdditionsStateFailureAndDryRun(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	path := service.profileSpecificStatePath(profileID, "sync.json")
	require.NoError(t, os.WriteFile(path, []byte("invalid state"), 0600))
	snapshot := syncsvc.SyncSnapshot{State: "completed", BookOutcomes: []syncsvc.BookOutcomeRecord{{BookID: "item", Outcome: syncsvc.OutcomeNeedsReview}}}
	require.Error(t, service.AnnotateEditionAdditions(profileID, &snapshot))
	require.False(t, snapshot.BookOutcomes[0].EditionAdded)
	snapshot.DryRun = true
	require.NoError(t, service.AnnotateEditionAdditions(profileID, &snapshot))
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "invalid state", string(contents))
}
