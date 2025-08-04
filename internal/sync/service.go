package sync

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

// Error definitions
var (
	ErrSkippedBook = errors.New("book was skipped")
)

// progressUpdateInfo stores information about the last progress update for a book
type progressUpdateInfo struct {
	timestamp time.Time
	progress  float64
}

// Service handles the synchronization between Audiobookshelf and Hardcover
type Service struct {
	audiobookshelf      audiobookshelf.AudiobookshelfClientInterface
	hardcover           hardcover.HardcoverClientInterface
	config              *Config
	log                 *logger.Logger
	state               *state.State
	statePath           string
	lastProgressUpdates map[string]progressUpdateInfo // Cache of last progress updates
	lastProgressMutex   sync.RWMutex                  // Mutex to protect the cache
	asinCache           map[string]*models.HardcoverBook // Cache for ASIN lookups (in-memory)
	asinCacheMutex      sync.RWMutex                     // Mutex to protect ASIN cache
	persistentCache     *PersistentASINCache             // Persistent ASIN cache across runs
	userBookCache       *PersistentUserBookCache         // Persistent user book cache
}

// Config is the configuration type for the sync service
type Config = config.Config

// NewService creates a new sync service
func NewService(absClient *audiobookshelf.Client, hcClient hardcover.HardcoverClientInterface, cfg *Config) (*Service, error) {
	svc := &Service{
		audiobookshelf:      absClient,
		hardcover:           hcClient,
		config:              cfg,
		log:                 logger.Get(),
		statePath:           cfg.Sync.StateFile,
		lastProgressUpdates: make(map[string]progressUpdateInfo),
		asinCache:           make(map[string]*models.HardcoverBook),
		persistentCache:     NewPersistentASINCache(cfg.Paths.CacheDir),
		userBookCache:       NewPersistentUserBookCache(cfg.Paths.CacheDir),
	}

	// Migrate old state file if it exists
	_, err := state.MigrateOldState("", svc.statePath)
	if err != nil {
		svc.log.Error("Failed to migrate old state file", map[string]interface{}{
			"error": err,
		})
		return nil, fmt.Errorf("failed to migrate old state: %w", err)
	}

	// Load or create state
	svc.state, err = state.LoadState(svc.statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	// Load persistent ASIN cache
	if err := svc.persistentCache.Load(); err != nil {
		svc.log.Warn("Failed to load persistent ASIN cache, starting with empty cache", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		total, successful, failed := svc.persistentCache.Stats()
		svc.log.Info("Loaded persistent ASIN cache", map[string]interface{}{
			"total_entries":      total,
			"successful_lookups": successful,
			"failed_lookups":     failed,
		})
	}



	// Load persistent user book cache
	if err := svc.userBookCache.Load(); err != nil {
		svc.log.Warn("Failed to load persistent user book cache, starting with empty cache", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		total, successful, failed := svc.userBookCache.Stats()
		svc.log.Info("Loaded persistent user book cache", map[string]interface{}{
			"total_entries":      total,
			"successful_lookups": successful,
			"failed_lookups":     failed,
		})
	}

	return svc, nil
}

// getASINFromCache retrieves a cached ASIN lookup result
// Checks in-memory cache first, then persistent cache
func (s *Service) getASINFromCache(asin string) (*models.HardcoverBook, bool) {
	// Check in-memory cache first (fastest)
	s.asinCacheMutex.RLock()
	book, exists := s.asinCache[asin]
	s.asinCacheMutex.RUnlock()
	
	if exists {
		return book, true
	}
	
	// Check persistent cache
	book, exists = s.persistentCache.Get(asin)
	if exists {
		// Promote to in-memory cache for faster access
		s.asinCacheMutex.Lock()
		s.asinCache[asin] = book
		s.asinCacheMutex.Unlock()
		
		s.log.Debug("Promoted ASIN from persistent to in-memory cache", map[string]interface{}{
			"asin": asin,
		})
	}
	
	return book, exists
}

// setASINInCache stores an ASIN lookup result in both caches
func (s *Service) setASINInCache(asin string, book *models.HardcoverBook) {
	// Store in in-memory cache
	s.asinCacheMutex.Lock()
	s.asinCache[asin] = book
	s.asinCacheMutex.Unlock()
	
	// Store in persistent cache
	s.persistentCache.Set(asin, book)
}

// clearASINCache clears only the in-memory ASIN cache (persistent cache remains)
func (s *Service) clearASINCache() {
	s.asinCacheMutex.Lock()
	defer s.asinCacheMutex.Unlock()
	
	s.asinCache = make(map[string]*models.HardcoverBook)
	s.log.Debug("Cleared in-memory ASIN cache for new sync (persistent cache preserved)", nil)
}

// logASINCacheStats logs ASIN cache performance statistics
func (s *Service) logASINCacheStats() {
	s.asinCacheMutex.RLock()
	// Get persistent cache stats
	total, successful, failed := s.persistentCache.Stats()

	// Calculate potential API call savings
	potentialSavings := 0
	for _, book := range s.asinCache {
		if book != nil {
			potentialSavings++
		}
	}
	s.asinCacheMutex.RUnlock()

	s.log.Info("ASIN Cache Performance Statistics", map[string]interface{}{
		"total_cached_asins":    total,
		"successful_lookups":    successful,
		"failed_lookups":        failed,
		"cache_hit_potential":   fmt.Sprintf("Avoided up to %d duplicate API calls", potentialSavings),
	})
}


// getUserBookFromCache retrieves a cached user book by user_book_id
func (s *Service) getUserBookFromCache(userBookID int) (*models.HardcoverBook, bool) {
	return s.userBookCache.GetByUserBook(userBookID)
}

// setUserBookByUserBookInCache stores a cached user book by user_book_id
func (s *Service) setUserBookByUserBookInCache(userBookID int, userBook *models.HardcoverBook) {
	s.userBookCache.SetByUserBook(userBookID, userBook)
}

// findOrCreateUserBookID finds or creates a user book ID for the given edition ID and status
func (s *Service) findOrCreateUserBookID(ctx context.Context, editionID, status string) (int64, error) {
	s.log.Debug("Starting findOrCreateUserBookID", map[string]interface{}{
		"editionID": editionID,
		"status":    status,
	})

	editionIDInt, err := strconv.ParseInt(editionID, 10, 64)
	if err != nil {
		errMsg := fmt.Sprintf("invalid edition ID format: %v", err)
		s.log.Error(errMsg, map[string]interface{}{
			"editionID": editionID,
			"error":     err.Error(),
		})
		return 0, fmt.Errorf("invalid edition ID format: %w", err)
	}

	// Create a logger with context
	logCtx := s.log.With(map[string]interface{}{
		"editionID":    editionID,
		"editionIDInt": editionIDInt,
	})

	// Check if we already have a user book ID for this edition
	logCtx.Debug("Checking for existing user book ID", map[string]interface{}{
		"editionID":    editionID,
		"editionIDInt": editionIDInt,
	})

	userBookID, err := s.hardcover.GetUserBookID(context.Background(), int(editionIDInt))
	if err != nil {
		errMsg := fmt.Sprintf("Error checking for existing user book ID: %v", err)
		logCtx.Error(errMsg, map[string]interface{}{
			"error":     err,
			"editionID": editionID,
		})
		return 0, fmt.Errorf("error checking for existing user book ID: %w", err)
	}

	// If we found an existing user book ID, return it
	if userBookID > 0 {
		s.log.Info("Found existing user book ID", map[string]interface{}{
			"editionID":    editionID,
			"editionIDInt": editionIDInt,
			"userBookID":   userBookID,
		})
		return int64(userBookID), nil
	}

	s.log.Debug("No existing user book ID found, will create new one", map[string]interface{}{
		"editionID": editionID,
	})

	// If dry-run mode is enabled, log and return early without creating
	if s.config.App.DryRun {
		dryRunMsg := fmt.Sprintf("[DRY-RUN] Would create new user book with status: %s", status)
		logCtx.Info(dryRunMsg, map[string]interface{}{
			"status": status,
		})
		// Return a negative value to indicate dry-run mode
		return -1, nil
	}

	logCtx.Info("Creating new user book with status", map[string]interface{}{
		"status": status,
	})

	logCtx.Info("Creating new user book with status", map[string]interface{}{
		"status": status,
	})

	// Double-check if the user book exists to prevent race conditions
	logCtx.Debug("Performing second check for existing user book ID to prevent race conditions", nil)
	
	userBookID, err = s.hardcover.GetUserBookID(context.Background(), int(editionIDInt))
	if err != nil {
		errMsg := fmt.Sprintf("Error in second check for existing user book ID: %v", err)
		logCtx.Error(errMsg, map[string]interface{}{
			"error":     err,
			"editionID": editionID,
		})
		return 0, fmt.Errorf("error in second check for existing user book ID: %w", err)
	}

	// If we found an existing user book ID in the second check, return it
	if userBookID > 0 {
		s.log.Info("Found existing user book ID in second check", map[string]interface{}{
			"editionID":    editionID,
			"editionIDInt": editionIDInt,
			"userBookID":   userBookID,
		})
		return int64(userBookID), nil
	}

	// Create a new user book with the specified status
	logCtx.Info("Attempting to create new user book", map[string]interface{}{
		"status": status,
	})

	newUserBookID, err := s.hardcover.CreateUserBook(ctx, editionID, status)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to create user book: %v", err)
		s.log.Error(errMsg, map[string]interface{}{
			"error":     err,
			"editionID": editionIDInt,
			"status":    status,
		})
		return 0, fmt.Errorf("failed to create user book: %w", err)
	}

	// Convert the new user book ID to an integer64
	userBookID64, err := strconv.ParseInt(newUserBookID, 10, 64)
	if err != nil {
		s.log.Error("Invalid user book ID format", map[string]interface{}{
			"error":      err,
			"userBookID": newUserBookID,
		})
		return 0, fmt.Errorf("invalid user book ID format: %w", err)
	}

	s.log.Info("Successfully created new user book with status", map[string]interface{}{
		"editionID":  editionIDInt,
		"userBookID": userBookID64,
		"status":     status,
	})

	return userBookID64, nil
}

// Sync performs a full synchronization between Audiobookshelf and Hardcover
func (s *Service) Sync(ctx context.Context) error {
	// Clear any existing mismatches at the start of each sync cycle
	// This prevents accumulation of resolved mismatches in continuous sync mode
	mismatch.Clear()
	s.log.Info("Cleared previous mismatches at start of sync cycle", nil)
	
	// Clear ASIN cache to ensure fresh lookups for this sync run
	s.clearASINCache()

	// Log the start of the sync
	s.log.Info("========================================", map[string]interface{}{
		"dry_run":          s.config.App.DryRun,
		"test_book_filter": s.config.App.TestBookFilter,
		"test_book_limit":  s.config.App.TestBookLimit,
	})
	s.log.Info("STARTING FULL SYNCHRONIZATION", nil)
	s.log.Info("========================================", nil)

	// Update the last sync start time
	s.state.UpdateLibrary("sync") // Using "sync" as a special library ID for global sync state

	// Log service configuration (without accessing unexported fields directly)
	s.log.Info("SYNC CONFIGURATION", nil)
	s.log.Info("========================================", nil)
	s.log.Info("Audiobookshelf Configuration", map[string]interface{}{
		"audiobookshelf_url":       s.config.Audiobookshelf.URL,
		"has_audiobookshelf_token": s.config.Audiobookshelf.Token != "",
	})

	s.log.Info("Hardcover Configuration", map[string]interface{}{
		"has_hardcover_token": s.config.Hardcover.Token != "",
	})

	s.log.Info("Sync Settings", map[string]interface{}{
		"minimum_progress":  s.config.App.MinimumProgress,
		"sync_want_to_read": s.config.App.SyncWantToRead,
		"sync_owned":        s.config.App.SyncOwned,
	})

	s.log.Info("========================================", nil)

	// Initialize the total books limit from config
	totalBooksLimit := s.config.App.TestBookLimit

	// Log configuration for debugging
	s.log.Debug("Configuration for debugging", map[string]interface{}{
		"audiobookshelf_url":       s.config.Audiobookshelf.URL,
		"has_audiobookshelf_token": s.config.Audiobookshelf.Token != "",
		"has_hardcover_token":      s.config.Hardcover.Token != "",
		"minimum_progress":         s.config.App.MinimumProgress,
		"sync_want_to_read":        s.config.App.SyncWantToRead,
		"test_book_limit":          totalBooksLimit,
	})

	// Fetch user progress data from Audiobookshelf
	s.log.Info("Fetching user progress data from Audiobookshelf...", nil)
	userProgress, err := s.audiobookshelf.GetUserProgress(ctx)
	if err != nil {
		s.log.Warn("Failed to fetch user progress data, falling back to basic progress tracking", map[string]interface{}{
			"error": err,
		})
	} else {
		s.log.Info("Fetched user progress data", map[string]interface{}{
			"media_progress_items": len(userProgress.MediaProgress),
			"listening_sessions":   len(userProgress.ListeningSessions),
		})
	}

	// Get all libraries from Audiobookshelf
	s.log.Info("Fetching libraries from Audiobookshelf...", nil)
	libraries, err := s.audiobookshelf.GetLibraries(ctx)
	if err != nil {
		s.log.Error("Failed to fetch libraries", map[string]interface{}{
			"error": err,
		})
		return fmt.Errorf("failed to fetch libraries: %w", err)
	}

	s.log.Info("Found libraries", map[string]interface{}{
		"libraries_count": len(libraries),
	})

	// Filter libraries based on configuration
	filteredLibraries := make([]audiobookshelf.AudiobookshelfLibrary, 0, len(libraries))
	skippedLibraries := make([]string, 0)
	for _, library := range libraries {
		if s.shouldSyncLibrary(&library) {
			filteredLibraries = append(filteredLibraries, library)
		} else {
			skippedLibraries = append(skippedLibraries, library.Name)
		}
	}

	// Log library filtering results
	if len(s.config.Sync.Libraries.Include) > 0 || len(s.config.Sync.Libraries.Exclude) > 0 {
		s.log.Info("Library filtering applied", map[string]interface{}{
			"total_libraries":    len(libraries),
			"filtered_libraries": len(filteredLibraries),
			"skipped_libraries":  len(skippedLibraries),
			"include_filter":     s.config.Sync.Libraries.Include,
			"exclude_filter":     s.config.Sync.Libraries.Exclude,
		})
		if len(skippedLibraries) > 0 {
			s.log.Info("Skipped libraries", map[string]interface{}{
				"skipped_library_names": skippedLibraries,
			})
		}
	} else {
		s.log.Info("No library filtering configured, processing all libraries", nil)
		filteredLibraries = libraries
	}

	// Track total books processed across all libraries
	totalBooksProcessed := 0

	// Log the test book limit if it's set
	if totalBooksLimit > 0 {
		s.log.Info("Test book limit is active", map[string]interface{}{
			"test_book_limit": totalBooksLimit,
		})
	}

	// Process each filtered library
	for i := range filteredLibraries {
		// Skip processing if we've reached the limit
		if totalBooksLimit > 0 && totalBooksProcessed >= totalBooksLimit {
			s.log.Info("Reached test book limit before processing library", map[string]interface{}{
				"limit":             totalBooksLimit,
				"already_processed": totalBooksProcessed,
			})
			break
		}

		// Process the library and get the number of books processed
		processed, err := s.processLibrary(ctx, &filteredLibraries[i], totalBooksLimit-totalBooksProcessed, userProgress)
		if err != nil {
			s.log.Error("Failed to process library", map[string]interface{}{
				"error":      err,
				"library_id": filteredLibraries[i].ID,
			})
			continue
		}

		totalBooksProcessed += processed

		// Log progress
		if totalBooksLimit > 0 {
			s.log.Info("Progress towards test book limit", map[string]interface{}{
				"processed": totalBooksProcessed,
				"limit":     totalBooksLimit,
			})

			if totalBooksProcessed >= totalBooksLimit {
				s.log.Info("Successfully reached test book limit, stopping processing", map[string]interface{}{
					"limit":     totalBooksLimit,
					"processed": totalBooksProcessed,
				})
				break
			}
		}
	}

	// Save any mismatches that occurred during sync
	if err := mismatch.SaveToFile(ctx, s.hardcover, "", s.config); err != nil {
		s.log.Error("Failed to save mismatch files", map[string]interface{}{
			"error": err,
		})
		// Don't return error here as the sync itself completed successfully
	}

	// Update the last sync time
	s.state.SetFullSync()

	// Save the state
	if err := s.state.Save(s.statePath); err != nil {
		s.log.Error("Failed to save sync state", map[string]interface{}{
			"error": err.Error(),
		})
		// Don't return the error here as the sync itself was successful
	}

	// Log ASIN cache performance statistics
	s.logASINCacheStats()

	// Save persistent ASIN cache
	if err := s.persistentCache.Save(); err != nil {
		s.log.Warn("Failed to save persistent ASIN cache", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		s.log.Debug("Saved persistent ASIN cache", nil)
	}



	// Save persistent user book cache
	if err := s.userBookCache.Save(); err != nil {
		s.log.Warn("Failed to save persistent user book cache", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		s.log.Debug("Saved persistent user book cache", nil)
	}

	s.log.Info("Sync completed successfully", nil)
	return nil
}

// shouldSyncLibrary determines if a library should be synced based on configuration
func (s *Service) shouldSyncLibrary(library *audiobookshelf.AudiobookshelfLibrary) bool {
	// If include list is specified, only sync libraries in the include list
	if len(s.config.Sync.Libraries.Include) > 0 {
		for _, included := range s.config.Sync.Libraries.Include {
			// Match by name (case-insensitive) or ID
			if strings.EqualFold(included, library.Name) || included == library.ID {
				return true
			}
		}
		// If include list is specified but library is not in it, don't sync
		return false
	}

	// If exclude list is specified, don't sync libraries in the exclude list
	if len(s.config.Sync.Libraries.Exclude) > 0 {
		for _, excluded := range s.config.Sync.Libraries.Exclude {
			// Match by name (case-insensitive) or ID
			if strings.EqualFold(excluded, library.Name) || excluded == library.ID {
				return false
			}
		}
	}

	// Default: sync all libraries if no filtering is configured
	return true
}

// processLibrary processes a library and returns the number of books processed
func (s *Service) processLibrary(ctx context.Context, library *audiobookshelf.AudiobookshelfLibrary, maxBooks int, userProgress *models.AudiobookshelfUserProgress) (int, error) {
	// Create a logger with library context
	libraryLog := s.log.With(map[string]interface{}{
		"library_id":   library.ID,
		"library_name": library.Name,
	})

	libraryLog.Info("Processing library", nil)

	// Get all items from the library
	items, err := s.audiobookshelf.GetLibraryItems(ctx, library.ID)
	if err != nil {
		return 0, fmt.Errorf("failed to get library items: %w", err)
	}

	libraryLog.Info("Found items in library", map[string]interface{}{
		"library_id":   library.ID,
		"library_name": library.Name,
		"items_count":  len(items),
	})

	// If we have a maxBooks limit, apply it
	if maxBooks > 0 && len(items) > maxBooks {
		libraryLog.Info("Limiting number of books to process based on remaining test book limit", map[string]interface{}{
			"original_count": len(items),
			"limit":          maxBooks,
		})
		items = items[:maxBooks]
	}

	// Process each item in the library
	processed := 0
	for _, book := range items {
		// Process the item
		err := s.processBook(ctx, book, userProgress)
		if err != nil {
			// Check if this is ErrSkippedBook - which we still count as processed
			// since we've recorded a mismatch and updated state for these books
			if err == ErrSkippedBook {
				libraryLog.Debug("Book was skipped but counted as processed", map[string]interface{}{
					"item_id": book.ID,
				})
				processed++
			} else {
				// For other errors, log and skip without incrementing processed count
				libraryLog.Error("Failed to process item", map[string]interface{}{
					"error":   err,
					"item_id": book.ID,
				})
			}
			continue
		}

		processed++
	}

	libraryLog.Info("Finished processing library", map[string]interface{}{
		"library_id":   library.ID,
		"library_name": library.Name,
		"processed":    processed,
	})

	return processed, nil
}

// processBook processes a single book and updates its status in Hardcover
func (s *Service) processBook(ctx context.Context, book models.AudiobookshelfBook, userProgress *models.AudiobookshelfUserProgress) error {
	// Create a logger with book context
	bookTitle := book.Media.Metadata.Title
	bookLog := s.log.WithFields(map[string]interface{}{
		"book_id": book.ID,
		"title":   bookTitle,
		"author":  book.Media.Metadata.AuthorName,
	})

	// Apply test book filter if configured - do this before any expensive lookups
	if s.config.App.TestBookFilter != "" {
		// Check if the book title contains the filter string (case-insensitive)
		if !strings.Contains(strings.ToLower(bookTitle), strings.ToLower(s.config.App.TestBookFilter)) {
			bookLog.Debugf("Skipping book as it doesn't match test book filter: %s", s.config.App.TestBookFilter)
			return nil
		}
		bookLog.Debugf("Book matches test book filter, processing: %s", s.config.App.TestBookFilter)
	}

	// Early filtering for incremental sync - check if book needs syncing
	if s.config.Sync.Incremental {
		// Calculate current progress and status
		currentProgress := 0.0
		if book.Media.Duration > 0 {
			currentProgress = book.Progress.CurrentTime / book.Media.Duration
		}
		currentStatus := s.determineBookStatus(currentProgress, book.Progress.IsFinished, book.Progress.FinishedAt)
		
		// Create preliminary state key (we'll update it with edition ID later if found)
		preliminaryStateKey := book.ID
		
		// Check if this book needs syncing based on changes
		minChangeThreshold := float64(s.config.Sync.MinChangeThreshold) / book.Media.Duration // Convert seconds to progress ratio
		if !s.state.NeedsSync(preliminaryStateKey, currentProgress, currentStatus, minChangeThreshold) {
			bookLog.Debug("Skipping book - no significant changes since last sync", map[string]interface{}{
				"current_progress": currentProgress,
				"current_status":   currentStatus,
				"change_threshold": minChangeThreshold,
			})
			return nil
		}
		
		bookLog.Debug("Book needs syncing - changes detected", map[string]interface{}{
			"current_progress": currentProgress,
			"current_status":   currentStatus,
		})
	}

	// Declare variables at the top of the function to avoid redeclaration
	var (
		hcBook   *models.HardcoverBook
		findErr  error
		editionID string
		stateKey string
	)

	// Find the book in Hardcover to get the edition ID
	hcBook, findErr = s.findBookInHardcover(ctx, book)
	if findErr == nil && hcBook != nil && hcBook.EditionID != "" {
		editionID = hcBook.EditionID
	}

	// Create a composite key for state tracking: bookID:editionID
	stateKey = book.ID
	if editionID != "" {
		stateKey = fmt.Sprintf("%s:%s", book.ID, editionID)
	}

	// Add edition info to log context
	bookLog = bookLog.With(map[string]interface{}{
		"state_key": stateKey,
	})

	// Log start of processing
	bookLog.Info("Starting to process book", nil)
	bookLog.Debug("Book details", map[string]interface{}{
		"book_id": book.ID,
		"title":   bookTitle,
		"author":  book.Media.Metadata.AuthorName,
	})

	// Check if we should skip this book based on incremental sync
	if s.config.Sync.Incremental {
		// Calculate current progress
		currentProgress := 0.0
		if book.Media.Duration > 0 && book.Progress.CurrentTime > 0 {
			currentProgress = book.Progress.CurrentTime / book.Media.Duration
		}

		// Get the last sync state for this book using the composite key
		bookState, exists := s.state.Books[stateKey]
		if exists && !s.config.App.DryRun {
			// Check if progress has changed significantly (more than 1%)
			progressChanged := math.Abs(currentProgress-bookState.LastProgress) > 0.01

			// Check if status has changed
			currentStatus := s.determineBookStatus(currentProgress, book.Progress.IsFinished, book.Progress.FinishedAt)
			statusChanged := currentStatus != bookState.Status

			// Check if there's any activity that would require an update
			lastActivity := int64(0)
			if book.Progress.FinishedAt > 0 {
				lastActivity = book.Progress.FinishedAt
			} else if book.Progress.StartedAt > 0 {
				lastActivity = book.Progress.StartedAt
			}
			activityChanged := lastActivity > bookState.LastUpdated

			// If nothing has changed, skip this book
			if !progressChanged && !statusChanged && !activityChanged {
				bookLog.Debug("Skipping unchanged book in incremental sync mode", map[string]interface{}{
					"last_updated":     time.Unix(bookState.LastUpdated, 0).Format(time.RFC3339),
					"last_progress":    bookState.LastProgress,
					"current_progress": currentProgress,
					"last_status":      bookState.Status,
					"current_status":   currentStatus,
				})
				return nil
			}

			bookLog.Debug("Processing changed book", map[string]interface{}{
				"progress_changed": progressChanged,
				"status_changed":   statusChanged,
				"activity_changed": activityChanged,
				"last_progress":    bookState.LastProgress,
				"current_progress": currentProgress,
				"last_status":      bookState.Status,
				"current_status":   currentStatus,
			})
		}
	}

	// Enhance book data with user progress if available
	if userProgress != nil {
		// Try to find matching progress in mediaProgress (most accurate source)
		var bestProgress *struct {
			ID            string  `json:"id"`
			LibraryItemID string  `json:"libraryItemId"`
			UserID        string  `json:"userId"`
			IsFinished    bool    `json:"isFinished"`
			Progress      float64 `json:"progress"`
			CurrentTime   float64 `json:"currentTime"`
			Duration      float64 `json:"duration"`
			StartedAt     int64   `json:"startedAt"`
			FinishedAt    int64   `json:"finishedAt"`
			LastUpdate    int64   `json:"lastUpdate"`
			TimeListening float64 `json:"timeListening"`
		}

		// Find the most recent progress entry for this book
		for i := range userProgress.MediaProgress {
			if userProgress.MediaProgress[i].LibraryItemID == book.ID {
				if bestProgress == nil || userProgress.MediaProgress[i].LastUpdate > bestProgress.LastUpdate {
					bestProgress = &userProgress.MediaProgress[i]
				}
			}
		}

		// If we found progress in mediaProgress, use it
		if bestProgress != nil {
			bookLog = bookLog.With(map[string]interface{}{
				"has_media_progress": true,
				"progress":           bestProgress.Progress,
				"is_finished":        bestProgress.IsFinished,
				"last_update":        bestProgress.LastUpdate,
			})

			// Update book progress with the most accurate data
			book.Progress.CurrentTime = bestProgress.CurrentTime
			book.Progress.IsFinished = bestProgress.IsFinished
			book.Progress.FinishedAt = bestProgress.FinishedAt
			book.Progress.StartedAt = bestProgress.StartedAt

			bookLog.Debug("Using enhanced progress from media progress data", map[string]interface{}{
				"current_time": book.Progress.CurrentTime,
				"finished_at":  book.Progress.FinishedAt,
			})
		} else {
			// Fall back to listening sessions if no media progress found
			var bestSession *struct {
				ID            string `json:"id"`
				UserID        string `json:"userId"`
				LibraryItemID string `json:"libraryItemId"`
				MediaType     string `json:"mediaType"`
				MediaMetadata struct {
					Title  string `json:"title"`
					Author string `json:"author"`
				} `json:"mediaMetadata"`
				Duration    float64 `json:"duration"`
				CurrentTime float64 `json:"currentTime"`
				Progress    float64 `json:"progress"`
				IsFinished  bool    `json:"isFinished"`
				StartedAt   int64   `json:"startedAt"`
				UpdatedAt   int64   `json:"updatedAt"`
			}

			// Find the most recent listening session for this book
			for i := range userProgress.ListeningSessions {
				session := &userProgress.ListeningSessions[i]
				if session.LibraryItemID == book.ID &&
					(bestSession == nil || session.UpdatedAt > bestSession.UpdatedAt) {
					bestSession = session
				}
			}

			if bestSession != nil {
				bookLog = bookLog.WithFields(map[string]interface{}{
					"has_session_progress": true,
					"session_progress":     bestSession.Progress,
					"session_finished":     bestSession.IsFinished,
					"session_updated":      bestSession.UpdatedAt,
				})

				book.Progress.CurrentTime = bestSession.CurrentTime
				book.Progress.IsFinished = bestSession.IsFinished

				bookLog.Debug("Using progress from listening session", map[string]interface{}{
					"current_time": book.Progress.CurrentTime,
				})
			} else {
				bookLog.Debug("No enhanced progress data found in /api/me response", nil)
			}
		}
	}

	// Skip books that haven't been started
	if book.Progress.CurrentTime <= 0 {
		bookLog.Info("Skipping unstarted book", map[string]interface{}{
			"current_time": book.Progress.CurrentTime,
		})
		return nil
	}

	// Calculate progress percentage based on current time and total duration
	var progress float64
	if book.Media.Duration > 0 && book.Progress.CurrentTime > 0 {
		progress = book.Progress.CurrentTime / book.Media.Duration
	}

	// Update logger with progress information
	bookLog = bookLog.With(map[string]interface{}{
		"progress":       progress,
		"current_time":   book.Progress.CurrentTime,
		"total_duration": book.Media.Duration,
		"is_finished":    book.Progress.IsFinished,
		"finished_at":    book.Progress.FinishedAt,
	})

	bookLog.Debug("Calculated book progress", nil)

	// Skip books below minimum progress threshold
	if progress < s.config.App.MinimumProgress && progress > 0 {
		bookLog.Info("Skipping book below minimum progress threshold", map[string]interface{}{
			"progress":         progress,
			"minimum_progress": s.config.App.MinimumProgress,
		})
		return nil
	}

	// Determine the target status for the book after enhancing progress data
	targetStatus := s.determineBookStatus(progress, book.Progress.IsFinished, book.Progress.FinishedAt)

	// Log what we're going to do (regardless of dry-run)
	action := "skip"
	switch targetStatus {
	case "FINISHED":
		action = "mark as FINISHED"
	case "IN_PROGRESS":
		action = "update reading progress"
	case "WANT_TO_READ":
		if s.config.App.SyncWantToRead {
			action = "mark as WANT_TO_READ"
		} else {
			action = "skip (WANT_TO_READ sync disabled)"
		}
	}

	// Add optional fields to the logger
	logFields := map[string]interface{}{
		"action":       action,
		"status":       targetStatus,
		"progress":     progress,
		"current_time": book.Progress.CurrentTime,
		"duration":     book.Media.Duration,
		"is_finished":  book.Progress.IsFinished,
		"finished_at":  book.Progress.FinishedAt,
	}

	// Add optional fields if they exist
	if book.Media.Metadata.ASIN != "" {
		logFields["asin"] = book.Media.Metadata.ASIN
	}
	if book.Media.Metadata.ISBN != "" {
		logFields["isbn"] = book.Media.Metadata.ISBN
	}

	// Log the planned action with progress details
	bookLog.Info("Processing book with calculated progress", logFields)

	// Use the target status determined earlier
	status := targetStatus

	bookLog.Debug("Determined book status", map[string]interface{}{
		"is_finished": book.Progress.IsFinished,
		"finished_at": book.Progress.FinishedAt,
		"progress":    progress,
		"status":      status,
	})

	// Find the book in Hardcover
	hcBook, findErr = s.findBookInHardcover(ctx, book)
	if findErr != nil {
		errMsg := "error finding book in Hardcover"
		bookLog.Error("Error finding book in Hardcover, skipping", map[string]interface{}{
			"error": findErr,
		})

		// Build cover URL if cover path is available
		coverURL := ""
		if book.Media.CoverPath != "" {
			coverURL = fmt.Sprintf("%s/api/items/%s/cover", s.config.Audiobookshelf.URL, book.ID)
		}

		// Get book ID from hcBook if available, otherwise try to get from BookError
		bookID := ""
		editionID := ""
		if hcBook != nil {
			bookID = hcBook.ID
			editionID = hcBook.EditionID
		} else {
			// Check if this is a BookError with a book ID
			var bookErr *hardcover.BookError
			if findErr != nil && errors.As(findErr, &bookErr) && bookErr.BookID != "" {
				bookID = bookErr.BookID
				bookLog.Info("Found book ID in BookError", map[string]interface{}{
					"book_id": bookID,
					"error":   bookErr.Error(),
				})
			}
		}

		// Record mismatch with error details
		mismatch.AddWithMetadata(
			mismatch.MediaMetadata{
				Title:         book.Media.Metadata.Title,
				Subtitle:      book.Media.Metadata.Subtitle,
				AuthorName:    book.Media.Metadata.AuthorName,
				NarratorName:  book.Media.Metadata.NarratorName,
				Publisher:     book.Media.Metadata.Publisher,
				PublishedYear: book.Media.Metadata.PublishedYear,
				ISBN:          book.Media.Metadata.ISBN,
				ASIN:          book.Media.Metadata.ASIN,
				CoverURL:      coverURL,
				Duration:      book.Media.Duration,
			},
			bookID,    // Use the book ID if available
			editionID, // Use the edition ID if available
			fmt.Sprintf("%s: %v", errMsg, findErr),
			book.Media.Duration,
			book.ID,
			s.hardcover, // Pass the Hardcover client for publisher lookup
		)

		// Calculate progress percentage if we have duration
		progressPct := 0.0
		if book.Media.Duration > 0 && book.Progress.CurrentTime > 0 {
			progressPct = (book.Progress.CurrentTime / book.Media.Duration) * 100
		}
		if updated := s.state.UpdateBook(book.ID, progressPct, "SKIPPED"); updated {
			bookLog.Debug("Updated book state to SKIPPED", map[string]interface{}{
				"progress": progressPct,
			})
		}
		return ErrSkippedBook // Skip this book but continue with others
	}

	// Check if book was found but has no edition ID
	if hcBook != nil && hcBook.EditionID == "" {
		errMsg := "book found by title/author search but no edition ID available"
		bookLog.Warn(errMsg, map[string]interface{}{
			"book_id": hcBook.ID,
			"title":   hcBook.Title,
		})

		// Build cover URL if cover path is available
		coverURL := ""
		if book.Media.CoverPath != "" {
			coverURL = fmt.Sprintf("%s/api/items/%s/cover", s.config.Audiobookshelf.URL, book.ID)
		}

		// Record mismatch for book without edition
		mismatch.AddWithMetadata(
			mismatch.MediaMetadata{
				Title:         book.Media.Metadata.Title,
				Subtitle:      book.Media.Metadata.Subtitle,
				AuthorName:    book.Media.Metadata.AuthorName,
				NarratorName:  book.Media.Metadata.NarratorName,
				Publisher:     book.Media.Metadata.Publisher,
				PublishedYear: book.Media.Metadata.PublishedYear,
				ISBN:          book.Media.Metadata.ISBN,
				ASIN:          book.Media.Metadata.ASIN,
				CoverURL:      coverURL,
				Duration:      book.Media.Duration,
			},
			hcBook.ID, // Use the book ID we found
			"",        // No edition ID
			errMsg,
			book.Media.Duration,
			book.ID,
			s.hardcover, // Pass the Hardcover client for publisher lookup
		)

		// Update the state to track this book with current progress
		progressPct := 0.0
		if book.Media.Duration > 0 && book.Progress.CurrentTime > 0 {
			progressPct = (book.Progress.CurrentTime / book.Media.Duration) * 100
		}
		if updated := s.state.UpdateBook(book.ID, progressPct, "NO_EDITION"); updated {
			bookLog.Debug("Updated book state to NO_EDITION", map[string]interface{}{
				"progress": progressPct,
			})
		}
		return ErrSkippedBook
	}

	if hcBook == nil {
		errMsg := "could not find book in Hardcover"
		if book.Media.Metadata.Title == "" {
			errMsg = "book title is empty, cannot search by title/author"
		}
		bookLog.Error(errMsg, map[string]interface{}{
			"book_id": book.ID,
			"title":   book.Media.Metadata.Title,
			"author":  book.Media.Metadata.AuthorName,
			"isbn":    book.Media.Metadata.ISBN,
			"asin":    book.Media.Metadata.ASIN,
		})

		// Build cover URL if cover path is available
		coverURL := ""
		if book.Media.CoverPath != "" {
			coverURL = fmt.Sprintf("%s/api/items/%s/cover", s.config.Audiobookshelf.URL, book.ID)
		}

		// Initialize with empty IDs since hcBook is nil
		bookID := ""
		editionID := ""

		// Record mismatch for book not found
		mismatch.AddWithMetadata(
			mismatch.MediaMetadata{
				Title:         book.Media.Metadata.Title,
				Subtitle:      book.Media.Metadata.Subtitle,
				AuthorName:    book.Media.Metadata.AuthorName,
				NarratorName:  book.Media.Metadata.NarratorName,
				Publisher:     book.Media.Metadata.Publisher,
				PublishedYear: book.Media.Metadata.PublishedYear,
				ISBN:          book.Media.Metadata.ISBN,
				ASIN:          book.Media.Metadata.ASIN,
				CoverURL:      coverURL,
				Duration:      book.Media.Duration,
			},
			bookID,    // Use the book ID if available
			editionID, // Use the edition ID if available
			errMsg,
			book.Media.Duration,
			book.ID,
			s.hardcover, // Pass the Hardcover client for publisher lookup
		)

		// Update state with current progress before returning
		progressPct := 0.0
		if book.Media.Duration > 0 && book.Progress.CurrentTime > 0 {
			progressPct = (book.Progress.CurrentTime / book.Media.Duration) * 100
		}
		if updated := s.state.UpdateBook(book.ID, progressPct, "NOT_FOUND"); updated {
			bookLog.Debug("Updated book state to NOT_FOUND", map[string]interface{}{
				"progress": progressPct,
			})
		}
		return nil
	}

	// Get or create a user book ID for this edition
	editionID = hcBook.EditionID
	if editionID == "" {
		editionID = hcBook.ID // Fall back to book ID if no edition ID
	}

	// Skip if we still don't have a valid edition ID
	if editionID == "" {
		errMsg := "no edition ID or book ID available"
		bookLog.Warn("Skipping user book ID creation: "+errMsg, nil)

		// Build cover URL if cover path is available
		coverURL := ""
		if book.Media.CoverPath != "" {
			coverURL = fmt.Sprintf("%s/api/items/%s/cover", s.config.Audiobookshelf.URL, book.ID)
		}

		// Get book ID from error if available
		var bookID string
		var bookErr *hardcover.BookError
		if findErr != nil && errors.As(findErr, &bookErr) && bookErr.BookID != "" {
			bookID = bookErr.BookID
			bookLog.Info("Found book ID in BookError for missing edition", map[string]interface{}{
				"book_id": bookID,
			})
		} else if hcBook != nil {
			bookID = hcBook.ID
		}

		// Record mismatch with error details
		mismatch.AddWithMetadata(
			mismatch.MediaMetadata{
				Title:         book.Media.Metadata.Title,
				Subtitle:      book.Media.Metadata.Subtitle,
				AuthorName:    book.Media.Metadata.AuthorName,
				NarratorName:  book.Media.Metadata.NarratorName,
				Publisher:     book.Media.Metadata.Publisher,
				PublishedYear: book.Media.Metadata.PublishedYear,
				ISBN:          book.Media.Metadata.ISBN,
				ASIN:          book.Media.Metadata.ASIN,
				CoverURL:      coverURL,
				Duration:      book.Media.Duration,
			},
			bookID,    // Use the book ID from BookError if available
			editionID, // Empty since we don't have an edition ID
			errMsg,
			book.Media.Duration,
			book.ID,
			s.hardcover, // Pass the Hardcover client for publisher lookup
		)
		return nil
	}

	// Log the edition ID we're using
	bookLog.Debug("Looking up or creating user book ID for edition", map[string]interface{}{
		"edition_id": editionID,
	})

	// Find or create a user book ID for this edition with the determined status
	userBookID, err := s.findOrCreateUserBookID(ctx, editionID, status)
	if err != nil {
		bookLog.Error("Failed to get or create user book ID", map[string]interface{}{
			"error":      err.Error(),
			"edition_id": editionID,
		})
		return fmt.Errorf("failed to get or create user book ID: %w", err)
	}

	// Mark book as owned if sync_owned is enabled
	if s.config.App.SyncOwned && hcBook != nil && hcBook.EditionID != "" && hcBook.EditionID != "0" {
		editionIDInt, err := strconv.Atoi(hcBook.EditionID)
		if err != nil {
			bookLog.Warn("Invalid edition ID format for marking as owned", map[string]interface{}{
				"edition_id": hcBook.EditionID,
				"error":      err.Error(),
			})
		} else {
			// Check if book is already marked as owned using book ID (not edition ID)
			bookIDInt, err := strconv.Atoi(hcBook.ID)
			if err != nil {
				bookLog.Warn("Invalid book ID format for ownership check", map[string]interface{}{
					"book_id": hcBook.ID,
					"error":   err.Error(),
				})
			} else {
				isOwned, err := s.hardcover.CheckBookOwnership(ctx, bookIDInt)
				if err != nil {
					bookLog.Warn("Failed to check book ownership status", map[string]interface{}{
						"book_id":    bookIDInt,
						"edition_id": editionIDInt,
						"error":      err.Error(),
					})
				} else if !isOwned {
					err = s.hardcover.MarkEditionAsOwned(ctx, editionIDInt)
					if err != nil {
						bookLog.Warn("Failed to mark edition as owned", map[string]interface{}{
							"edition_id": editionIDInt,
							"error":      err.Error(),
						})
					} else {
						bookLog.Info("Successfully marked edition as owned", map[string]interface{}{
							"book_id":    bookIDInt,
							"edition_id": editionIDInt,
						})
					}
				} else {
					bookLog.Debug("Book is already marked as owned", map[string]interface{}{
						"book_id":    bookIDInt,
						"edition_id": editionIDInt,
					})
				}
			}
		}
	}

	// Log the book we're processing
	editionIDStr := editionID // Keep original string for logging
	bookLog = bookLog.With(map[string]interface{}{
		"hardcover_id": hcBook.ID,
		"edition_id":   editionIDStr,
		"user_book_id": userBookID,
	})

	bookLog.Debug("Using user book ID for updates", map[string]interface{}{
		"user_book_id": userBookID,
	})

	// Handle progress update based on status
	switch status {
	case "FINISHED":
		// For finished books, we need to handle re-reads specially
		bookLog.Info("Processing finished book", map[string]interface{}{
			"status":   status,
			"progress": progress,
		})
		if err := s.HandleFinishedBook(ctx, book, editionID, userBookID); err != nil {
			bookLog.Error("Failed to handle finished book", map[string]interface{}{
				"error": err,
			})
			return fmt.Errorf("error handling finished book: %w", err)
		}

	case "IN_PROGRESS", "READING":
		// Handle in-progress book
		bookLog.Info("Processing in-progress book", map[string]interface{}{
			"status":   status,
			"progress": progress,
		})

		// Call handleInProgressBook to update the progress with the composite state key
		if err := s.handleInProgressBook(ctx, userBookID, book, stateKey); err != nil {
			bookLog.Error("Failed to handle in-progress book", map[string]interface{}{
				"error": err,
			})
			return fmt.Errorf("error handling in-progress book: %w", err)
		}
		return nil

	default:
		bookLog.Info("No specific handling for book status", map[string]interface{}{
			"status": status,
		})
	}

	return nil
}

// createFinishedBookLogger creates a logger with context for the handleFinishedBook function
func (s *Service) createFinishedBookLogger(userBookID int64, editionID string, book models.AudiobookshelfBook) *logger.Logger {
	fields := map[string]interface{}{
		"user_book_id": userBookID,
		"edition_id":   editionID,
		"book_id":      book.ID,
		"dry_run":      s.config.App.DryRun,
	}

	// Add title from metadata if available
	if book.Media.Metadata.Title != "" {
		fields["title"] = book.Media.Metadata.Title
	}

	// Add progress information if available
	if book.Progress.CurrentTime > 0 || book.Progress.IsFinished || book.Progress.FinishedAt > 0 {
		fields["progress"] = book.Progress.CurrentTime
		fields["is_finished"] = book.Progress.IsFinished
		fields["finished_at"] = book.Progress.FinishedAt
	}

	// Add additional debugging info
	fields["book_type"] = fmt.Sprintf("%T", book)

	// Create a new logger with the fields
	return s.log.With(fields)
}

// HandleFinishedBook processes a book that has been marked as finished
func (s *Service) HandleFinishedBook(ctx context.Context, book models.AudiobookshelfBook, editionID string, userBookID int64) error {
	// Create a logger with context
	log := s.createFinishedBookLogger(userBookID, editionID, book)
	log.Info("Handling finished book", nil)

	// First, check the current status of the book and update to FINISHED if needed
	log.Info("Checking current book status", map[string]interface{}{
		"user_book_id": userBookID,
	})

	// Convert userBookID to string for GetUserBook
	userBookIDStr := strconv.FormatInt(userBookID, 10)

	// Get the current user book to check its status
	// Try cache first
	var userBook *models.HardcoverBook
	var getUserBookErr error
	
	if cachedUserBook, found := s.getUserBookFromCache(int(userBookID)); found {
		log.Debug("User book found in cache", map[string]interface{}{
			"user_book_id": userBookID,
		})
		userBook = cachedUserBook
	} else {
		// Not in cache, fetch from API
		userBook, getUserBookErr = s.hardcover.GetUserBook(ctx, userBookIDStr)
		if getUserBookErr == nil && userBook != nil {
			// Cache the result
			s.setUserBookByUserBookInCache(int(userBookID), userBook)
			log.Debug("User book cached", map[string]interface{}{
				"user_book_id": userBookID,
			})
		}
	}
	if getUserBookErr != nil {
		log.Warn("Failed to get current book status, will attempt to update anyway", map[string]interface{}{
			"error": getUserBookErr,
		})
		// Continue with update even if we couldn't get the current status
	} else if userBook != nil {
		// Check if the book is already marked as FINISHED (status ID 3)
		log.Debug("Current book status", map[string]interface{}{
			"book_status_id": userBook.BookStatusID,
		})

		// Only update if not already FINISHED (status ID 3 = READ/FINISHED)
		if userBook.BookStatusID != 3 {
			log.Info("Updating book status to FINISHED", map[string]interface{}{
				"user_book_id":      userBookID,
				"current_status_id": userBook.BookStatusID,
			})

			statusErr := s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
				ID:     userBookID,
				Status: "FINISHED",
			})

			if statusErr != nil {
				log.Error("Failed to update book status to FINISHED", map[string]interface{}{
					"error": statusErr,
				})
				// Continue processing even if this fails - we'll still try to update the read status
			} else {
				log.Info("Successfully updated book status to FINISHED", nil)
			}
		} else {
			log.Info("Book already has FINISHED status, skipping status update", map[string]interface{}{
				"user_book_id":   userBookID,
				"book_status_id": userBook.BookStatusID,
			})
		}
	} else {
		// If we got nil without an error, something is wrong
		log.Warn("Got nil user book without error, will attempt to update status anyway", nil)

		statusErr := s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
			ID:     userBookID,
			Status: "FINISHED",
		})

		if statusErr != nil {
			log.Error("Failed to update book status to FINISHED", map[string]interface{}{
				"error": statusErr,
			})
			// Continue processing even if this fails - we'll still try to update the read status
		} else {
			log.Info("Successfully updated book status to FINISHED", nil)
		}
	}

	// Look for the most recent unfinished read and check for existing finished reads
	log.Info("Fetching read statuses from Hardcover", map[string]interface{}{
		"user_book_id": userBookID,
	})

	readStatuses, err := s.hardcover.GetUserBookReads(ctx, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	})

	if err != nil {
		log.Error("Failed to get read statuses", map[string]interface{}{
			"error": err,
		})
		return fmt.Errorf("error getting read statuses: %w", err)
	}

	log.Info("Received read statuses from Hardcover", map[string]interface{}{
		"count": len(readStatuses),
	})

	// Look for the most recent unfinished read and check for existing finished reads today
	var latestUnfinishedRead *hardcover.UserBookRead
	var latestFinishedReadTime time.Time
	hasFinishedRead := false

	log.Debug("Processing read statuses", map[string]interface{}{
		"total_statuses": len(readStatuses),
		"book_id":        book.ID,
		"title":          book.Media.Metadata.Title,
	})

	for i, read := range readStatuses {
		log.Debug("Processing read status", map[string]interface{}{
			"index":            i,
			"read_id":          read.ID,
			"started_at":       read.StartedAt,
			"finished_at":      read.FinishedAt,
			"progress":         read.Progress,
			"progress_seconds": read.ProgressSeconds,
		})

		// Check if this is a finished read
		if read.FinishedAt != nil && *read.FinishedAt != "" {
			finishedDate := *read.FinishedAt
			if len(finishedDate) > 10 { // If it includes time, truncate to just date
				finishedDate = finishedDate[:10]
			}

			// Track the most recent finished read
			// Parse the date string, handling multiple possible formats
			var finishedAt time.Time
			var parseErr error

			// Try parsing as RFC3339 first (full timestamp with timezone)
			finishedAt, parseErr = time.Parse(time.RFC3339, finishedDate)
			if parseErr != nil {
				// Try parsing as just date (YYYY-MM-DD)
				finishedAt, parseErr = time.Parse("2006-01-02", finishedDate)
			}

			if parseErr != nil {
				log.Error("Failed to parse finished date", map[string]interface{}{
					"error":   parseErr.Error(),
					"rawDate": finishedDate,
				})
			} else if finishedAt.After(latestFinishedReadTime) {
				latestFinishedReadTime = finishedAt
			}

			hasFinishedRead = true
		}

		if read.FinishedAt == nil || *read.FinishedAt == "" {
			// This is an unfinished read
			log.Debug("Found unfinished read", map[string]interface{}{
				"read_id": read.ID,
			})
			if latestUnfinishedRead == nil {
				latestUnfinishedRead = &readStatuses[i]
			}
		} else {
			// This is a finished read
			hasFinishedRead = true
			// Track the most recent finished read
			if read.FinishedAt != nil && *read.FinishedAt != "" {
				// Try parsing with RFC3339 first, then fall back to YYYY-MM-DD
				finishedDate := *read.FinishedAt
				var finishedAt time.Time
				var parseErr error

				// Try parsing as RFC3339 first (full timestamp with timezone)
				finishedAt, parseErr = time.Parse(time.RFC3339, finishedDate)
				if parseErr != nil {
					// Try parsing as just date (YYYY-MM-DD)
					if len(finishedDate) > 10 { // If it includes time, truncate to just date
						finishedDate = finishedDate[:10]
					}
					finishedAt, parseErr = time.Parse("2006-01-02", finishedDate)
				}

				if parseErr != nil {
					log.Error("Failed to parse finished_at time", map[string]interface{}{
						"error":     parseErr.Error(),
						"raw_value": *read.FinishedAt,
					})
				} else if finishedAt.After(latestFinishedReadTime) {
					latestFinishedReadTime = finishedAt
					log.Debug("Updated latest finished read time", map[string]interface{}{
						"new_time": latestFinishedReadTime,
						"read_id":  read.ID,
					})
				}
			}
		}
	}

	log.Info("Finished processing read statuses", map[string]interface{}{
		"has_unfinished_read": latestUnfinishedRead != nil,
		"has_finished_read":   hasFinishedRead,
		"latest_finished":     latestFinishedReadTime,
		"book_id":             book.ID,
		"title":               book.Media.Metadata.Title,
	})

	// If we have any read status, we don't need to create a new one
	if hasFinishedRead || latestUnfinishedRead != nil {
		// If we have an unfinished read, update it to mark as finished
		if latestUnfinishedRead != nil {
			// Create update object with all fields needed for the update
			progress := 100.0
			finishedAt := time.Now().Format("2006-01-02")

			// Prepare the update object with all fields
			updateObj := map[string]interface{}{
				"finished_at": finishedAt,
				"progress":    progress, // Always set to 100% when marking as finished
			}

			// Add progress_seconds if available, otherwise use book's duration
			if latestUnfinishedRead.ProgressSeconds != nil {
				updateObj["progress_seconds"] = *latestUnfinishedRead.ProgressSeconds
			} else if book.Media.Duration > 0 {
				durationInt := int(book.Media.Duration)
				updateObj["progress_seconds"] = durationInt
			}

			// Preserve started_at if it exists
			if latestUnfinishedRead.StartedAt != nil && *latestUnfinishedRead.StartedAt != "" {
				updateObj["started_at"] = *latestUnfinishedRead.StartedAt
			} else {
				// If no started_at, use current date as fallback
				updateObj["started_at"] = finishedAt
			}

			// Preserve edition_id if it exists
			if latestUnfinishedRead.EditionID != nil {
				editionID := *latestUnfinishedRead.EditionID
				updateObj["edition_id"] = editionID
			}

			// Log the update object for debugging
			s.log.Debug("Updating existing read status to mark as finished", map[string]interface{}{
				"id":          latestUnfinishedRead.ID,
				"progress":    updateObj["progress"],
				"started_at":  updateObj["started_at"],
				"finished_at": updateObj["finished_at"],
			})

			// Update the read record with all fields
			_, err = s.hardcover.UpdateUserBookRead(ctx, hardcover.UpdateUserBookReadInput{
				ID:     latestUnfinishedRead.ID,
				Object: updateObj,
			})

			if err != nil {
				log.Error("Failed to update existing read status", map[string]interface{}{
					"error":   err.Error(),
					"read_id": latestUnfinishedRead.ID,
				})
				return fmt.Errorf("error updating read status: %w", err)
			}

			log.Info("Updated existing read status to mark as finished", map[string]interface{}{
				"read_id": latestUnfinishedRead.ID,
			})
		} else {
			log.Info("Book already has a read status, not creating a new one", map[string]interface{}{
				"book_id": book.ID,
				"title":   book.Media.Metadata.Title,
			})
		}
		return nil
	}

	// If we get here, there are no existing read statuses at all
	shouldCreateNewRead := true

	if shouldCreateNewRead {
		// Create a new read record with current progress
		finishedAt := time.Now().Format("2006-01-02")
		startedAt := finishedAt // Use current time as started_at if not available

		// If we have progress from the book, use it
		var progressSeconds *int

		if book.Progress.CurrentTime > 0 {
			seconds := int(book.Progress.CurrentTime)
			progressSeconds = &seconds
		}

		// Convert editionID to int64
		editionIDInt, _ := strconv.ParseInt(editionID, 10, 64)

		// Set progress to 100% when creating a new finished read
		// We'll use progress_seconds to set the progress
		var finalProgressSeconds int
		if progressSeconds != nil {
			finalProgressSeconds = *progressSeconds
		} else if book.Media.Duration > 0 {
			// If no progress seconds but we have duration, use that
			finalProgressSeconds = int(book.Media.Duration)
		} else {
			// Default to a reasonable value if we have no other info
			finalProgressSeconds = 3600 // 1 hour as fallback
		}

		// Create the read record using the proper input type
		_, err = s.hardcover.InsertUserBookRead(ctx, hardcover.InsertUserBookReadInput{
			UserBookID: userBookID,
			DatesRead: hardcover.DatesReadInput{
				FinishedAt:      &finishedAt,
				StartedAt:       &startedAt,
				ProgressSeconds: &finalProgressSeconds, // This will effectively set progress to 100%
				EditionID:       &editionIDInt,
			},
		})

		if err != nil {
			log.Error("Failed to create new read record", map[string]interface{}{
				"error": err.Error(),
			})
			return fmt.Errorf("error creating new read record: %w", err)
		}

		log.Info("Successfully created new read record")
	} else {
		log.Info("Skipping read record creation - recent finished read exists", nil)
	}

	return nil
}

