package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEditionRecoveryTokenBindsAttemptAndProfileCredential(t *testing.T) {
	claims := editionRecoveryClaims{
		ProfileID: "profile-1", RunID: "run-1", ABSItemID: "item-1",
		HardcoverBookID: "42", AudibleIdentifier: "B0ABCDE123:uk", Correction: "B0ABCDE123:uk",
	}
	token := signEditionRecoveryToken("profile-hardcover-token", claims)
	verified, ok := verifyEditionRecoveryToken("profile-hardcover-token", token, claims)
	require.True(t, ok)
	require.Equal(t, claims.Correction, verified.Correction)

	for name, mutate := range map[string]func(*editionRecoveryClaims){
		"profile":    func(c *editionRecoveryClaims) { c.ProfileID = "profile-2" },
		"run":        func(c *editionRecoveryClaims) { c.RunID = "run-2" },
		"item":       func(c *editionRecoveryClaims) { c.ABSItemID = "item-2" },
		"book":       func(c *editionRecoveryClaims) { c.HardcoverBookID = "43" },
		"identifier": func(c *editionRecoveryClaims) { c.AudibleIdentifier = "B0OTHER123:uk" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := claims
			mutate(&changed)
			_, ok := verifyEditionRecoveryToken("profile-hardcover-token", token, changed)
			require.False(t, ok)
		})
	}
	_, ok = verifyEditionRecoveryToken("rotated-hardcover-token", token, claims)
	require.False(t, ok, "rotating the profile's Hardcover credential invalidates old recovery attempts")

	last := token[len(token)-1]
	for _, replacement := range []byte{'a', 'b', 'c', 'd'} {
		if last != replacement {
			tampered := token[:len(token)-1] + string(replacement)
			if _, ok := verifyEditionRecoveryToken("profile-hardcover-token", tampered, claims); !ok {
				return
			}
		}
	}
	t.Fatal("expected a modified signature to invalidate the recovery token")
}
