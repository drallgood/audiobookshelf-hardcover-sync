package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ownershipTestAssociation() Association {
	return Association{
		ABSItemID: "item-1", SourceASIN: "B0OWNED001", HardcoverBookID: "123", HardcoverEditionID: "456",
		ReadingFormat: "audiobook", Provenance: "edition_asin",
	}
}

func TestOwnershipVerificationIsTiedToTheSavedMatch(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		book    string
		edition string
		cutoff  time.Time
		want    bool
	}{
		{name: "recent confirmation", book: "123", edition: "456", cutoff: now.Add(-time.Hour), want: true},
		{name: "confirmation older than cutoff", book: "123", edition: "456", cutoff: now.Add(time.Hour)},
		{name: "different edition", book: "123", edition: "789", cutoff: now.Add(-time.Hour)},
		{name: "different book", book: "999", edition: "456", cutoff: now.Add(-time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewState()
			require.NoError(t, s.SetAssociation(ownershipTestAssociation()))
			s.RecordOwnershipVerified("item-1", "123", "456", now)

			assert.Equal(t, tt.want, s.OwnershipVerifiedSince("item-1", tt.book, tt.edition, tt.cutoff))
		})
	}
}

func TestOwnershipVerificationNeedsAnAssociationAndPersists(t *testing.T) {
	s := NewState()
	s.RecordOwnershipVerified("item-1", "123", "456", time.Now())
	assert.False(t, s.IsDirty(), "nothing is saved without an association")
	assert.False(t, s.OwnershipVerifiedSince("item-1", "123", "456", time.Time{}))

	require.NoError(t, s.SetAssociation(ownershipTestAssociation()))
	s.RecordOwnershipVerified("item-1", "123", "456", time.Now())
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, s.Save(path))

	loaded, err := LoadState(path)
	require.NoError(t, err)
	assert.True(t, loaded.OwnershipVerifiedSince("item-1", "123", "456", time.Now().Add(-time.Hour)))
}

func TestReconfirmingTheSameMatchKeepsOwnershipButANewMatchDropsIt(t *testing.T) {
	s := NewState()
	require.NoError(t, s.SetAssociation(ownershipTestAssociation()))
	s.RecordOwnershipVerified("item-1", "123", "456", time.Now())

	require.NoError(t, s.SetAssociation(ownershipTestAssociation()))
	assert.True(t, s.OwnershipVerifiedSince("item-1", "123", "456", time.Now().Add(-time.Hour)))

	changed := ownershipTestAssociation()
	changed.HardcoverEditionID = "789"
	require.NoError(t, s.SetAssociation(changed))
	assert.False(t, s.OwnershipVerifiedSince("item-1", "123", "789", time.Now().Add(-time.Hour)))
}

func TestForgettingAMatchForgetsItsOwnershipVerification(t *testing.T) {
	s := NewState()
	require.NoError(t, s.SetAssociation(ownershipTestAssociation()))
	s.RecordOwnershipVerified("item-1", "123", "456", time.Now())

	_, removed := s.RemoveAssociation("item-1")

	require.True(t, removed)
	assert.False(t, s.OwnershipVerifiedSince("item-1", "123", "456", time.Time{}))
}

func TestCheckpointUpdatesKeepOwnershipVerification(t *testing.T) {
	s := NewState()
	require.NoError(t, s.SetAssociation(ownershipTestAssociation()))
	s.RecordOwnershipVerified("item-1", "123", "456", time.Now())

	s.UpdateBook("item-1:456", 0.4, "IN_PROGRESS")
	s.UpdateBookWithUserBookID("item-1", 0.6, "IN_PROGRESS", "300")

	assert.True(t, s.OwnershipVerifiedSince("item-1", "123", "456", time.Now().Add(-time.Hour)))
}