// handleInProgressBook processes a book that is currently in progress
// stateKey is the composite key used to track state for this book (bookID:editionID)
func (s *Service) handleInProgressBook(ctx context.Context, userBookID int64, book models.AudiobookshelfBook, stateKey string) error {
	// Initialize logger context
	bookTitle := book.Media.Metadata.Title
	logCtx := map[string]interface{}{
		"function":     "handleInProgressBook",
		"user_book_id": userBookID,
		"book_id":      book.ID,
	}

	// Add title and author if available
	if bookTitle != "" {
		logCtx["title"] = bookTitle
	}
	if book.Media.Metadata.AuthorName != "" {
		logCtx["author"] = book.Media.Metadata.AuthorName
	}

	// Create a logger with context
	log := s.log.With(logCtx)

	// Debug logging for Scrum book
	if strings.Contains(strings.ToLower(bookTitle), "scrum") {
		log.Info("DEBUG - Handling in-progress Scrum book", map[string]interface{}{
			"progress":     book.Progress.CurrentTime,
			"is_finished":  book.Progress.IsFinished,
			"duration":     book.Media.Duration,
			"progress_pct": (book.Progress.CurrentTime / book.Media.Duration) * 100,
		})
	}
	log.Info("Processing in-progress book", nil)

	// Get current book status from Hardcover
	// Try cache first
	var hcBook *models.HardcoverBook
	var err error
	
	if cachedUserBook, found := s.getUserBookFromCache(int(userBookID)); found {
		log.Debug("User book found in cache", map[string]interface{}{
			"user_book_id": userBookID,
		})
		hcBook = cachedUserBook
	} else {
		// Not in cache, fetch from API
		hcBook, err = s.hardcover.GetUserBook(ctx, strconv.FormatInt(userBookID, 10))
		if err == nil && hcBook != nil {
			// Cache the result
			s.setUserBookByUserBookInCache(int(userBookID), hcBook)
			log.Debug("User book cached", map[string]interface{}{
				"user_book_id": userBookID,
			})
		}
	}
	if err != nil {
		errCtx := make(map[string]interface{}, len(logCtx)+1)
		errCtx["error"] = err.Error()
		s.log.With(errCtx).Error("Failed to get current book status from Hardcover", nil)
		return fmt.Errorf("failed to get current book status: %w", err)
	}

	// Get the current read status to check progress
	readStatuses, err := s.hardcover.GetUserBookReads(ctx, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	})
	if err != nil {
		errCtx := make(map[string]interface{}, len(logCtx)+1)
		errCtx["error"] = err.Error()
		s.log.With(errCtx).Error("Failed to get current read status from Hardcover", nil)
		// Continue with update as we can't determine current progress
	}

	// Add progress information to log context
	logCtx["current_time_sec"] = book.Progress.CurrentTime
	logCtx["is_finished"] = book.Progress.IsFinished
	logCtx["started_at"] = book.Progress.StartedAt
	logCtx["finished_at"] = book.Progress.FinishedAt
	logCtx["duration_seconds"] = book.Media.Duration

	// Add current Hardcover progress to log context
	// Note: HardcoverBook doesn't have a Progress field, so we'll skip this for now
	// We'll log the book ID and title for debugging
	if hcBook != nil {
		logCtx["hardcover_book_id"] = hcBook.ID
		logCtx["hardcover_title"] = hcBook.Title
	}

	// Calculate progress percentage if we have duration
	if book.Media.Duration > 0 {
		progressPct := (book.Progress.CurrentTime / book.Media.Duration) * 100
		logCtx["progress_percent"] = fmt.Sprintf("%.1f%%", progressPct)
	}

	// Create logger with all context
	log = s.log.With(logCtx)

	// In dry-run mode, log that we're in dry-run and continue with checks
	if s.config.App.DryRun {
		logCtx["action"] = "dry_run_skipped"
		logCtx["reason"] = "dry-run mode is enabled"
		s.log.With(logCtx).Info("Dry-run mode: skipping update", nil)
		return nil
	}

	// Log the current progress from Audiobookshelf
	dbgCtx := make(map[string]interface{}, len(logCtx)+4)
	for k, v := range logCtx {
		dbgCtx[k] = v
	}
	dbgCtx["abs_progress"] = book.Progress.CurrentTime
	dbgCtx["started_at"] = book.Progress.StartedAt
	dbgCtx["finished_at"] = book.Progress.FinishedAt
	dbgCtx["is_finished"] = book.Progress.IsFinished
	log.Debug("Current progress from Audiobookshelf", dbgCtx)

	// Check if we have progress to report
	if book.Progress.CurrentTime <= 0 {
		log.Info("No progress to update (current time is 0)", nil)
		return nil
	}

	// Check if we've recently updated this book's progress
	bookCacheKey := fmt.Sprintf("%s:%d", book.ID, userBookID)
	s.lastProgressMutex.RLock()
	lastUpdate, exists := s.lastProgressUpdates[bookCacheKey]
	s.lastProgressMutex.RUnlock()

	// If we've updated this book in the last 5 minutes and the progress is very similar (within 5 seconds),
	// skip the update to prevent unnecessary API calls
	if exists && time.Since(lastUpdate.timestamp) < 5*time.Minute {
		progressDiff := math.Abs(book.Progress.CurrentTime - lastUpdate.progress)
		if progressDiff < 5.0 {
			logCtx["last_update_time"] = lastUpdate.timestamp
			logCtx["last_progress"] = lastUpdate.progress
			logCtx["progress_diff"] = progressDiff
			log.Info("Skipping update - recently updated with similar progress", logCtx)
			return nil
		}
	}

	// Find the most appropriate read status to update and identify any duplicates
	var readStatusToUpdate *hardcover.UserBookRead
	var mostRecentRead *hardcover.UserBookRead
	var mostRecentTime time.Time
	var duplicateUnfinishedReads []*hardcover.UserBookRead

	// First pass: identify all unfinished reads and find the one with most progress
	for i := range readStatuses {
		read := &readStatuses[i]

		// Track all unfinished reads
		if read.FinishedAt == nil {
			if readStatusToUpdate == nil {
				// First unfinished read we find becomes our primary
				readStatusToUpdate = read
			} else {
				// Any additional unfinished reads are duplicates
				// Compare progress and use the one with the highest progress as primary
				var currentProgress, newProgress float64

				if readStatusToUpdate.ProgressSeconds != nil {
					currentProgress = float64(*readStatusToUpdate.ProgressSeconds)
				} else {
					currentProgress = readStatusToUpdate.Progress
				}

				if read.ProgressSeconds != nil {
					newProgress = float64(*read.ProgressSeconds)
				} else {
					newProgress = read.Progress
				}

				log.Warn("Found duplicate unfinished read entry", map[string]interface{}{
					"current_read_id":    readStatusToUpdate.ID,
					"current_progress":   currentProgress,
					"duplicate_read_id":  read.ID,
					"duplicate_progress": newProgress,
				})

				// If the new one has higher progress, swap them
				if newProgress > currentProgress {
					duplicateUnfinishedReads = append(duplicateUnfinishedReads, readStatusToUpdate)
					readStatusToUpdate = read
				} else {
					duplicateUnfinishedReads = append(duplicateUnfinishedReads, read)
				}
			}
		}

		// Track the most recent read status as a fallback
		if read.FinishedAt != nil {
			finishedTime, err := time.Parse("2006-01-02", *read.FinishedAt)
			if err == nil && (mostRecentRead == nil || finishedTime.After(mostRecentTime)) {
				mostRecentTime = finishedTime
				mostRecentRead = read
			}
		}
	}

	// If we found duplicates, clean them up
	if len(duplicateUnfinishedReads) > 0 {
		log.Warn(fmt.Sprintf("Found %d duplicate unfinished read entries, will clean up", len(duplicateUnfinishedReads)), nil)

		// We'll only delete the duplicates if we're not in dry-run mode
		if !s.config.App.DryRun {
			for _, duplicateRead := range duplicateUnfinishedReads {
				log.Info("Marking duplicate read entry as deleted", map[string]interface{}{
					"read_id": duplicateRead.ID,
				})

				// Mark the duplicate as deleted by setting finished_at to today and progress to 0
				// (We do this instead of deleting to preserve history)
				today := time.Now().Format("2006-01-02")
				updateObj := map[string]interface{}{
					"finished_at":      today,
					"progress_seconds": 0,
					"progress":         0,
				}

				_, err := s.hardcover.UpdateUserBookRead(ctx, hardcover.UpdateUserBookReadInput{
					ID:     duplicateRead.ID,
					Object: updateObj,
				})

				if err != nil {
					log.Error("Failed to mark duplicate read as deleted", map[string]interface{}{
						"read_id": duplicateRead.ID,
						"error":   err.Error(),
					})
				} else {
					log.Info("Successfully marked duplicate read as deleted", map[string]interface{}{
						"read_id": duplicateRead.ID,
					})
				}
			}
		}
	}

	// Create a logger with book context
	bookLog := s.log.WithFields(map[string]interface{}{
		"book_id": book.ID,
		"title":   book.Media.Metadata.Title,
		"author":  book.Media.Metadata.AuthorName,
	})

	// Update state with current progress before proceeding
	progressPct := 0.0
	if book.Media.Duration > 0 {
		progressPct = (book.Progress.CurrentTime / book.Media.Duration) * 100
	}
	status := "IN_PROGRESS"
	if book.Progress.IsFinished {
		status = "FINISHED"
	}

	// Update the state with the current progress and status using the composite key
	if s.state.UpdateBook(stateKey, progressPct, status) {
		bookLog.Debug("Updated book state", map[string]interface{}{
			"progress":  progressPct,
			"status":    status,
			"state_key": stateKey,
		})
	}

	// If no read status found at all, we'll create a new one
	if readStatusToUpdate == nil && mostRecentRead == nil {
		log.Info("No existing read status found, will create a new one", logCtx)
	} else {
		// Only use unfinished reads for updates - don't update finished reads for rereads
		if readStatusToUpdate == nil && mostRecentRead != nil {
			// Check if the most recent read is finished
			if mostRecentRead.FinishedAt != nil && *mostRecentRead.FinishedAt != "" {
				// This is a reread scenario - create a new read instead of updating the finished one
				log.Info("Book has only finished reads but shows new progress - creating new read for reread", map[string]interface{}{
					"most_recent_finished_at": *mostRecentRead.FinishedAt,
					"current_progress": book.Progress.CurrentTime,
				})
				// Set readStatusToUpdate to nil so we create a new read
				readStatusToUpdate = nil
			} else {
				// Most recent read is unfinished, we can update it
				readStatusToUpdate = mostRecentRead
				logCtx["using_most_recent_read"] = true
			}
		}

		// Log which read status we're using
		if readStatusToUpdate != nil {
			logCtx["read_status_id"] = readStatusToUpdate.ID
			// Use ProgressSeconds if available, otherwise fall back to Progress field
			var hcProgressSeconds float64
			if readStatusToUpdate.ProgressSeconds != nil {
				hcProgressSeconds = float64(*readStatusToUpdate.ProgressSeconds)
				logCtx["existing_progress_seconds"] = hcProgressSeconds
				logCtx["progress_source"] = "progress_seconds"
			} else {
				hcProgressSeconds = readStatusToUpdate.Progress
				logCtx["existing_progress_seconds"] = hcProgressSeconds
				logCtx["progress_source"] = "progress"
			}
			if readStatusToUpdate.FinishedAt != nil {
				logCtx["read_status_finished_at"] = *readStatusToUpdate.FinishedAt
			}

			// Calculate progress difference in both absolute seconds and percentage
			progressDiff := math.Abs(float64(book.Progress.CurrentTime - hcProgressSeconds))
			minDiff := 60.0 // 60 second minimum difference to trigger an update (increased from 30s)

			// Calculate progress percentage difference if we have duration
			var progressPctDiff float64
			if book.Media.Duration > 0 {
				hcProgressPct := (hcProgressSeconds / book.Media.Duration) * 100
				absProgressPct := (book.Progress.CurrentTime / book.Media.Duration) * 100
				progressPctDiff = math.Abs(hcProgressPct - absProgressPct)
				logCtx["existing_progress_pct"] = fmt.Sprintf("%.1f%%", hcProgressPct)
				logCtx["progress_pct_diff"] = fmt.Sprintf("%.1f%%", progressPctDiff)
			}

			// If progress is very small, be more lenient
			if hcProgressSeconds < 60 || book.Progress.CurrentTime < 60 {
				minDiff = 10.0 // 10 second threshold for new/small progress
			}

			// If progress is nearly the same (within 1 second), skip update regardless of threshold
			if progressDiff < 1.0 {
				logCtx["progress_diff_seconds"] = fmt.Sprintf("%.2f", progressDiff)
				log.Info("Progress is identical or nearly identical, skipping update", logCtx)
				return nil
			}

			logCtx["progress_diff_seconds"] = fmt.Sprintf("%.2f", progressDiff)
			logCtx["min_diff_seconds"] = minDiff

			// Skip update if progress difference is below threshold
			if progressDiff < minDiff {
				log.Info("Progress difference below threshold, skipping update", logCtx)
				return nil
			}

			// Warn about extremely large progress differences (> 1 hour)
			if progressDiff > 3600 {
				logCtx["progress_diff_hours"] = fmt.Sprintf("%.2f", progressDiff/3600)
				logCtx["abs_progress_time"] = time.Duration(int64(book.Progress.CurrentTime) * int64(time.Second)).String()
				logCtx["hc_progress_time"] = time.Duration(int64(hcProgressSeconds) * int64(time.Second)).String()
				log.Warn("Extremely large progress difference detected. Possible book mapping or sync issue.", logCtx)
			}

			// Store the last update time and progress for this book to prevent frequent updates
			// This is a memory-only cache that will be reset when the service restarts
			bookCacheKey := fmt.Sprintf("%s:%d", book.ID, userBookID)
			s.lastProgressMutex.Lock()
			s.lastProgressUpdates[bookCacheKey] = progressUpdateInfo{
				timestamp: time.Now(),
				progress:  book.Progress.CurrentTime,
			}
			s.lastProgressMutex.Unlock()

			// Update the state with current progress and status
			status := "IN_PROGRESS"
			if book.Progress.IsFinished {
				status = "FINISHED"
			}
			progressPct := 0.0
			if book.Media.Duration > 0 {
				progressPct = (book.Progress.CurrentTime / book.Media.Duration) * 100
			}
			if updated := s.state.UpdateBook(book.ID, progressPct, status); updated {
				log.Debug("Updated book state with new progress", map[string]interface{}{
					"book_id":  book.ID,
					"progress": progressPct,
					"status":   status,
				})
			}

			log.Info("Significant progress difference detected, will update", logCtx)
		}
	}

	// Prepare the update object with progress and format
	updateObj := map[string]interface{}{
		"progress_seconds":  int64(book.Progress.CurrentTime),
		"reading_format_id": 2, // 2 = Audiobook format
	}

	// Format dates as YYYY-MM-DD strings
	if book.Progress.StartedAt > 0 {
		startedAt := time.Unix(book.Progress.StartedAt/1000, 0).Format("2006-01-02")
		updateObj["started_at"] = startedAt
	}

	// Handle finished status
	if book.Progress.IsFinished && book.Progress.FinishedAt > 0 {
		finishedAt := time.Unix(book.Progress.FinishedAt/1000, 0).Format("2006-01-02")
		updateObj["finished_at"] = finishedAt
	} else if readStatusToUpdate != nil && readStatusToUpdate.FinishedAt != nil {
		// If the book is not finished in ABS but was finished in Hardcover, mark it as in-progress
		updateObj["finished_at"] = nil
	}

	// Check if the book is marked as finished in both systems
	isFinishedInABS := book.Progress.IsFinished
	// HardcoverBook doesn't have a Status field, so we'll assume it's not finished
	// We'll need to get this information from the read status instead
	isFinishedInHC := false
	if readStatusToUpdate != nil && readStatusToUpdate.FinishedAt != nil {
		isFinishedInHC = true
	}

	// If the book is marked as finished in both systems, we don't need to update anything
	if isFinishedInABS && isFinishedInHC {
		log.Info("Book is already marked as finished in both systems, skipping update", logCtx)
		return nil
	}

	// If we have a read status to update, update it
	if readStatusToUpdate != nil {
		logCtx["existing_read_status_id"] = readStatusToUpdate.ID

		// Use ProgressSeconds if available, otherwise fall back to Progress field
		var hcProgressSeconds float64
		if readStatusToUpdate.ProgressSeconds != nil {
			hcProgressSeconds = float64(*readStatusToUpdate.ProgressSeconds)
		} else {
			hcProgressSeconds = readStatusToUpdate.Progress
		}
		logCtx["existing_progress"] = hcProgressSeconds

		if readStatusToUpdate.FinishedAt != nil {
			logCtx["existing_finished_at"] = *readStatusToUpdate.FinishedAt
		}

		// Format the finished date from Audiobookshelf
		absFinishedAt := ""
		if book.Progress.FinishedAt > 0 {
			t := time.Unix(book.Progress.FinishedAt/1000, 0)
			absFinishedAt = t.Format("2006-01-02")
		}

		// If the book is marked as finished in ABS
		if book.Progress.IsFinished && book.Progress.FinishedAt > 0 {
			// If the existing status is not finished, or has a different finished date
			if readStatusToUpdate.FinishedAt == nil || *readStatusToUpdate.FinishedAt == "" ||
				*readStatusToUpdate.FinishedAt != absFinishedAt {

				// Update the existing read status with the new finished date
				updateObj["finished_at"] = absFinishedAt
				logCtx["abs_finished_date"] = absFinishedAt
				if readStatusToUpdate.FinishedAt != nil {
					logCtx["hardcover_finished_date"] = *readStatusToUpdate.FinishedAt
				}
				log.Info("Updating existing read status with new finished date", logCtx)
			} else {
				// If the existing status is already finished with the same date, skip update
				log.Info("Skipping update - existing read status is already marked as finished with the same date", logCtx)
				return nil
			}
		} else {
			// Progress difference was already checked above, so we can proceed with the update
			// Get the values from the log context to avoid undefined variables
			pDiff, _ := logCtx["progress_diff_seconds"].(string)
			mDiff, _ := logCtx["min_diff_seconds"].(float64)
			log.Info(fmt.Sprintf("Updating existing read status - progress difference (%s) >= min threshold (%.2f)", pDiff, mDiff), logCtx)
		}

		// Include edition_id if available
		if readStatusToUpdate.EditionID != nil {
			updateObj["edition_id"] = *readStatusToUpdate.EditionID
		} else if hcBook != nil && hcBook.EditionID != "" {
			// Convert string edition ID to int if needed
			editionID, err := strconv.Atoi(hcBook.EditionID)
			if err == nil && editionID != 0 {
				updateObj["edition_id"] = editionID
			}
		}

		// Update the read with the current progress
		updateInput := hardcover.UpdateUserBookReadInput{
			ID:     readStatusToUpdate.ID,
			Object: updateObj,
		}

		logCtx["update_input"] = updateInput
		log.Debug("Updating existing read status", logCtx)

		_, err = s.hardcover.UpdateUserBookRead(ctx, updateInput)
		if err != nil {
			errCtx := map[string]interface{}{
				"read_id": readStatusToUpdate.ID,
				"error":   err.Error(),
			}
			log.With(errCtx).Error("Failed to update progress")
			return fmt.Errorf("failed to update progress: %w", err)
		}

		log.Info("Successfully updated read status in Hardcover", logCtx)

		// Update book status based on progress
		if hcBook != nil {
			// If the book is marked as finished in ABS but not in Hardcover, update status
			if book.Progress.IsFinished && !isFinishedInHC {
				log.Debug("Updating book status to COMPLETED", logCtx)
				err = s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
					ID:       userBookID,
					StatusID: 3, // 3 = Completed
				})
				if err != nil {
					errCtx := map[string]interface{}{
						"user_book_id": userBookID,
						"error":        err.Error(),
					}
					log.With(errCtx).Error("Failed to update book status to COMPLETED")
				} else {
					log.Info("Successfully updated book status to COMPLETED", nil)
				}
			} else if !isFinishedInHC {
				// If the book is in progress in ABS but not in Hardcover, update status
				log.Debug("Updating book status to IN_PROGRESS", logCtx)
				err = s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
					ID:       userBookID,
					StatusID: 2, // 2 = Currently Reading
				})
				if err != nil {
					errCtx := map[string]interface{}{
						"user_book_id": userBookID,
						"error":        err.Error(),
					}
					log.With(errCtx).Error("Failed to update book status to IN_PROGRESS")
				} else {
					log.Info("Successfully updated book status to IN_PROGRESS", nil)
				}
			} else {
				log.Debug("Book status is already up to date", logCtx)
			}
		}
	} else {
		// Create a new read status since none exists
		progressSeconds := int(book.Progress.CurrentTime)
		createObj := hardcover.DatesReadInput{
			ProgressSeconds: &progressSeconds,
		}

		// Add dates if available
		if book.Progress.StartedAt > 0 {
			startedAt := time.Unix(book.Progress.StartedAt/1000, 0).Format("2006-01-02")
			createObj.StartedAt = &startedAt
		}

		if book.Progress.IsFinished && book.Progress.FinishedAt > 0 {
			finishedAt := time.Unix(book.Progress.FinishedAt/1000, 0).Format("2006-01-02")
			createObj.FinishedAt = &finishedAt
		}

		// Get the edition ID from the user book
		// Try cache first
		var hcBook *models.HardcoverBook
		var err error
		
		if cachedUserBook, found := s.getUserBookFromCache(int(userBookID)); found {
			log.Debug("User book found in cache for read status creation", map[string]interface{}{
				"user_book_id": userBookID,
			})
			hcBook = cachedUserBook
		} else {
			// Not in cache, fetch from API
			hcBook, err = s.hardcover.GetUserBook(ctx, strconv.FormatInt(userBookID, 10))
			if err == nil && hcBook != nil {
				// Cache the result
				s.setUserBookByUserBookInCache(int(userBookID), hcBook)
				log.Debug("User book cached for read status creation", map[string]interface{}{
					"user_book_id": userBookID,
				})
			}
		}
		if err != nil || hcBook == nil {
			errMsg := "User book not found"
			if err != nil {
				errMsg = fmt.Sprintf("Failed to get user book: %v", err)
			}
			errCtx := map[string]interface{}{"error": errMsg}
			log.With(errCtx).Error("Cannot create read status without user book")
			return fmt.Errorf("cannot create read status: %s", errMsg)
		}

		// Set edition ID if available
		if hcBook.EditionID != "" {
			// Convert string edition ID to int if needed
			editionID, err := strconv.Atoi(hcBook.EditionID)
			if err == nil && editionID != 0 {
				editionIDInt := int64(editionID) // Convert to int64 to match expected type
				createObj.EditionID = &editionIDInt
			}
		}

		// Insert the new read status
		_, err = s.hardcover.InsertUserBookRead(ctx, hardcover.InsertUserBookReadInput{
			UserBookID: userBookID,
			DatesRead:  createObj,
		})

		if err != nil {
			errCtx := map[string]interface{}{"error": err.Error()}
			log.With(errCtx).Error("Failed to create read status in Hardcover")
			return fmt.Errorf("failed to create read status in Hardcover: %w", err)
		}

		log.Info("Successfully created new read status in Hardcover", nil)

		// Only update status to IN_PROGRESS if not already in progress
		if !isFinishedInHC {
			err = s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
				ID:       userBookID,
				StatusID: 2, // 2 = Currently Reading
			})
			if err != nil {
				errCtx := map[string]interface{}{
					"user_book_id": userBookID,
					"error":        err.Error(),
				}
				log.With(errCtx).Error("Failed to update book status to IN_PROGRESS")
			} else {
				log.Info("Successfully updated book status to IN_PROGRESS", nil)
			}
		} else {
			log.Debug("Book status is already IN_PROGRESS, skipping update", nil)
		}
	}

	return nil
}

