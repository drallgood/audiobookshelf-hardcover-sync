package hardcover

import (
	"context"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientDryRunSkipsMutations(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	client := &Client{logger: logger.Get()}
	client.SetDryRun(true)
	ctx := context.Background()
	require.NoError(t, client.GraphQLMutation(ctx, "mutation Test { test }", nil, nil))

	insertedID, err := client.InsertUserBookRead(ctx, InsertUserBookReadInput{})
	require.NoError(t, err)
	assert.Zero(t, insertedID)

	updated, err := client.UpdateUserBookRead(ctx, UpdateUserBookReadInput{})
	require.NoError(t, err)
	assert.True(t, updated)

	require.NoError(t, client.DeleteUserBookRead(ctx, 1))
	require.NoError(t, client.UpdateUserBookStatus(ctx, UpdateUserBookStatusInput{}))
	require.NoError(t, client.UpdateUserBookEdition(ctx, 1, 2))

	createdID, err := client.CreateUserBook(ctx, "2", "FINISHED")
	require.NoError(t, err)
	assert.Equal(t, "-1", createdID)

	require.NoError(t, client.MarkEditionAsOwned(ctx, 2))
	require.NoError(t, client.UpdateUserBook(ctx, UpdateUserBookInput{}))
}
