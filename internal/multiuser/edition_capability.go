package multiuser

import (
	"context"
	"fmt"
	"strings"
)

// EditionCapabilityOperation identifies a Hardcover catalogue-write
// operation. Ebook insertion and regional Audible resolution have separate
// permissions and are reported independently.
type EditionCapabilityOperation string

const (
	EditionCapabilityInsertEdition EditionCapabilityOperation = "insert_edition"
	EditionCapabilityUpsertBook    EditionCapabilityOperation = "upsert_book"
)

// EditionCapabilityState is the evidence available for one catalogue-write
// operation under a profile's current Hardcover token.
type EditionCapabilityState string

const (
	EditionCapabilityAllowed    EditionCapabilityState = "allowed"
	EditionCapabilityDenied     EditionCapabilityState = "denied"
	EditionCapabilityUnverified EditionCapabilityState = "unverified"
)

// EditionCapabilityStatus describes capability evidence for one write
// operation. Unverified permits a later eligible, user-confirmed attempt with
// a warning; a known denial prevents an attempt.
type EditionCapabilityStatus struct {
	Operation  EditionCapabilityOperation `json:"operation"`
	Status     EditionCapabilityState     `json:"status"`
	CanAttempt bool                       `json:"can_attempt"`
	Reason     string                     `json:"reason,omitempty"`
	Warning    string                     `json:"warning,omitempty"`
}

// EditionCapability keeps ebook insertion separate from Audible resolution.
type EditionCapability struct {
	Ebook     EditionCapabilityStatus `json:"ebook"`
	Audiobook EditionCapabilityStatus `json:"audiobook"`
	DryRun    bool                    `json:"dry_run"`
}

// EditionCapabilityForProfile returns operation-specific evidence for the
// profile's Hardcover token. Hardcover has no documented read-only scope
// introspection, and no create operation has yet produced operation-specific
// evidence, so a configured token is unverified for both operations. No
// Hardcover request is made here.
func (s *MultiUserService) EditionCapabilityForProfile(_ context.Context, profileID string) (EditionCapability, error) {
	profile, err := s.repository.GetProfileHardcoverSettings(profileID)
	if err != nil {
		return EditionCapability{}, fmt.Errorf("failed to load profile %s for edition capability: %w", profileID, err)
	}
	if profile == nil {
		return EditionCapability{}, fmt.Errorf("%w: %s", ErrProfileNotFound, profileID)
	}

	statusFor := unverifiedEditionCapability
	if strings.TrimSpace(profile.HardcoverToken) == "" {
		statusFor = missingHardcoverTokenEditionCapability
	}
	return EditionCapability{
		Ebook:     statusFor(EditionCapabilityInsertEdition),
		Audiobook: statusFor(EditionCapabilityUpsertBook),
		DryRun:    profile.SyncConfig.DryRun,
	}, nil
}

func unverifiedEditionCapability(operation EditionCapabilityOperation) EditionCapabilityStatus {
	return EditionCapabilityStatus{
		Operation:  operation,
		Status:     EditionCapabilityUnverified,
		CanAttempt: true,
		Warning:    "permission_unverified",
	}
}

func missingHardcoverTokenEditionCapability(operation EditionCapabilityOperation) EditionCapabilityStatus {
	return EditionCapabilityStatus{
		Operation:  operation,
		Status:     EditionCapabilityDenied,
		CanAttempt: false,
		Reason:     "hardcover_token_missing",
	}
}
