package mismatch

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// LookupPublisherID looks up a publisher ID by name in Hardcover
func LookupPublisherID(ctx context.Context, hc hardcover.HardcoverClientInterface, name string) (int, error) {
	if hc == nil || name == "" {
		return 0, nil
	}

	// Get logger from context or create a new one
	log := logger.FromContext(ctx)
	if log == nil {
		log = logger.Get()
	}

	// Check cache first
	cacheKey := fmt.Sprintf("publisher:%s", strings.ToLower(name))
	if id, found := getFromCache(cacheKey); found {
		log.Debug("Found publisher ID in cache", map[string]interface{}{
			"name": name,
			"id":   id,
		})
		return id, nil
	}

	// Search for publisher in Hardcover
	log.Debug("Searching for publisher in Hardcover", map[string]interface{}{
		"publisher": name,
	})

	// Search for publishers with a similar name
	publishers, err := hc.SearchPublishers(ctx, name, 5)
	if err != nil {
		log.Error("Failed to search for publisher", map[string]interface{}{
			"name":  name,
			"error": err.Error(),
		})
		return 0, fmt.Errorf("failed to search for publisher: %w", err)
	}

	// If we found matches, use the first one
	if len(publishers) > 0 {
		publisher := publishers[0]
		// Convert ID to int
		publisherID, err := strconv.Atoi(publisher.ID)
		if err != nil {
			log.Error("Invalid publisher ID from API", map[string]interface{}{
				"id":    publisher.ID,
				"error": err.Error(),
			})
			return 0, fmt.Errorf("invalid publisher ID from API: %w", err)
		}

		// Add to cache
		addToCache(cacheKey, publisherID, publisher.Name)

		log.Debug("Found publisher ID", map[string]interface{}{
			"name": name,
			"id":   publisherID,
		})

		return publisherID, nil
	}

	log.Warn("Publisher not found in Hardcover", map[string]interface{}{
		"name": name,
	})

	// Return 0 to indicate no publisher found
	return 0, nil
}
