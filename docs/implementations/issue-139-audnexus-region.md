# Implementation Plan: Configurable Audnexus Region (Issue #139)

**Status: ✅ COMPLETED** (2026-07-11)

## Summary

Users with non-US Audible accounts (e.g., `audible.ca`, `audible.co.uk`) have region-specific ASINs that differ from the US store. The current Audnexus enrichment lookup does not pass a `region` parameter, causing 404 errors for all non-US ASINs. This feature adds a configurable `audnexus_region` setting with a fallback mechanism.

## Issue

**Link:** https://github.com/drallgood/audiobookshelf-hardcover-sync/issues/139

**Current behavior:** The `audnex.Client.GetBookByASIN(ctx, asin, "")` call always passes an empty region string. For non-US ASINs, this returns 404.

**Expected behavior:** A configurable `audnexus_region` setting in config so the lookup queries the correct regional endpoint, with a fallback strategy: try the configured region first, then fall back to `us` if the lookup fails.

## Known Audnexus Regions

| Code  | Country          |
|-------|------------------|
| `us`  | United States    |
| `ca`  | Canada           |
| `uk`  | United Kingdom   |
| `au`  | Australia        |
| `de`  | Germany          |
| `fr`  | France           |

## Scope of Changes

The Audnexus client is only used in one location: `internal/mismatch/mismatch.go:150` during mismatch enrichment (when a book is looked up by ASIN to get release date details). The `GetBookByASIN` method already accepts a `region` parameter — the issue is that it's never populated with anything other than `""`.

## Implementation Steps

### Step 1: Add `audnexus_region` to Config struct

**File:** `internal/config/config.go`

- Add `AudnexusRegion string` field to the `Audiobookshelf` config struct (lines 84-89).
- Add env var binding `AUDIOBOOKSHELF_AUDNEXUS_REGION`.
- Set default to `""` (empty = current behavior, no region filtering).
- Add logging of the configured region in `Load()`.

```go
// In the Audiobookshelf struct (line 84):
Audiobookshelf struct {
    URL             string `yaml:"url" env:"AUDIOBOOKSHELF_URL"`
    Token           string `yaml:"token" env:"AUDIOBOOKSHELF_TOKEN"`
    AudnexusRegion  string `yaml:"audnexus_region" env:"AUDIOBOOKSHELF_AUDNEXUS_REGION"`
} `yaml:"audiobookshelf"`
```

- In `loadFromEnv()` (around line 576), add:

```go
if region := os.Getenv("AUDIOBOOKSHELF_AUDNEXUS_REGION"); region != "" {
    cfg.Audiobookshelf.AudnexusRegion = region
}
```

- In `Load()` (around line 326), add the field to the config dump:

```go
fmt.Printf("  audnexus_region: %s\n", cfg.Audiobookshelf.AudnexusRegion)
```

### Step 2: Update `config.example.yaml`

**File:** `config.example.yaml`

Add `audnexus_region` under the `audiobookshelf:` section:

```yaml
audiobookshelf:
  url: "https://your-audiobookshelf-instance.com"
  token: "your-audiobookshelf-token"
  audnexus_region: ""   # Audnexus region for ASIN lookup (ca, uk, au, de, fr, us). Empty = current/default behavior.
```

### Step 3: Update `.env.example`

**File:** `.env.example`

Add the env var in the Audiobookshelf API Settings section:

```
# Audnexus region for non-US Audible ASIN lookups (ca, uk, au, de, fr, us)
# Leave empty for default behavior (no region filter)
AUDIOBOOKSHELF_AUDNEXUS_REGION=
```

### Step 4: Add fallback logic to `mismatch.AddWithMetadata`

**File:** `internal/mismatch/mismatch.go`

Currently at line 150:
```go
book, err := audnexClient.GetBookByASIN(ctx, metadata.ASIN, "")
```

The problem: `AddWithMetadata` doesn't have access to the config. We need to thread the `audnexus_region` through either:

**Option A (Recommended):** Accept a `region` parameter or `config.Config` in `AddWithMetadata`.

This requires updating `AddWithMetadata`'s signature. It's called from:
- `internal/sync/service.go:1267` — already has access to `s.config`

**Option B:** Extract region from a global/singleton — not ideal, makes testing harder.

**Recommended implementation (Option A):**

Update `AddWithMetadata` signature to accept `region string`:

```go
func AddWithMetadata(metadata MediaMetadata, bookID, editionID, reason string, duration float64, audiobookShelfID string, hc hardcover.HardcoverClientInterface, audnexusRegion string) {
```

And add the fallback logic (replaces line 149-151):

