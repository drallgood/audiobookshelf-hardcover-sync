package api

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

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

func TestEditionRecoveryTokenExpiryValidation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := editionRecoveryClaims{
		Version: 1, ProfileID: "profile-1", RunID: "run-1", ABSItemID: "item-1",
		HardcoverBookID: "42", AudibleIdentifier: "B0ABCDE123:uk", Correction: "B0ABCDE123:uk",
		IssuedAt: now.Add(-time.Hour).Unix(), ExpiresAt: now.Add(47 * time.Hour).Unix(),
	}
	tests := []struct {
		name    string
		mutate  func(*editionRecoveryClaims)
		checkAt time.Time
		want    bool
	}{
		{name: "valid before expiry", checkAt: now.Add(46*time.Hour + 59*time.Minute), want: true},
		{name: "exact expiry", checkAt: now.Add(47 * time.Hour), want: false},
		{name: "missing legacy timestamps", mutate: func(c *editionRecoveryClaims) { c.IssuedAt, c.ExpiresAt = 0, 0 }, checkAt: now, want: false},
		{name: "future issuance", mutate: func(c *editionRecoveryClaims) { c.IssuedAt = now.Add(time.Second).Unix() }, checkAt: now, want: false},
		{name: "nonpositive interval", mutate: func(c *editionRecoveryClaims) { c.ExpiresAt = c.IssuedAt }, checkAt: now, want: false},
		{name: "excessive interval", mutate: func(c *editionRecoveryClaims) {
			c.ExpiresAt = c.IssuedAt + int64(editionRecoveryLifetime/time.Second) + 1
		}, checkAt: now, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := base
			if tt.mutate != nil {
				tt.mutate(&claims)
			}
			token := encodeEditionRecoveryClaims("profile-hardcover-token", claims)
			_, ok := verifyEditionRecoveryTokenAt("profile-hardcover-token", token, base, tt.checkAt)
			require.Equal(t, tt.want, ok)
		})
	}

	signed := signEditionRecoveryTokenAt("profile-hardcover-token", base, now)
	verified, ok := verifyEditionRecoveryTokenAt("profile-hardcover-token", signed, base, now)
	require.True(t, ok)
	require.Equal(t, now.Unix(), verified.IssuedAt)
	require.Equal(t, now.Add(editionRecoveryLifetime).Unix(), verified.ExpiresAt)
}

func encodeEditionRecoveryClaims(hardcoverToken string, claims editionRecoveryClaims) string {
	payload, _ := json.Marshal(claims)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := editionRecoverySignature(hardcoverToken, encodedPayload)
	return encodedPayload + "." + base64.RawURLEncoding.EncodeToString(signature)
}
