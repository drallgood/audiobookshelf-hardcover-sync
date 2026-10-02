package sync

import (
	"bytes"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/testutils"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type dailyQuotaPausedHardcoverClient struct {
	*MockHardcoverClient
	paused bool
}

func (c *dailyQuotaPausedHardcoverClient) DailyQuotaPaused() bool {
	return c.paused
}

func TestDebugRequestIntentObservesDailyQuotaPause(t *testing.T) {
	testutils.SetGlobalLogLevel(t, zerolog.DebugLevel)

	tests := []struct {
		name      string
		client    HardcoverSyncClient
		wantEvent bool
	}{
		{
			name:      "unpaused client emits intent",
			client:    &dailyQuotaPausedHardcoverClient{MockHardcoverClient: &MockHardcoverClient{}},
			wantEvent: true,
		},
		{
			name:      "paused client suppresses intent",
			client:    &dailyQuotaPausedHardcoverClient{MockHardcoverClient: &MockHardcoverClient{}, paused: true},
			wantEvent: false,
		},
		{
			name:      "client without pause capability emits intent",
			client:    &MockHardcoverClient{},
			wantEvent: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			log := &logger.Logger{Logger: zerolog.New(&output).Level(zerolog.DebugLevel)}
			service := &Service{hardcover: test.client}

			service.debugRequestIntent(log, "test intent event", map[string]interface{}{"request_marker": "caller-provided-marker"})

			if test.wantEvent {
				require.Contains(t, output.String(), `"message":"test intent event"`)
				require.Contains(t, output.String(), `"request_marker":"caller-provided-marker"`)
			} else {
				require.Empty(t, output.String())
			}
		})
	}
}
