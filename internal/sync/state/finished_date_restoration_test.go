package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinishedDateRestorationPersistsWithoutChangingLegacyCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":"4.0","books":{"item":{"status":"FINISHED","lastProgress":100,"lastUpdated":1}}}`), 0600))
	current, err := LoadState(path)
	require.NoError(t, err)
	assert.False(t, current.HasFinishedDateRestoration("item"))
	dates := map[int64]string{99: "2024-01-01", 1: "2025-08-12", 2: "", 3: "2025-08-12T23:00:00.123-04:00"}
	current.SetFinishedDateRestoration("789", "item", dates)
	dates[1] = "changed"
	require.NoError(t, current.Save(path))
	reloaded, err := LoadState(path)
	require.NoError(t, err)
	got, ok := reloaded.GetFinishedDateRestoration("789")
	require.True(t, ok)
	assert.Equal(t, "2025-08-12", got[1])
	assert.Equal(t, "", got[2])
	assert.Equal(t, "2025-08-12T23:00:00.123-04:00", got[3])
	got[99] = "changed"
	again, _ := reloaded.GetFinishedDateRestoration("789")
	assert.Equal(t, "2024-01-01", again[99])
	checkpoint, ok := reloaded.GetBookState("item")
	require.True(t, ok)
	assert.Equal(t, "FINISHED", checkpoint.Status)
	assert.True(t, reloaded.HasFinishedDateRestoration("item"))
	reloaded.ClearFinishedDateRestoration("789")
	require.NoError(t, reloaded.Save(path))
	cleared, err := LoadState(path)
	require.NoError(t, err)
	assert.False(t, cleared.HasFinishedDateRestoration("item"))
}
