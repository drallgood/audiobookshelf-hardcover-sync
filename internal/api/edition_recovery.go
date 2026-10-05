package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const editionRecoveryLifetime = 48 * time.Hour

type editionRecoveryClaims struct {
	Version           int    `json:"v"`
	ProfileID         string `json:"p"`
	RunID             string `json:"r"`
	ABSItemID         string `json:"i"`
	HardcoverBookID   string `json:"b"`
	AudibleIdentifier string `json:"a"`
	Correction        string `json:"c"`
	IssuedAt          int64  `json:"issued_at"`
	ExpiresAt         int64  `json:"expires_at"`
}

type editionRecoveryData struct {
	AudibleIdentifier string `json:"audible_identifier"`
	HardcoverBookID   string `json:"hardcover_book_id,omitempty"`
	RecoveryToken     string `json:"recovery_token"`
	RecoveryExpiresAt int64  `json:"recovery_expires_at,omitempty"`
}

func signEditionRecoveryToken(hardcoverToken string, claims editionRecoveryClaims) string {
	return signEditionRecoveryTokenAt(hardcoverToken, claims, time.Now())
}

func signEditionRecoveryTokenAt(hardcoverToken string, claims editionRecoveryClaims, now time.Time) string {
	claims.Version = 1
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(editionRecoveryLifetime).Unix()
	payload, _ := json.Marshal(claims)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := editionRecoverySignature(hardcoverToken, encodedPayload)
	return encodedPayload + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func verifyEditionRecoveryToken(hardcoverToken, token string, expected editionRecoveryClaims) (editionRecoveryClaims, bool) {
	return verifyEditionRecoveryTokenAt(hardcoverToken, token, expected, time.Now())
}

func verifyEditionRecoveryTokenAt(hardcoverToken, token string, expected editionRecoveryClaims, now time.Time) (editionRecoveryClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return editionRecoveryClaims{}, false
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return editionRecoveryClaims{}, false
	}
	wantSignature := editionRecoverySignature(hardcoverToken, parts[0])
	if !hmac.Equal(providedSignature, wantSignature) {
		return editionRecoveryClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return editionRecoveryClaims{}, false
	}
	var claims editionRecoveryClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return editionRecoveryClaims{}, false
	}
	nowUnix := now.Unix()
	if claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > int64(editionRecoveryLifetime/time.Second) ||
		claims.IssuedAt > nowUnix || nowUnix >= claims.ExpiresAt {
		return editionRecoveryClaims{}, false
	}
	if claims.Version != 1 || claims.ProfileID != expected.ProfileID || claims.RunID != expected.RunID ||
		claims.ABSItemID != expected.ABSItemID || claims.HardcoverBookID != expected.HardcoverBookID ||
		claims.AudibleIdentifier != expected.AudibleIdentifier {
		return editionRecoveryClaims{}, false
	}
	return claims, true
}

func editionRecoverySignature(hardcoverToken, encodedPayload string) []byte {
	mac := hmac.New(sha256.New, []byte("edition-recovery-v1\x00"+hardcoverToken))
	_, _ = mac.Write([]byte(encodedPayload))
	return mac.Sum(nil)
}
