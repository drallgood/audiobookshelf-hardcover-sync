package multiuser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/stretchr/testify/require"
)

func TestEditionCapabilityForProfileSkipsProbesInDryRun(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	service, _ := newStatusLookupService(t)
	service.globalConfig.Hardcover.BaseURL = server.URL
	const profileID = "capability-profile"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
		database.SyncConfigData{DryRun: true},
	))

	capability, err := service.EditionCapabilityForProfile(context.Background(), profileID)
	require.NoError(t, err)
	require.True(t, capability.DryRun)
	require.Equal(t, EditionCapabilityInsertEdition, capability.Ebook.Operation)
	require.Equal(t, EditionCapabilityAllowed, capability.Ebook.Status)
	require.True(t, capability.Ebook.CanAttempt)
	require.Empty(t, capability.Ebook.Warning)
	require.Equal(t, EditionCapabilityUpsertBook, capability.Audiobook.Operation)
	require.Equal(t, EditionCapabilityAllowed, capability.Audiobook.Status)
	require.True(t, capability.Audiobook.CanAttempt)
	require.Empty(t, capability.Audiobook.Warning)
	require.Zero(t, requests.Load(), "dry run reports allowed without probing")
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
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Authorization"), "new-hardcover-token") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"insufficient_scope","error_description":"Missing scopes: write:catalog:append","scope":"write:catalog:append"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errors":[{"message":"Couldn't find Book"}],"data":null}`))
	}))
	defer server.Close()

	service, _ := newStatusLookupService(t)
	service.globalConfig.Hardcover.BaseURL = server.URL
	const profileID = "updated-token-capability-profile"
	require.NoError(t, service.CreateProfile(
		profileID, "Updated token profile", "http://abs.home", "abs-token", "old-hardcover-token",
		database.SyncConfigData{},
	))

	withOldToken, err := service.EditionCapabilityForProfile(context.Background(), profileID)
	require.NoError(t, err)
	require.Equal(t, EditionCapabilityAllowed, withOldToken.Ebook.Status)
	require.Equal(t, int32(1), requests.Load())

	require.NoError(t, service.UpdateProfileConfig(
		profileID, "http://abs.home", "", "new-hardcover-token", database.SyncConfigData{},
	))
	withUpdatedToken, err := service.EditionCapabilityForProfile(context.Background(), profileID)
	require.NoError(t, err)
	require.Equal(t, EditionCapabilityDenied, withUpdatedToken.Ebook.Status)
	require.False(t, withUpdatedToken.Ebook.CanAttempt)
	require.Equal(t, "insufficient_scope", withUpdatedToken.Ebook.Reason)
	require.Equal(t, EditionCapabilityUnverified, withUpdatedToken.Audiobook.Status)
	require.True(t, withUpdatedToken.Audiobook.CanAttempt)
	require.Equal(t, int32(2), requests.Load(), "a token change invalidates the cached probe result")
}

func TestEditionCapabilityForProfileCachesPerProfileAndExpires(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Couldn't find Book"}],"data":null}`))
	}))
	defer server.Close()

	service, _ := newStatusLookupService(t)
	service.globalConfig.Hardcover.BaseURL = server.URL
	for _, profileID := range []string{"cache-profile-one", "cache-profile-two"} {
		require.NoError(t, service.repository.CreateProfile(
			profileID, "Capability profile", "http://abs.home", "abs-token", "same-token",
			database.SyncConfigData{},
		))
	}

	for i := 0; i < 2; i++ {
		capability, err := service.EditionCapabilityForProfile(context.Background(), "cache-profile-one")
		require.NoError(t, err)
		require.Equal(t, EditionCapabilityAllowed, capability.Ebook.Status)
	}
	require.Equal(t, int32(1), requests.Load(), "same profile and operation reuses the definite result")

	_, err := service.EditionCapabilityForProfile(context.Background(), "cache-profile-two")
	require.NoError(t, err)
	require.Equal(t, int32(2), requests.Load(), "another profile has an independent result")

	service.hardcoverClientMutex.Lock()
	for key, entry := range service.editionCapabilityCache {
		if key.profileID == "cache-profile-one" {
			entry.expiresAt = time.Now().Add(-time.Second)
			service.editionCapabilityCache[key] = entry
		}
	}
	service.hardcoverClientMutex.Unlock()
	_, err = service.EditionCapabilityForProfile(context.Background(), "cache-profile-one")
	require.NoError(t, err)
	require.Equal(t, int32(3), requests.Load(), "expired definite results are probed again")
}

