# Test Utilities

This package contains helpers shared by production-package tests.

`SetGlobalLogLevel(t, level)` serializes tests that change zerolog's process-wide
log level and restores the previous level during test cleanup. Use it instead of
changing the global level without synchronization.

```go
import "github.com/drallgood/audiobookshelf-hardcover-sync/internal/testutils"

func TestExample(t *testing.T) {
    testutils.SetGlobalLogLevel(t, zerolog.DebugLevel)
    // Exercise the production behavior under test.
}
```

Tests belong beside the production code they exercise. Do not add copied
implementations or hard-coded service stubs here and test those in place of the
application. Stub external boundaries in the consuming package instead.
