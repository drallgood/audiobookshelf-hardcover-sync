package mismatch

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// lookupCache caches person lookups to avoid duplicate API calls
var (
	personCache   = make(map[string]int) // name -> ID mapping
	personIDCache = make(map[int]string) // ID -> name mapping
	cacheLock     sync.RWMutex
)

// LookupAuthorIDs looks up author IDs by name
func LookupAuthorIDs(ctx context.Context, hc *hardcover.Client, names ...string) ([]int, error) {
	return lookupPeople(ctx, hc, "author", names...)
}

// LookupNarratorIDs looks up narrator IDs by name.
// It can handle multiple names separated by commas in a single string.
func LookupNarratorIDs(ctx context.Context, hc *hardcover.Client, names ...string) ([]int, error) {
	// If we have exactly one name that contains commas, split it
	if len(names) == 1 && strings.Contains(names[0], ",") {
		splitNames := strings.Split(names[0], ",")
		// Trim whitespace from each name
		for i, name := range splitNames {
			splitNames[i] = strings.TrimSpace(name)
		}
		return lookupPeople(ctx, hc, "narrator", splitNames...)
	}
	return lookupPeople(ctx, hc, "narrator", names...)
}

// lookupPeople is a helper function to look up people (authors or narrators) by name
func lookupPeople(ctx context.Context, hc *hardcover.Client, personType string, names ...string) ([]int, error) {
	if hc == nil {
		return nil, fmt.Errorf("hardcover client is required")
	}

	// Get logger from context or create a new one
	log := logger.FromContext(ctx)
	if log == nil {
		log = logger.Get()
	}

	// Use a map to deduplicate IDs
	idMap := make(map[int]bool)
	var ids []int

	for _, name := range names {
		if name == "" {
			continue
		}

		// Check cache first
		cacheKey := fmt.Sprintf("%s:%s", personType, strings.ToLower(name))
		if id, found := getFromCache(cacheKey); found {
			if !idMap[id] {
				idMap[id] = true
				ids = append(ids, id)
			}
			continue
		}

		// Not in cache, search via API
		var people []models.Author
		var err error

		switch personType {
		case "author":
			people, err = hc.SearchAuthors(ctx, name, 5)
		case "narrator":
			people, err = hc.SearchNarrators(ctx, name, 5)
		default:
			return nil, fmt.Errorf("invalid person type: %s", personType)
		}

		if err != nil {
			log.Error(fmt.Sprintf("Failed to search for %s", personType), map[string]interface{}{
				"name":  name,
				"error": err.Error(),
			})
			continue
		}

		// Take only the first result (already sorted by book count)
		if len(people) > 0 {
			person := people[0]
			// Convert ID to int
			personID, err := strconv.Atoi(person.ID)
			if err != nil {
				log.Error("Invalid person ID from API", map[string]interface{}{
					"id":    person.ID,
					"error": err.Error(),
				})
			} else if !idMap[personID] { // Skip duplicates
				// Add to cache and results
				addToCache(cacheKey, personID, person.Name)
				idMap[personID] = true
				ids = append(ids, personID)

				log.Debug(fmt.Sprintf("Found %s ID (top result by book count)", personType), map[string]interface{}{
					"name":       name,
					"id":         personID,
					"book_count": person.BookCount,
				})
			}
		}
	}

	// If we processed all names but didn't find any matches, log a warning for the last name
	if len(ids) == 0 && len(names) > 0 {
		lastName := names[len(names)-1]
		if lastName != "" {
			log.Warn(fmt.Sprintf("Could not find %s in Hardcover", personType), map[string]interface{}{
				"name": lastName,
			})
		}
	}

	return ids, nil
}

// getFromCache retrieves a person's ID from the cache
func getFromCache(cacheKey string) (int, bool) {
	cacheLock.RLock()
	defer cacheLock.RUnlock()

	if id, ok := personCache[cacheKey]; ok {
		return id, true
	}
	return 0, false
}

// addToCache adds a person's name and ID to the cache
func addToCache(cacheKey string, id int, name string) {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	personCache[cacheKey] = id
	personIDCache[id] = name
}

// GetPersonName returns the name of a person by their ID
func GetPersonName(id int) string {
	cacheLock.RLock()
	defer cacheLock.RUnlock()

	return personIDCache[id]
}