func TestEditionCapabilityForProfileRetriesUnverifiedProbeAfterShortTTL(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "temporary failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	service, _ := newStatusLookupService(t)
	service.globalConfig.Hardcover.BaseURL = server.URL
	service.globalConfig.RateLimit.Rate = time.Nanosecond
	require.NoError(t, service.repository.CreateProfile(
		"transient-capability-profile", "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
		database.SyncConfigData{},
	))

	capability, err := service.EditionCapabilityForProfile(context.Background(), "transient-capability-profile")
	require.NoError(t, err)
	require.Equal(t, EditionCapabilityUnverified, capability.Ebook.Status)
	service.hardcoverClientMutex.Lock()
	cacheEntry := service.editionCapabilityCache[editionCapabilityCacheKey{
		profileID: "transient-capability-profile",
		operation: EditionCapabilityInsertEdition,
	}]
	service.hardcoverClientMutex.Unlock()
	remainingTTL := time.Until(cacheEntry.expiresAt)
	require.Positive(t, remainingTTL)
	require.LessOrEqual(t, remainingTTL, editionCapabilityUnverifiedTTL)

	service.hardcoverClientMutex.Lock()
	cacheEntry.expiresAt = time.Now().Add(-time.Second)
	service.editionCapabilityCache[editionCapabilityCacheKey{
		profileID: "transient-capability-profile",
		operation: EditionCapabilityInsertEdition,
	}] = cacheEntry
	service.hardcoverClientMutex.Unlock()
	_, err = service.EditionCapabilityForProfile(context.Background(), "transient-capability-profile")
	require.NoError(t, err)
	require.Equal(t, int32(2), requests.Load(), "transient outcomes are retried after the short cache TTL")
}

func TestCapabilityAndProfileHardcoverClientsShareLimiter(t *testing.T) {
	var active, maxActive atomic.Int32
	var requests atomic.Int32
	started := make(chan struct{}, 3)
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		for previous := maxActive.Load(); current > previous && !maxActive.CompareAndSwap(previous, current); previous = maxActive.Load() {
		}
		defer active.Add(-1)
		if requests.Add(1) == 1 {
			started <- struct{}{}
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
		} else {
			started <- struct{}{}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()

	service, _ := newStatusLookupService(t)
	service.globalConfig.Hardcover.BaseURL = server.URL
	service.globalConfig.RateLimit.Rate = time.Nanosecond
	service.globalConfig.RateLimit.MaxConcurrent = 1
	const profileID = "shared-hardcover-limiter-profile"
	const token = "shared-hardcover-token"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Shared limiter", "http://abs.home", "abs-token", token,
		database.SyncConfigData{},
	))

	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		_, _ = service.EditionCapabilityForProfile(context.Background(), profileID)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("capability probe did not reach Hardcover")
	}

	clients := []*hardcover.Client{
		service.NewHardcoverClientForProfile(profileID, token),
		service.NewHardcoverClientForProfile(profileID, token),
	}
	clientDone := make(chan error, len(clients))
	for _, client := range clients {
		go func(client *hardcover.Client) {
			clientDone <- client.GraphQLMutation(context.Background(), "mutation { test }", nil, &map[string]any{})
		}(client)
	}

	// Keep the probe request inside its HTTP handler. The other profile clients
	// must wait on the same one-request concurrency permit.
	select {
	case <-started:
		t.Fatal("another profile client bypassed the active probe's limiter")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)

	for range clients {
		select {
		case err := <-clientDone:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("profile Hardcover request did not finish")
		}
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("capability probe did not finish")
	}
	require.Equal(t, int32(3), requests.Load())
	require.Equal(t, int32(1), maxActive.Load(), "profile clients share one concurrent-request limit")

	service.hardcoverClientMutex.Lock()
	originalLimiter := service.profileHardcoverRateLimiters[profileHardcoverRateLimiterKey{
		profileID: profileID, tokenFingerprint: hardcoverTokenFingerprint(token),
	}].limiter
	service.hardcoverClientMutex.Unlock()

	rotatedClient := service.NewHardcoverClientForProfile(profileID, "rotated-hardcover-token")
	require.NotNil(t, rotatedClient)
	service.NewHardcoverClientForProfile(profileID, token)
	service.hardcoverClientMutex.Lock()
	rotatedLimiter := service.profileHardcoverRateLimiters[profileHardcoverRateLimiterKey{
		profileID: profileID, tokenFingerprint: hardcoverTokenFingerprint("rotated-hardcover-token"),
	}].limiter
	currentLimiter := service.profileHardcoverRateLimiters[profileHardcoverRateLimiterKey{
		profileID: profileID, tokenFingerprint: hardcoverTokenFingerprint(token),
	}].limiter
	service.hardcoverClientMutex.Unlock()
	require.NotSame(t, originalLimiter, rotatedLimiter, "different tokens must not share a limiter")
	require.Same(t, originalLimiter, currentLimiter, "clients for the same token reuse its limiter")
}