// determineBookStatus determines the book status based on progress and finished status
func (s *Service) determineBookStatus(progress float64, isFinished bool, finishedAt int64) string {
	// If the book is marked as finished, return "FINISHED"
	if isFinished && finishedAt > 0 {
		return "FINISHED"
	}

	// If progress is 100% or more, consider it finished
	if progress >= 1.0 {
		return "FINISHED"
	}

	// If there's some progress but not finished, consider it in progress
	if progress > 0 {
		return "IN_PROGRESS"
	}

	// Only return WANT_TO_READ if sync_want_to_read is enabled
	if s.config.Sync.SyncWantToRead {
		return "WANT_TO_READ"
	}

	// If sync_want_to_read is disabled, return empty string for 0% progress books
	return ""
}

// processFoundBook handles the common logic for processing a found book
func (s *Service) processFoundBook(ctx context.Context, hcBook *models.HardcoverBook, book models.AudiobookshelfBook) (*models.HardcoverBook, error) {
	if hcBook == nil {
		return nil, errors.New("book cannot be nil")
	}

	// Create base logger with common fields
	logCtx := map[string]interface{}{
		"title": book.Media.Metadata.Title,
	}

	// Add book-specific fields if available
	if hcBook != nil {
		logCtx["book_id"] = hcBook.ID
		if hcBook.EditionID != "" {
			logCtx["edition_id"] = hcBook.EditionID
		}
	}
	log := s.log.With(logCtx)

	// Mark book as owned if sync_owned is enabled
	if s.config.App.SyncOwned && hcBook != nil && hcBook.EditionID != "" && hcBook.EditionID != "0" {
		editionID, err := strconv.Atoi(hcBook.EditionID)
		if err != nil {
			log.Warn("Invalid edition ID format for marking as owned", map[string]interface{}{
				"edition_id": hcBook.EditionID,
				"error":      err.Error(),
			})
		} else {
			// Check if book is already marked as owned
			isOwned, err := s.hardcover.CheckBookOwnership(ctx, editionID)
			if err != nil {
				log.Warn("Failed to check book ownership status", map[string]interface{}{
					"edition_id": editionID,
					"error":      err.Error(),
				})
			} else if !isOwned {
				err = s.hardcover.MarkEditionAsOwned(ctx, editionID)
				if err != nil {
					log.Warn("Failed to mark edition as owned", map[string]interface{}{
						"edition_id": editionID,
						"error":      err.Error(),
					})
				} else {
					log.Info("Successfully marked edition as owned", map[string]interface{}{
						"edition_id": editionID,
					})
				}
			} else {
				log.Debug("Book is already marked as owned", map[string]interface{}{
					"edition_id": editionID,
				})
			}
		}
	}

	// If we don't have an edition ID but have a book ID, try to get the first edition
	if (hcBook.EditionID == "" || hcBook.EditionID == "0") && hcBook.ID != "" {
		// Get edition details (caching is handled in the Hardcover client)
		edition, err := s.hardcover.GetEdition(ctx, hcBook.ID)
		
		if err != nil {
			log.Warn("Failed to get edition details, will try to continue with basic info", map[string]interface{}{
				"error": err.Error(),
			})
		} else if edition != nil {
			hcBook.EditionID = edition.ID
			hcBook.EditionASIN = edition.ASIN
			hcBook.EditionISBN10 = edition.ISBN10
			hcBook.EditionISBN13 = edition.ISBN13
			log.Debug("Retrieved edition details", map[string]interface{}{
				"edition_id": hcBook.EditionID,
				"isbn10":     hcBook.EditionISBN10,
				"isbn13":     hcBook.EditionISBN13,
				"asin":       hcBook.EditionASIN,
			})
		}
	}

	// Calculate progress
	progress := 0.0
	isFinished := book.Progress.IsFinished
	finishedAt := book.Progress.FinishedAt
	if book.Media.Duration > 0 {
		progress = book.Progress.CurrentTime / book.Media.Duration
	}

	// Determine the status based on progress and isFinished flag
	status := s.determineBookStatus(progress, isFinished, finishedAt)

	// Only try to get/create user book ID if we have a valid edition ID
	if hcBook.EditionID != "" && hcBook.EditionID != "0" {
		userBookID, err := s.findOrCreateUserBookID(ctx, hcBook.EditionID, status)
		if err != nil {
			fields := map[string]interface{}{
				"edition_id": hcBook.EditionID,
				"error":      err.Error(),
			}
			log.Warn("Failed to get or create user book ID", fields)
		} else {
			hcBook.UserBookID = strconv.FormatInt(userBookID, 10)
		}
	} else {
		log.Warn("Skipping user book ID creation: no valid edition ID available", nil)
	}

	log.Info("Processed found book", map[string]interface{}{
		"book_id":      hcBook.ID,
		"edition_id":   hcBook.EditionID,
		"user_book_id": hcBook.UserBookID,
		"progress":     progress,
		"is_finished":  isFinished,
		"finished_at":  finishedAt,
		"status":       status,
	})

	return hcBook, nil
}

