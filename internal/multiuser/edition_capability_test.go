package multiuser

import (
	"context"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/stretchr/testify/require"
)

func TestEditionCapabilityForProfileReportsUnverifiedOperationStatuses(t *testing.T) {
	service, _ := newStatusLookupService(t)
	const profileID = "capability-profile"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
		database.SyncConfigData{DryRun: true},
	))

	capability, err := service.EditionCapabilityForProfile(context.Background(), profileID)
	require.NoError(t, err)
	require.True(t, capability.DryRun)
	require.Equal(t, EditionCapabilityInsertEdition, capability.Ebook.Operation)
	require.Equal(t, EditionCapabilityUnverified, capability.Ebook.Status)
	require.True(t, capability.Ebook.CanAttempt)
	require.Equal(t, "permission_unverified", capability.Ebook.Warning)
	require.Equal(t, EditionCapabilityUpsertBook, capability.Audiobook.Operation)
	require.Equal(t, EditionCapabilityUnverified, capability.Audiobook.Status)
	require.True(t, capability.Audiobook.CanAttempt)
	require.Equal(t, "permission_unverified", capability.Audiobook.Warning)
}

func TestEditionCapabilityForProfileReturnsNotFound(t *testing.T) {
	service, _ := newStatusLookupService(t)
	_, err := service.EditionCapabilityForProfile(context.Background(), "missing-capability-profile")
	require.ErrorIs(t, err, ErrProfileNotFound)
}

func TestEditionCapabilityForProfileBlocksMissingHardcoverToken(t *testing.T) {
	service, _ := newStatusLookupService(t)
	const profileID = "missing-token-capability-profile"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Missing token profile", "http://abs.home", "abs-token", "",
		database.SyncConfigData{},
	))

	capability, err := service.EditionCapabilityForProfile(context.Background(), profileID)
	require.NoError(t, err)
	require.Equal(t, EditionCapabilityDenied, capability.Ebook.Status)
	require.False(t, capability.Ebook.CanAttempt)
	require.Equal(t, "hardcover_token_missing", capability.Ebook.Reason)
	require.Equal(t, EditionCapabilityDenied, capability.Audiobook.Status)
	require.False(t, capability.Audiobook.CanAttempt)
	require.Equal(t, "hardcover_token_missing", capability.Audiobook.Reason)
}

func TestEditionCapabilityForProfilePreservesHardcoverAndConfigErrors(t *testing.T) {
	tests := []struct {
		name          string
		updates       map[string]interface{}
		errorContains string
	}{
		{
			name:          "hardcover decryption",
			updates:       map[string]interface{}{"hardcover_token_encrypted": "corrupt-ciphertext"},
			errorContains: "failed to decrypt Hardcover token",
		},
		{
			name:          "sync config parsing",
			updates:       map[string]interface{}{"sync_config": "{"},
			errorContains: "failed to parse sync config",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, db := newStatusLookupService(t)
			const profileID = "invalid-capability-settings-profile"
			require.NoError(t, service.repository.CreateProfile(
				profileID, "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
				database.SyncConfigData{},
			))
			require.NoError(t, db.Model(&database.SyncProfileConfig{}).
				Where("profile_id = ?", profileID).Updates(test.updates).Error)

			_, err := service.EditionCapabilityForProfile(context.Background(), profileID)
			require.ErrorContains(t, err, test.errorContains)
		})
	}
}

func TestEditionCapabilityForProfileUsesUpdatedHardcoverToken(t *testing.T) {
	service, _ := newStatusLookupService(t)
	const profileID = "updated-token-capability-profile"
	require.NoError(t, service.CreateProfile(
		profileID, "Updated token profile", "http://abs.home", "abs-token", "",
		database.SyncConfigData{},
	))

	withoutToken, err := service.EditionCapabilityForProfile(context.Background(), profileID)
	require.NoError(t, err)
	require.Equal(t, EditionCapabilityDenied, withoutToken.Ebook.Status)
	require.False(t, withoutToken.Ebook.CanAttempt)

	require.NoError(t, service.UpdateProfileConfig(
		profileID, "http://abs.home", "", "new-hardcover-token", database.SyncConfigData{},
	))
	withUpdatedToken, err := service.EditionCapabilityForProfile(context.Background(), profileID)
	require.NoError(t, err)
	require.Equal(t, EditionCapabilityUnverified, withUpdatedToken.Ebook.Status)
	require.True(t, withUpdatedToken.Ebook.CanAttempt)
	require.Equal(t, "permission_unverified", withUpdatedToken.Ebook.Warning)
	require.Equal(t, EditionCapabilityUnverified, withUpdatedToken.Audiobook.Status)
	require.True(t, withUpdatedToken.Audiobook.CanAttempt)
}