func TestConfigOnlyProfileUpdatePreservesLimiterForExistingClient(t *testing.T) {
	var active, maxActive atomic.Int32
	var requests atomic.Int32
	started := make(chan struct{}, 2)
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		for previous := maxActive.Load(); current > previous && !maxActive.CompareAndSwap(previous, current); previous = maxActive.Load() {
		}
		defer active.Add(-1)
		if requests.Add(1) == 1 {
			started <- struct{}{}
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
		} else {
			started <- struct{}{}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()

	service, _ := newStatusLookupService(t)
	service.globalConfig.Hardcover.BaseURL = server.URL
	service.globalConfig.RateLimit.Rate = time.Nanosecond
	service.globalConfig.RateLimit.MaxConcurrent = 1
	const profileID = "config-update-shared-limiter-profile"
	const token = "unchanged-hardcover-token"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Shared limiter", "http://abs.home", "abs-token", token,
		database.SyncConfigData{},
	))

	oldClient := service.NewHardcoverClientForProfile(profileID, token)
	limiterKey := profileHardcoverRateLimiterKey{profileID: profileID, tokenFingerprint: hardcoverTokenFingerprint(token)}
	service.hardcoverClientMutex.Lock()
	oldLimiter := service.profileHardcoverRateLimiters[limiterKey].limiter
	service.hardcoverClientMutex.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	oldDone := make(chan error, 1)
	go func() {
		oldDone <- oldClient.GraphQLMutation(ctx, "mutation { test }", nil, &map[string]any{})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("existing Hardcover client did not reach the test server")
	}

	// The API passes the existing nonempty token on a config-only update.
	require.NoError(t, service.UpdateProfileConfig(
		profileID, "http://abs.updated", "", token, database.SyncConfigData{},
	))
	service.hardcoverClientMutex.Lock()
	limiterAfterUpdate := service.profileHardcoverRateLimiters[limiterKey].limiter
	service.hardcoverClientMutex.Unlock()
	require.Same(t, oldLimiter, limiterAfterUpdate, "unchanged token must preserve the active limiter")

	newClient := service.NewHardcoverClientForProfile(profileID, token)
	newDone := make(chan error, 1)
	go func() {
		newDone <- newClient.GraphQLMutation(ctx, "mutation { test }", nil, &map[string]any{})
	}()
	select {
	case <-started:
		t.Fatal("a client created after a config-only update bypassed the active limiter")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	for _, done := range []<-chan error{oldDone, newDone} {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("shared-limit request did not finish")
		}
	}
	require.Equal(t, int32(2), requests.Load())
	require.Equal(t, int32(1), maxActive.Load(), "clients remain serialized by the profile's concurrency limit")
}

func TestEditionCapabilityForProfileDeduplicatesConcurrentProbes(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Couldn't find Book"}],"data":null}`))
	}))
	defer server.Close()

	service, _ := newStatusLookupService(t)
	service.globalConfig.Hardcover.BaseURL = server.URL
	service.globalConfig.RateLimit.Rate = time.Nanosecond
	require.NoError(t, service.repository.CreateProfile(
		"dedupe-capability-profile", "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
		database.SyncConfigData{},
	))

	var wg sync.WaitGroup
	results := make([]EditionCapabilityState, 2)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			capability, err := service.EditionCapabilityForProfile(context.Background(), "dedupe-capability-profile")
			require.NoError(t, err)
			results[index] = capability.Ebook.Status
		}(i)
		if i == 0 {
			<-started
		}
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	require.Equal(t, []EditionCapabilityState{EditionCapabilityAllowed, EditionCapabilityAllowed}, results)
	require.Equal(t, int32(1), requests.Load(), "concurrent loads share one in-flight operation")
}