// calculateTitleSimilarity returns a similarity score between two titles
// The score ranges from 0 (completely different) to 1 (exact match)
// It uses a combination of techniques to calculate similarity
func calculateTitleSimilarity(title1, title2 string) float64 {
	// Normalize both titles to lowercase
	title1 = strings.ToLower(title1)
	title2 = strings.ToLower(title2)

	// Remove common punctuation and normalize spaces
	title1 = normalizeTitle(title1)
	title2 = normalizeTitle(title2)

	// Check for exact match after normalization
	if title1 == title2 {
		return 1.0
	}

	// If one title is fully contained in the other, that's a strong signal
	if strings.Contains(title1, title2) || strings.Contains(title2, title1) {
		// Calculate length ratio to prefer closer length matches
		len1 := float64(len(title1))
		len2 := float64(len(title2))
		lenRatio := math.Min(len1, len2) / math.Max(len1, len2)

		// Return high score (0.8-0.9) based on length ratio
		return 0.8 + (0.1 * lenRatio)
	}

	// Split into words for word-based comparison
	words1 := strings.Fields(title1)
	words2 := strings.Fields(title2)

	// Count matching words
	matchCount := 0
	for _, word1 := range words1 {
		for _, word2 := range words2 {
			if word1 == word2 && len(word1) > 2 { // Only count matches for words longer than 2 chars
				matchCount++
				break
			}
		}
	}

	// Calculate word match ratio
	totalUniqueWords := len(getUniqueWords(append(words1, words2...)))
	wordMatchRatio := float64(matchCount) / float64(totalUniqueWords)

	// Return a score that considers word matches
	return math.Min(0.7, wordMatchRatio) // Cap at 0.7 to ensure exact/contained matches rank higher
}

