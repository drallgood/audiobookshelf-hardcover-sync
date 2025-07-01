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
	audiobookshelf *audiobookshelf.Client
	hardcover      hardcover.HardcoverClientInterface
	config         *Config
	log            *logger.Logger
	state          *state.State
	statePath      string
	lastProgressUpdates map[string]progressUpdateInfo // Cache of last progress updates
	lastProgressMutex sync.RWMutex                  // Mutex to protect the cache
}

// Config is the configuration type for the sync service
type Config = config.Config

// NewService creates a new sync service
func NewService(absClient *audiobookshelf.Client, hcClient hardcover.HardcoverClientInterface, cfg *Config) (*Service, error) {
	svc := &Service{
		audiobookshelf: absClient,
		hardcover:      hcClient,
		config:         cfg,
		log:            logger.Get(),
		statePath:      cfg.Sync.StateFile,
		lastProgressUpdates: make(map[string]progressUpdateInfo),
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

	return svc, nil
}

// findOrCreateUserBookID finds or creates a user book ID for the given edition ID and status
func (s *Service) findOrCreateUserBookID(ctx context.Context, editionID, status string) (int64, error) {
	editionIDInt, err := strconv.ParseInt(editionID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid edition ID format: %w", err)
	}

	// Create a logger with context
	logCtx := s.log.With(map[string]interface{}{
		"editionID": editionID,
	})

	// Check if we already have a user book ID for this edition
	userBookID, err := s.hardcover.GetUserBookID(context.Background(), int(editionIDInt))
	if err != nil {
		logCtx.Error("Error checking for existing user book ID", map[string]interface{}{
			"error": err,
		})
		return 0, fmt.Errorf("error checking for existing user book ID: %w", err)
	}

	// If we found an existing user book ID, return it
	if userBookID > 0 {
		s.log.Debug("Found existing user book ID", map[string]interface{}{
			"editionID":  editionID,
			"userBookID": userBookID,
		})
		return int64(userBookID), nil
	}

	s.log.Debug("No existing user book ID found, will create new one", map[string]interface{}{
		"editionID": editionID,
	})

	// If dry-run mode is enabled, log and return early without creating
	if s.config.App.DryRun {
		logCtx.Info("[DRY-RUN] Would create new user book with status", map[string]interface{}{
			"status": status,
		})
		// Return a negative value to indicate dry-run mode
		return -1, nil
	}

	logCtx.Info("Creating new user book with status", map[string]interface{}{
		"status": status,
	})

	// Double-check if the user book exists to prevent race conditions
	userBookID, err = s.hardcover.GetUserBookID(context.Background(), int(editionIDInt))
	if err != nil {
		logCtx.Error("Error in second check for existing user book ID", map[string]interface{}{
			"error": err,
		})
		return 0, fmt.Errorf("error in second check for existing user book ID: %w", err)
	}

	// If we found an existing user book ID in the second check, return it
	if userBookID > 0 {
		logCtx.Debug("Found existing user book ID in second check", map[string]interface{}{
			"userBookID": userBookID,
		})
		return int64(userBookID), nil
	}

	// Create a new user book with the specified status
	newUserBookID, err := s.hardcover.CreateUserBook(ctx, editionID, status)
	if err != nil {
		s.log.Error("Failed to create user book", map[string]interface{}{
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

	// Track total books processed across all libraries
	totalBooksProcessed := 0

	// Log the test book limit if it's set
	if totalBooksLimit > 0 {
		s.log.Info("Test book limit is active", map[string]interface{}{
			"test_book_limit": totalBooksLimit,
		})
	}

	// Process each library
	for i := range libraries {
		// Skip processing if we've reached the limit
		if totalBooksLimit > 0 && totalBooksProcessed >= totalBooksLimit {
			s.log.Info("Reached test book limit before processing library", map[string]interface{}{
				"limit":             totalBooksLimit,
				"already_processed": totalBooksProcessed,
			})
			break
		}

		// Process the library and get the number of books processed
		processed, err := s.processLibrary(ctx, &libraries[i], totalBooksLimit-totalBooksProcessed, userProgress)
		if err != nil {
			s.log.Error("Failed to process library", map[string]interface{}{
				"error":      err,
				"library_id": libraries[i].ID,
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

	s.log.Info("Sync completed successfully", nil)
	return nil
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
			libraryLog.Error("Failed to process item", map[string]interface{}{
				"error":   err,
				"item_id": book.ID,
			})
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
	bookLog := s.log.WithFields(map[string]interface{}{
		"book_id": book.ID,
		"title":   book.Media.Metadata.Title,
		"author":  book.Media.Metadata.AuthorName,
	})

	// Log start of processing
	bookLog.Info("Starting to process book", nil)
	bookLog.Debug("Book details", map[string]interface{}{
		"book_id": book.ID,
		"title":   book.Media.Metadata.Title,
		"author":  book.Media.Metadata.AuthorName,
	})

	// Check if we should skip this book based on incremental sync
	if s.config.Sync.Incremental {
		// Get the most recent activity timestamp from the book
		lastActivity := int64(0)
		if book.Progress.FinishedAt > 0 {
			lastActivity = book.Progress.FinishedAt
		} else if book.Progress.StartedAt > 0 {
			lastActivity = book.Progress.StartedAt
		}

		// Get the last sync time for this book from state
		bookState, exists := s.state.Books[book.ID]
		if exists && lastActivity <= bookState.LastUpdated && !s.config.App.DryRun {
			bookLog.Debug("Skipping unchanged book in incremental sync mode", map[string]interface{}{
				"last_activity": time.Unix(lastActivity, 0).Format(time.RFC3339),
				"last_sync":     time.Unix(bookState.LastUpdated, 0).Format(time.RFC3339),
			})
			return nil
		}

		bookLog.Debug("Processing changed book", map[string]interface{}{
			"last_activity": time.Unix(lastActivity, 0).Format(time.RFC3339),
			"last_sync":     time.Unix(bookState.LastUpdated, 0).Format(time.RFC3339),
		})
	}

	// Apply test book filter if configured
	if s.config.App.TestBookFilter != "" {
		// Check if the book title contains the filter string (case-insensitive)
		if !strings.Contains(strings.ToLower(book.Media.Metadata.Title), strings.ToLower(s.config.App.TestBookFilter)) {
			bookLog.Debugf("Skipping book as it doesn't match test book filter: %s", s.config.App.TestBookFilter)
			return nil
		}
		bookLog.Debugf("Book matches test book filter, processing: %s", s.config.App.TestBookFilter)
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
	case "TO_READ":
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
	hcBook, err := s.findBookInHardcover(ctx, book)
	if err != nil {
		errMsg := "error finding book in Hardcover"
		bookLog.Error("Error finding book in Hardcover, skipping", map[string]interface{}{
			"error": err,
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
			if errors.As(err, &bookErr) && bookErr.BookID != "" {
				bookID = bookErr.BookID
				bookLog.Info("Found book ID in BookError", map[string]interface{}{
					"book_id": bookID,
					"error":    bookErr.Error(),
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
			fmt.Sprintf("%s: %v", errMsg, err),
			book.Media.Duration,
			book.ID,
			s.hardcover, // Pass the Hardcover client for publisher lookup
		)
		return nil // Skip this book but continue with others
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
		return nil
	}

	// Get or create a user book ID for this edition
	editionID := hcBook.EditionID
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
		if errors.As(err, &bookErr) && bookErr.BookID != "" {
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
		if err := s.handleFinishedBook(ctx, userBookID, editionID, book); err != nil {
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

		// Call handleInProgressBook to update the progress
		if err := s.handleInProgressBook(ctx, userBookID, book); err != nil {
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

// handleFinishedBook processes a book that has been marked as finished
func (s *Service) handleFinishedBook(ctx context.Context, userBookID int64, editionID string, book models.AudiobookshelfBook) error {
	// Create a logger with context
	log := s.createFinishedBookLogger(userBookID, editionID, book)
	log.Info("Handling finished book", nil)

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
			"index":          i,
			"read_id":        read.ID,
			"started_at":     read.StartedAt,
			"finished_at":    read.FinishedAt,
			"progress":       read.Progress,
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
				"finished_at":     finishedAt,
				"progress":        progress, // Always set to 100% when marking as finished
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
				FinishedAt:     &finishedAt,
				StartedAt:      &startedAt,
				ProgressSeconds: &finalProgressSeconds, // This will effectively set progress to 100%
				EditionID:      &editionIDInt,
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
func (s *Service) handleInProgressBook(ctx context.Context, userBookID int64, book models.AudiobookshelfBook) error {
	// Initialize logger context
	logCtx := map[string]interface{}{
		"function":     "handleInProgressBook",
		"user_book_id": userBookID,
		"book_id":      book.ID,
	}

	// Add title and author if available
	if book.Media.Metadata.Title != "" {
		logCtx["title"] = book.Media.Metadata.Title
	}
	if book.Media.Metadata.AuthorName != "" {
		logCtx["author"] = book.Media.Metadata.AuthorName
	}

	// Create a logger with context
	log := s.log.With(logCtx)
	log.Info("Processing in-progress book", nil)

	// Get current book status from Hardcover
	hcBook, err := s.hardcover.GetUserBook(ctx, strconv.FormatInt(userBookID, 10))
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

	// Find the most appropriate read status to update
	var readStatusToUpdate *hardcover.UserBookRead
	var mostRecentRead *hardcover.UserBookRead
	var mostRecentTime time.Time

	// First, try to find an existing read status that matches our criteria
	for i := range readStatuses {
		read := &readStatuses[i]
		
		// If we find an unfinished read status, use that
		if read.FinishedAt == nil {
			readStatusToUpdate = read
			break
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

	// If no read status found at all, we'll create a new one
	if readStatusToUpdate == nil && mostRecentRead == nil {
		log.Info("No existing read status found, will create a new one", logCtx)
	} else {
		// Use the most recent read status if we didn't find an unfinished one
		if readStatusToUpdate == nil && mostRecentRead != nil {
			readStatusToUpdate = mostRecentRead
			logCtx["using_most_recent_read"] = true
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
				logCtx["abs_progress_time"] = time.Duration(int64(book.Progress.CurrentTime)*int64(time.Second)).String()
				logCtx["hc_progress_time"] = time.Duration(int64(hcProgressSeconds)*int64(time.Second)).String()
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
		hcBook, err := s.hardcover.GetUserBook(ctx, strconv.FormatInt(userBookID, 10))
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

	// Default to "TO_READ" if no progress and not finished
	return "TO_READ"
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

// findBookInHardcoverByTitleAuthor searches for a book in Hardcover by title and author
// This should only be used for mismatch handling when a book can't be found by ASIN/ISBN
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

	// Get the first result (most relevant)
	firstResult := searchResults[0]

	// Add book ID to logger
	log = log.With(map[string]interface{}{
		"book_id": firstResult.ID,
	})

	// Create a new HardcoverBook from the search result
	hcBook := &models.HardcoverBook{
		ID:           firstResult.ID,
		Title:        firstResult.Title,
		BookStatusID: 0, // Will be set when we get the full book details
	}

	log.Debug("Created HardcoverBook from search result", map[string]interface{}{
		"id":    hcBook.ID,
		"title": hcBook.Title,
	})

	// Try to get the first audio edition
	edition, err := s.hardcover.GetEdition(ctx, firstResult.ID)
	if err != nil {
		log.Warn("Failed to get edition details, treating as mismatch", map[string]interface{}{
			"error":      err.Error(),
			"book_id":    hcBook.ID,
			"book_title": hcBook.Title,
		})
		// If we can't get edition details, we should treat this as a mismatch
		return nil, fmt.Errorf("edition not found for book %s (%s)", hcBook.ID, hcBook.Title)
	}

	if edition != nil {
		hcBook.EditionID = edition.ID
		hcBook.EditionASIN = edition.ASIN
		hcBook.EditionISBN13 = edition.ISBN13
		hcBook.EditionISBN10 = edition.ISBN10

		log.Debug("Updated HardcoverBook with edition details", map[string]interface{}{
			"book_id":        hcBook.ID,
			"edition_id":     hcBook.EditionID,
			"edition_asin":   hcBook.EditionASIN,
			"edition_isbn10": hcBook.EditionISBN10,
			"edition_isbn13": hcBook.EditionISBN13,
		})

		// We must have a valid edition ID to proceed with user book creation
		if hcBook.EditionID == "" {
			log.Warn("Cannot create user book: edition ID is empty", map[string]interface{}{
				"book_id": hcBook.ID,
				"title":   hcBook.Title,
			})
			return nil, fmt.Errorf("edition ID is empty for book %s (%s)", hcBook.ID, hcBook.Title)
		}

		log.Info("Successfully found book by title and author with edition details", map[string]interface{}{
			"book_id":      hcBook.ID,
			"edition_id":   hcBook.EditionID,
			"user_book_id": hcBook.UserBookID,
			"title":        hcBook.Title,
		})
	} else {
		log.Info("No edition details available for book", map[string]interface{}{
			"book_id": hcBook.ID,
			"title":   hcBook.Title,
		})
	}

	return hcBook, nil
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
		log.Info(fmt.Sprintf("Searching for book by ASIN: %s", book.Media.Metadata.ASIN), nil)

		hcBook, err := s.hardcover.SearchBookByASIN(ctx, book.Media.Metadata.ASIN)
		if err != nil {
			// Check if this is a BookError with a book ID
			var bookErr *hardcover.BookError
			if errors.As(err, &bookErr) && bookErr.BookID != "" {
				log.Info("Found book ID in BookError", map[string]interface{}{
					"book_id": bookErr.BookID,
					"error":    bookErr.Error(),
				})
				// Create a minimal book with just the ID
				return &models.HardcoverBook{
					ID: bookErr.BookID,
				}, nil
			}
			log.Warn(fmt.Sprintf("Search by ASIN failed, will try other methods: %v", err), nil)
		} else if hcBook != nil {
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
			if errors.As(err, &bookErr) {
				log.Info("Found book ID in BookError from ISBN-13 search", map[string]interface{}{
					"book_id": bookErr.BookID,
					"error":    bookErr.Error(),
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
			if errors.As(err, &bookErr) && bookErr.BookID != "" {
				log.Info("Found book ID in BookError from ISBN-10 search", map[string]interface{}{
					"book_id": bookErr.BookID,
					"error":    bookErr.Error(),
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

		log.Info("Found book by title/author with valid edition", map[string]interface{}{
			"search_method": "title_author",
			"book_id":       hcBook.ID,
			"edition_id":    hcBook.EditionID,
			"user_book_id":  hcBook.UserBookID,
			"title":         hcBook.Title,
			"author":        book.Media.Metadata.AuthorName,
		})

		// Log the complete state of the book before returning
		log.Debug("Returning HardcoverBook from title/author search", map[string]interface{}{
			"book": fmt.Sprintf("%+v", *hcBook),
		})

		return hcBook, nil
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
