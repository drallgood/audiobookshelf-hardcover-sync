package database

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncConfigOwnershipRecheckDaysPreservesOmittedAndExplicitZero(t *testing.T) {
	var legacy SyncConfigData
	require.NoError(t, json.Unmarshal([]byte(`{"sync_owned":true}`), &legacy))
	require.Nil(t, legacy.OwnershipRecheckDays)
	require.False(t, legacy.IsEmpty())

	var explicitZero SyncConfigData
	require.NoError(t, json.Unmarshal([]byte(`{"ownership_recheck_days":0}`), &explicitZero))
	require.NotNil(t, explicitZero.OwnershipRecheckDays)
	require.Equal(t, 0, *explicitZero.OwnershipRecheckDays)
	require.False(t, explicitZero.IsEmpty(), "explicit zero must be treated as a profile update")

	encodedLegacy, err := json.Marshal(SyncConfigData{})
	require.NoError(t, err)
	require.NotContains(t, string(encodedLegacy), "ownership_recheck_days")
	encodedZero, err := json.Marshal(explicitZero)
	require.NoError(t, err)
	require.Contains(t, string(encodedZero), `"ownership_recheck_days":0`)
}

func TestSyncConfigRejectsNullOwnershipRecheckDays(t *testing.T) {
	var syncConfig SyncConfigData
	require.ErrorContains(t,
		json.Unmarshal([]byte(`{"ownership_recheck_days":null}`), &syncConfig),
		`sync config field "ownership_recheck_days" cannot be null`,
	)
}