// normalizeTitle removes punctuation and normalizes spaces in a title
func normalizeTitle(title string) string {
	// Remove punctuation
	var sb strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			sb.WriteRune(r)
		}
	}

	// Normalize spaces
	normalized := strings.Join(strings.Fields(sb.String()), " ")
	return normalized
}

// getUniqueWords returns a slice of unique words from the input words
func getUniqueWords(words []string) []string {
	wordSet := make(map[string]struct{})
	for _, word := range words {
		wordSet[word] = struct{}{}
	}

	unique := make([]string, 0, len(wordSet))
	for word := range wordSet {
		unique = append(unique, word)
	}

	return unique
}

// findBookInHardcoverByTitleAuthor searches for a book in Hardcover by title and author
// This should only be used for mismatch handling when a book can't be found by ASIN/ISBN
// Note: This function intentionally does not call GetEdition with the book ID after finding a match.
// While we have the capability to get edition details, we deliberately avoid it here because:
// 1. GetEdition expects an edition ID, not a book ID
// 2. Using a book ID with GetEdition would always fail or return incorrect data
// 3. For our sync purposes, minimal book data is sufficient when no edition is found
// If a caller truly needs edition details, they should handle that separately after receiving
// the book information returned by this function.
func (s *Service) findBookInHardcoverByTitleAuthor(ctx context.Context, book models.AudiobookshelfBook) (*models.HardcoverBook, error) {
	// Normalize title and author for better matching
	title := strings.TrimSpace(book.Media.Metadata.Title)
	author := strings.TrimSpace(book.Media.Metadata.AuthorName)

	// Create a logger with search context
	logCtx := map[string]interface{}{
		"search_method": "title_author",
		"title":         title,
		"author":        author,
	}
	log := s.log.With(logCtx)

	log.Info("Searching for book by title and author", nil)

	// Build search query with title and author if available
	searchQuery := title
	if author != "" {
		searchQuery = fmt.Sprintf("%s %s", title, author)
	}

	// Update logger with query
	log = log.With(map[string]interface{}{
		"query": searchQuery,
	})

	// Search for books using the search API
	searchResults, err := s.hardcover.SearchBooks(ctx, searchQuery, "")
	if err != nil {
		log.Error("Failed to search for books", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to search for books: %w", err)
	}

	if len(searchResults) == 0 {
		log.Info("No books found matching search query", nil)
		return nil, fmt.Errorf("no books found matching search query: %s", searchQuery)
	}

	// Check if the original title contains "summary" to avoid filtering in that case
	titleHasSummary := strings.Contains(strings.ToLower(title), "summary")

	// Get the best result based on filtering and similarity scoring
	var bestMatch *models.HardcoverBook
	var highestScore float64
	var matchDetails []string

	// Log number of results found
	log.Info("Search returned multiple results, will apply filtering and scoring", map[string]interface{}{
		"result_count": len(searchResults),
	})

	// Calculate scores for each result and find the best match
	for _, result := range searchResults {
		resultTitle := result.Title
		resultLower := strings.ToLower(resultTitle)

		// Skip results that contain "Summary" if original doesn't (summary books are less relevant)
		if !titleHasSummary && (strings.Contains(resultLower, "summary of") ||
			strings.Contains(resultLower, "summary:") ||
			strings.HasPrefix(resultLower, "summary ")) {
			log.Debug("Skipping summary book", map[string]interface{}{
				"title": resultTitle,
				"id":    result.ID,
			})
			continue
		}

		// Calculate similarity score
		score := calculateTitleSimilarity(title, resultTitle)

		// Boost score if author matches
		if author != "" && strings.Contains(strings.ToLower(resultTitle), strings.ToLower(author)) {
			score += 0.1 // Boost score for author match in title
			if score > 1.0 {
				score = 1.0 // Cap at 1.0
			}
		}

		// Track match details for logging
		matchDetails = append(matchDetails, fmt.Sprintf("%s (ID: %s, Score: %.2f)",
			resultTitle, result.ID, score))

		// Update best match if score is higher
		if score > highestScore {
			highestScore = score
			bestMatch = &models.HardcoverBook{
				ID:           result.ID,
				Title:        resultTitle,
				BookStatusID: 0, // Will be set when we get the full book details
			}
		}
	}

	// Log all match details for debugging
	log.Debug("Match scores for all results", map[string]interface{}{
		"matches": matchDetails,
	})

	// If no match found after filtering, fall back to first result
	if bestMatch == nil {
		log.Warn("No best match found after filtering, falling back to first result", nil)
		firstResult := searchResults[0]
		bestMatch = &models.HardcoverBook{
			ID:           firstResult.ID,
			Title:        firstResult.Title,
			BookStatusID: 0,
		}
	}

	// Add best match details to logger
	log = log.With(map[string]interface{}{
		"book_id":    bestMatch.ID,
		"book_title": bestMatch.Title,
		"score":      highestScore,
	})

	log.Info("Selected best matching book based on title similarity", map[string]interface{}{
		"original_title": title,
		"match_title":    bestMatch.Title,
		"match_score":    highestScore,
	})

	// IMPORTANT: We deliberately do not attempt to get edition details here.
	// While the API exists, calling GetEdition with a book ID instead of an edition ID would fail.
	// For our sync purposes, having just the book ID and title is sufficient.

	// For a real implementation, we would need an API method to get editions by book ID
	// For now, return the book data we have without edition details
	// The caller can decide whether to handle this as a mismatch
	return bestMatch, nil
}

// findBookInHardcover finds a book in Hardcover by various methods
// It first tries ASIN, then ISBN-13, then ISBN-10
// Title/author search is only used for mismatches and should be called separately
func (s *Service) findBookInHardcover(ctx context.Context, book models.AudiobookshelfBook) (*models.HardcoverBook, error) {
	// Create a logger with book context
	logCtx := map[string]interface{}{
		"book_id": book.ID,
		"title":   book.Media.Metadata.Title,
		"author":  book.Media.Metadata.AuthorName,
	}

	// Add ASIN and ISBN to logger if available
	if book.Media.Metadata.ASIN != "" {
		logCtx["asin"] = book.Media.Metadata.ASIN
	}
	if book.Media.Metadata.ISBN != "" {
		logCtx["isbn"] = book.Media.Metadata.ISBN
	}

	log := s.log.With(logCtx)

	// 1. First try to find by ASIN if available
	if book.Media.Metadata.ASIN != "" {
		// Check ASIN cache first
		if cachedBook, exists := s.getASINFromCache(book.Media.Metadata.ASIN); exists {
			if cachedBook == nil {
				// This ASIN was previously looked up and failed
				log.Debug("Found negative ASIN cache result, skipping API call", map[string]interface{}{
					"asin": book.Media.Metadata.ASIN,
				})
				// Continue to ISBN lookup
			} else {
				log.Debug("Found book in ASIN cache", map[string]interface{}{
					"asin":       book.Media.Metadata.ASIN,
					"book_id":    cachedBook.ID,
					"edition_id": cachedBook.EditionID,
				})
			
			// Create a copy of the cached book to avoid modifying the cached version
			hcBook := &models.HardcoverBook{
				ID:        cachedBook.ID,
				Title:     cachedBook.Title,
				EditionID: cachedBook.EditionID,
				// Copy other fields as needed
			}
			
			// Still need to get/create user book ID for this specific book
			editionIDStr := hcBook.EditionID
			progress := 0.0
			isFinished := book.Progress.IsFinished
			finishedAt := book.Progress.FinishedAt
			if book.Media.Duration > 0 {
				progress = book.Progress.CurrentTime / book.Media.Duration
			}
			
			// Determine the status based on progress and isFinished flag
			status := s.determineBookStatus(progress, isFinished, finishedAt)
			userBookID, err := s.findOrCreateUserBookID(ctx, editionIDStr, status)
			if err != nil {
				s.log.Warn("Failed to get or create user book ID for cached edition", map[string]interface{}{
					"edition_id": editionIDStr,
					"error":      err.Error(),
				})
			} else {
				hcBook.UserBookID = strconv.FormatInt(userBookID, 10)
			}
			
			s.log.Info("Using cached book by ASIN", map[string]interface{}{
				"book_id":      hcBook.ID,
				"edition_id":   hcBook.EditionID,
				"user_book_id": hcBook.UserBookID,
			})
			
			return hcBook, nil
			}
		}
		
		log.Info(fmt.Sprintf("Searching for book by ASIN: %s", book.Media.Metadata.ASIN), nil)

		hcBook, err := s.hardcover.SearchBookByASIN(ctx, book.Media.Metadata.ASIN)
		if err != nil {
			// Check if this is a BookError with a book ID
			var bookErr *hardcover.BookError
			if err != nil && errors.As(err, &bookErr) && bookErr.BookID != "" {
				log.Info("Found book ID in BookError", map[string]interface{}{
					"book_id": bookErr.BookID,
					"error":   bookErr.Error(),
				})
				// Create a minimal book with just the ID
				return &models.HardcoverBook{
					ID: bookErr.BookID,
				}, nil
			}
			// Cache the negative result to avoid repeated failed lookups
			s.setASINInCache(book.Media.Metadata.ASIN, nil)
			log.Debug("Cached negative ASIN lookup result", map[string]interface{}{
				"asin": book.Media.Metadata.ASIN,
			})
			log.Warn(fmt.Sprintf("Search by ASIN failed, will try other methods: %v", err), nil)
		} else if hcBook != nil {
			// Cache the ASIN lookup result for future use
			s.setASINInCache(book.Media.Metadata.ASIN, hcBook)
			log.Debug("Cached ASIN lookup result", map[string]interface{}{
				"asin":       book.Media.Metadata.ASIN,
				"book_id":    hcBook.ID,
				"edition_id": hcBook.EditionID,
			})
			
			// Get or create user book ID for this edition
			editionIDStr := hcBook.EditionID
			progress := 0.0
			isFinished := book.Progress.IsFinished
			finishedAt := book.Progress.FinishedAt
			if book.Media.Duration > 0 {
				progress = book.Progress.CurrentTime / book.Media.Duration
			}

			// Determine the status based on progress and isFinished flag
			status := s.determineBookStatus(progress, isFinished, finishedAt)
			userBookID, err := s.findOrCreateUserBookID(ctx, editionIDStr, status)
			if err != nil {
				s.log.Warn("Failed to get or create user book ID for edition", map[string]interface{}{
					"edition_id": editionIDStr,
					"error":      err.Error(),
				})
			} else {
				hcBook.UserBookID = strconv.FormatInt(userBookID, 10)
			}

			s.log.Info("Found book by ASIN", map[string]interface{}{
				"book_id":      hcBook.ID,
				"edition_id":   hcBook.EditionID,
				"user_book_id": hcBook.UserBookID,
			})

			return hcBook, nil
		}
	}

	// 2. Try to find by ISBN if available
	if book.Media.Metadata.ISBN != "" {
		log.Info(fmt.Sprintf("Searching for book by ISBN: %s", book.Media.Metadata.ISBN), nil)

		// Try to find by ISBN-13 first
		hcBook, err := s.hardcover.SearchBookByISBN13(ctx, book.Media.Metadata.ISBN)
		if err != nil {
			// Check if this is a BookError with a book ID
			var bookErr *hardcover.BookError
			if err != nil && errors.As(err, &bookErr) {
				log.Info("Found book ID in BookError from ISBN-13 search", map[string]interface{}{
					"book_id": bookErr.BookID,
					"error":   bookErr.Error(),
				})
				// Create a minimal book with just the ID
				return &models.HardcoverBook{
					ID: bookErr.BookID,
				}, bookErr
			}
			log.Warn(fmt.Sprintf("Search by ISBN-13 failed, will try ISBN-10: %v", err), nil)
		} else if hcBook != nil {
			return s.processFoundBook(ctx, hcBook, book)
		}

		// If ISBN-13 search failed or returned no results, try ISBN-10
		hcBook, err = s.hardcover.SearchBookByISBN10(ctx, book.Media.Metadata.ISBN)
		if err != nil {
			// Check if this is a BookError with a book ID
			var bookErr *hardcover.BookError
			if err != nil && errors.As(err, &bookErr) && bookErr.BookID != "" {
				log.Info("Found book ID in BookError from ISBN-10 search", map[string]interface{}{
					"book_id": bookErr.BookID,
					"error":   bookErr.Error(),
				})
				// Create a minimal book with just the ID
				return &models.HardcoverBook{
					ID: bookErr.BookID,
				}, nil
			}
			log.Warn(fmt.Sprintf("Search by ISBN-10 failed: %v", err), nil)
		} else if hcBook != nil {
			return s.processFoundBook(ctx, hcBook, book)
		}

		log.Error("Failed to find book by ISBN", map[string]interface{}{
			"title":  book.Media.Metadata.Title,
			"author": book.Media.Metadata.AuthorName,
			"isbn":   book.Media.Metadata.ISBN,
			"asin":   book.Media.Metadata.ASIN,
		})

		return nil, fmt.Errorf("failed to find book by ISBN")
	}

	// 3. If we get here, we couldn't find the book by ASIN or ISBN, try title/author search
	if book.Media.Metadata.Title != "" && book.Media.Metadata.AuthorName != "" {
		log.Info("Trying title/author search after ASIN/ISBN search failed", map[string]interface{}{
			"search_method": "title_author",
			"title":         book.Media.Metadata.Title,
			"author":        book.Media.Metadata.AuthorName,
		})

		hcBook, err := s.findBookInHardcoverByTitleAuthor(ctx, book)
		if err != nil {
			log.Warn("Title/author search failed or edition not found", map[string]interface{}{
				"search_method": "title_author",
				"title":         book.Media.Metadata.Title,
				"author":        book.Media.Metadata.AuthorName,
				"error":         err.Error(),
			})
			// Return the error to be handled as a mismatch
			return nil, fmt.Errorf("book found but edition not available: %w", err)
		}

		// IMPORTANT: Books found by title/author should always trigger mismatch logic
		// This is because we only want ASIN/ISBN matches to be considered valid matches
		// Even if we found a book and it has all the details, it's still a mismatch
		log.Info("Found book by title/author but treating as mismatch per requirements", map[string]interface{}{
			"search_method":    "title_author",
			"book_id":          hcBook.ID,
			"edition_id":       hcBook.EditionID,
			"title":            hcBook.Title,
			"asin_match":       false,
			"isbn_match":       false,
			"forcing_mismatch": true,
		})

		// Return both the book and an error to indicate this is a mismatch
		// The error will trigger the mismatch logic in processBook
		// but the book details can still be used for enrichment
		return hcBook, fmt.Errorf("found by title/author only (not ASIN/ISBN): forcing mismatch")
	}

	log.Warn("Book not found in Hardcover by any search method", map[string]interface{}{
		"book_id": book.ID,
		"title":   book.Media.Metadata.Title,
		"author":  book.Media.Metadata.AuthorName,
		"isbn":    book.Media.Metadata.ISBN,
		"asin":    book.Media.Metadata.ASIN,
	})

	// Return a specific error that indicates this is a potential mismatch
	return nil, fmt.Errorf("book not found by ASIN/ISBN or title/author, potential mismatch")
}