```go
// Try configured region first, then fall back to "us" if not found
regions := []string{}
if audnexusRegion != "" {
    regions = append(regions, audnexusRegion)
    regions = append(regions, "us")  // fallback
} else {
    regions = append(regions, "")  // current behavior: no region
}

var book *audnex.Book
var err error
for _, region := range regions {
    book, err = audnexClient.GetBookByASIN(ctx, metadata.ASIN, region)
    if err == nil && book != nil {
        if region != "" {
            log.Info("Audnex API lookup succeeded with region", map[string]interface{}{
                "asin":   metadata.ASIN,
                "region": region,
            })
        }
        break
    }
    if region != "" {
        log.Debug("Audnex lookup failed for region, trying fallback", map[string]interface{}{
            "asin":   metadata.ASIN,
            "region": region,
            "error":  err.Error(),
        })
    }
}
```

Update the call site in `internal/sync/service.go` (line 1267-1288):

```go
mismatch.AddWithMetadata(
    mismatch.MediaMetadata{...},
    book.ID,
    edID,
    "Found by title/author only - manual verification required",
    book.Media.Duration,
    book.ID,
    s.hardcover,
    s.config.Audiobookshelf.AudnexusRegion,  // NEW: pass region
)
```

### Step 5: Update multi-user SyncConfigData model

**File:** `internal/database/models.go`

Add `AudnexusRegion` to `SyncConfigData`:

```go
type SyncConfigData struct {
    // ... existing fields ...
    AudnexusRegion     string  `json:"audnexus_region"`
    // ... existing fields ...
}
```

Update `IsEmpty()` to include the new field:

```go
func (s SyncConfigData) IsEmpty() bool {
    return !s.Incremental &&
        // ... existing checks ...
        s.AudnexusRegion == "" &&
        // ... existing checks ...
}
```

### Step 6: Thread AudnexusRegion through multi-user sync

**File:** `internal/multiuser/service.go`

When building the per-profile config for sync (the `buildProfileConfig` function — search for it), populate `cfg.Audiobookshelf.AudnexusRegion` from the profile's `SyncConfig.AudnexusRegion`.

### Step 7: Update web UI

**File:** `web/static/app.js`

In the profile configuration form, add an input field for `audnexus_region`. Locate where `sync_config` fields are rendered and add:

```javascript
// Add to the sync config form
<input type="text" name="audnexus_region" placeholder="e.g., ca, uk" maxlength="2"
    value="${user.sync_config?.audnexus_region || ''}">
```

Also add a label/help text:
```
Audnexus Region: (Optional) Two-letter region code for Audible ASIN lookups. Used by non-US
Audible users (ca, uk, au, de, fr). Leave empty for default behavior.
```

### Step 8: Add validation

**File:** `internal/config/config.go` in the `Validate()` method

Add validation for the region code:

```go
if c.Audiobookshelf.AudnexusRegion != "" {
    validRegions := map[string]bool{"us": true, "ca": true, "uk": true, "au": true, "de": true, "fr": true}
    if !validRegions[strings.ToLower(c.Audiobookshelf.AudnexusRegion)] {
        fmt.Printf("Warning: Unknown audnexus_region '%s'. Valid values: us, ca, uk, au, de, fr\n",
            c.Audiobookshelf.AudnexusRegion)
    }
}
```

### Step 9: Add tests

**File:** `internal/api/audnex/client_test.go`

Add a test case for non-empty region parameter:

```go
func TestGetBookByASIN_WithRegion(t *testing.T) {
    // Integration test: verifies GET /books/{asin}?region=ca returns data
}
```

**File:** `internal/mismatch/mismatch_test.go`

Add a test for the fallback logic (configured region fails → falls back to `us`):

```go
func TestAddWithMetadata_RegionFallback(t *testing.T) {
    // Test that when audnexusRegion is set to "ca" and it returns 404,
    // the fallback to "us" is attempted
}
```

## Files Changed Summary

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `AudnexusRegion` field, env var, validation |
| `config.example.yaml` | Document `audnexus_region` |
| `.env.example` | Document `AUDIOBOOKSHELF_AUDNEXUS_REGION` |
| `internal/mismatch/mismatch.go` | Add region parameter + fallback logic to `AddWithMetadata` |
| `internal/sync/service.go` | Pass `s.config.Audiobookshelf.AudnexusRegion` to mismatch call |
| `internal/database/models.go` | Add `AudnexusRegion` to `SyncConfigData` |
| `internal/multiuser/service.go` | Thread region from profile config to sync config |
| `web/static/app.js` | Add UI field for region in profile config form |
| `internal/api/audnex/client_test.go` | Test region parameter |
| `internal/mismatch/mismatch_test.go` | Test region fallback |

## Testing Strategy

1. **Unit tests:** Verify `loadFromEnv` parses the env var correctly.
2. **Integration test:** Call Audnex API with `region=ca` and verify it returns data for a known Canadian ASIN.
3. **Fallback test:** Mock the Audnex client to return 404 for `ca` but 200 for `us`, verify fallback occurs.
4. **Config round-trip:** Create a profile with a region, read it back, verify it's preserved.

## Backward Compatibility

- Default value is `""` (empty string), which matches the current behavior.
- No existing config files or environment variables are affected.
- Web UI forms without the new field will submit `audnexus_region: ""`.
- The feature is entirely opt-in.