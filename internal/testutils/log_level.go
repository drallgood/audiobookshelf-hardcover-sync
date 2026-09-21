package testutils

import (
	"sync"
	"testing"

	"github.com/rs/zerolog"
)

// logLevelMu serializes tests that change zerolog's process-wide level.
var logLevelMu sync.Mutex

// SetGlobalLogLevel sets zerolog's global level for the duration of the test
// and restores the previous level afterward. Tests that use it run one at a
// time, so overlapping tests cannot restore each other's level incorrectly.
func SetGlobalLogLevel(t testing.TB, level zerolog.Level) {
	t.Helper()
	logLevelMu.Lock()
	previous := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(level)
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(previous)
		logLevelMu.Unlock()
	})
}
