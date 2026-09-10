package sync

import (
	"context"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// dryRunHardcoverClient prevents every Hardcover mutation while preserving
// read operations so a dry run can still report what the sync would change.
type dryRunHardcoverClient struct {
	hardcover.HardcoverClientInterface
}

var _ hardcover.HardcoverClientInterface = (*dryRunHardcoverClient)(nil)

func (c *dryRunHardcoverClient) logSkippedMutation(operation string) {
	logger.Get().Info("[DRY-RUN] Skipping Hardcover mutation", map[string]interface{}{
		"operation": operation,
	})
}

func (c *dryRunHardcoverClient) MarkEditionAsOwned(context.Context, int) error {
	c.logSkippedMutation("MarkEditionAsOwned")
	return nil
}

func (c *dryRunHardcoverClient) InsertUserBookRead(context.Context, hardcover.InsertUserBookReadInput) (int, error) {
	c.logSkippedMutation("InsertUserBookRead")
	return 0, nil
}

func (c *dryRunHardcoverClient) UpdateUserBookRead(context.Context, hardcover.UpdateUserBookReadInput) (bool, error) {
	c.logSkippedMutation("UpdateUserBookRead")
	return true, nil
}

func (c *dryRunHardcoverClient) DeleteUserBookRead(context.Context, int64) error {
	c.logSkippedMutation("DeleteUserBookRead")
	return nil
}

func (c *dryRunHardcoverClient) UpdateUserBookStatus(context.Context, hardcover.UpdateUserBookStatusInput) error {
	c.logSkippedMutation("UpdateUserBookStatus")
	return nil
}

func (c *dryRunHardcoverClient) UpdateUserBookEdition(context.Context, int, int) error {
	c.logSkippedMutation("UpdateUserBookEdition")
	return nil
}

func (c *dryRunHardcoverClient) CreateUserBook(context.Context, string, string) (string, error) {
	c.logSkippedMutation("CreateUserBook")
	return "-1", nil
}
