package multiuser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
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

func TestCreateEditionWithAssociationPersistsOnlyAfterOperationSucceeds(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	called := false
	err := service.CreateEditionWithAssociation(profileID, "item-1", func(profile *database.ProfileWithTokens) (statepkg.Association, error) {
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

func TestCreateEditionWithAssociationDoesNotPersistWhenOperationFails(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	remoteErr := errors.New("remote create rejected")
	err := service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		return statepkg.Association{}, remoteErr
	})
	require.ErrorIs(t, err, remoteErr)
	_, err = os.Stat(service.profileSpecificStatePath(profileID, "sync.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestCreateEditionWithAssociationDistinguishesLocalSaveFailureAfterRemoteSuccess(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	statePath := service.profileSpecificStatePath(profileID, "sync.json")
	called := false
	err := service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
		called = true
		// Force atomic rename to fail only after state was loaded and the remote
		// operation has succeeded.
		return testCreateAssociation("item-1"), os.Mkdir(statePath, 0700)
	})
	require.True(t, called)
	require.ErrorIs(t, err, ErrEditionAssociationSaveAfterRemoteSuccess)
}

func TestCreateEditionWithAssociationClassifiesInvalidAssociationAfterRemoteSuccess(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	err := service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
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
	err = service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
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
	err := service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
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
	err = service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
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
		createDone <- service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
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

func TestCreateEditionWithAssociationUsesProfileScopedStatePath(t *testing.T) {
	service, profileID := newEditionCreateService(t)
	otherPath := filepath.Join(t.TempDir(), "other.json")
	require.NoError(t, os.WriteFile(otherPath, []byte(`{"version":"4.0"}`), 0600))
	require.NoError(t, service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
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

	err = service.CreateEditionWithAssociation(profileID, "item-1", func(*database.ProfileWithTokens) (statepkg.Association, error) {
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
