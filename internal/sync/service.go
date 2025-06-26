package sync

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// Error definitions
var (
	ErrSkippedBook = errors.New("book was skipped")
)

// parseDate parses a date string in either RFC3339 or YYYY-MM-DD format
func parseDate(dateStr string) (time.Time, error) {
	// Try parsing as RFC3339 first (with time)
	t, err := time.Parse(time.RFC3339, dateStr)
	if err == nil {
		return t, nil
	}

	// Try parsing as YYYY-MM-DD
	t, err = time.Parse("2006-01-02", dateStr)
	if err == nil {
		// Set to noon to avoid timezone issues
		return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, time.UTC), nil
	}

	return time.Time{}, fmt.Errorf("date string '%s' is not in a recognized format (RFC3339 or YYYY-MM-DD)", dateStr)
}

// Service handles the synchronization between Audiobookshelf and Hardcover
type Service struct {
	audiobookshelf *audiobookshelf.Client
	hardcover      *hardcover.Client
	config         *Config
	log            *logger.Logger
}

// Config is the configuration type for the sync service
type Config = config.Config

// NewService creates a new sync service
func NewService(absClient *audiobookshelf.Client, hcClient *hardcover.Client, cfg *Config) *Service {
	return &Service{
		audiobookshelf: absClient,
		hardcover:      hcClient,
		config:         cfg,
		log:            logger.Get(),
	}
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
	// Log the start of the sync
	s.log.Info("========================================", map[string]interface{}{
		"dry_run":          s.config.App.DryRun,
		"test_book_filter": s.config.App.TestBookFilter,
		"test_book_limit":  s.config.App.TestBookLimit,
	})
	s.log.Info("STARTING FULL SYNCHRONIZATION", nil)
	s.log.Info("========================================", nil)

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
		"minimum_progress":     s.config.App.MinimumProgress,
		"audiobook_match_mode": s.config.App.AudiobookMatchMode,
		"sync_want_to_read":    s.config.App.SyncWantToRead,
		"sync_owned":           s.config.App.SyncOwned,
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

// isBookOwned checks if a book is already marked as owned in Hardcover by checking the user's "Owned" list
func (s *Service) isBookOwned(ctx context.Context, bookID string) (bool, error) {
	// Convert bookID to int for the query
	bookIDInt, err := strconv.Atoi(bookID)
	if err != nil {
		return false, fmt.Errorf("invalid book ID format: %w", err)
	}

	s.log.Debug("Checking if book is marked as owned in Hardcover", map[string]interface{}{
		"book_id": bookIDInt,
	})

	// Use the hardcover client's CheckBookOwnership method
	isOwned, err := s.hardcover.CheckBookOwnership(ctx, bookIDInt)
	if err != nil {
		s.log.Error("Failed to check book ownership", map[string]interface{}{
			"error":   err,
			"book_id": bookIDInt,
		})
		return false, fmt.Errorf("failed to check book ownership: %w", err)
	}

	s.log.Debug("Checked book ownership in Hardcover", map[string]interface{}{
		"book_id":  bookIDInt,
		"is_owned": isOwned,
	})

	return isOwned, nil
}

// markBookAsOwned marks a book as owned in Hardcover using the edition_owned mutation
// This works with edition IDs, not book IDs
func (s *Service) markBookAsOwned(ctx context.Context, bookID, editionID string) error {
	if editionID == "" {
		return fmt.Errorf("edition ID is required for marking book as owned")
	}

	// Convert edition ID to integer
	editionIDInt, err := strconv.Atoi(editionID)
	if err != nil {
		return fmt.Errorf("invalid edition ID format: %w", err)
	}

	// Convert book ID to integer for logging
	bookIDInt, err := strconv.Atoi(bookID)
	if err != nil {
		return fmt.Errorf("invalid book ID format: %w", err)
	}

	// Log the ownership marking attempt
	s.log.Debug("Attempting to mark book as owned in Hardcover", map[string]interface{}{
		"book_id":    bookIDInt,
		"edition_id": editionIDInt,
	})

	// Use the hardcover client to mark the edition as owned
	err = s.hardcover.MarkEditionAsOwned(ctx, editionIDInt)
	if err != nil {
		s.log.Error("Failed to mark book as owned", map[string]interface{}{
			"error":      err,
			"book_id":    bookIDInt,
			"edition_id": editionIDInt,
		})
		return fmt.Errorf("failed to mark book as owned: %w", err)
	}

	s.log.Debug("Successfully marked book as owned in Hardcover", map[string]interface{}{
		"book_id":    bookIDInt,
		"edition_id": editionIDInt,
	})

	return nil
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
	case "READING":
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

// stringValue safely dereferences a string pointer, returning an empty string if the pointer is nil
func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// safeString is an alias for stringValue for backward compatibility
func safeString(s *string) string {
	return stringValue(s)
}

// stringPtr returns a pointer to the given string value
func stringPtr(s string) *string {
	return &s
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

	// Check if this is a re-read by looking for existing finished reads
	hasFinished, err := s.hardcover.CheckExistingFinishedRead(ctx, hardcover.CheckExistingFinishedReadInput{
		UserBookID: int(userBookID),
	})

	if err != nil {
		log.Error("Failed to check for existing finished reads", map[string]interface{}{
			"error": err,
		})
		return fmt.Errorf("error checking for existing finished reads: %w", err)
	}

	// If this is a re-read, we need to check if we should create a new read record
	if hasFinished.HasFinishedRead {
		// Only create a new read record if the book was finished more than 30 days ago
		// or if we don't have a finished_at date (for backward compatibility)
		createNewRead := false
		if hasFinished.LastFinishedAt == nil {
			log.Info("Found existing finished read without a finish date, creating new read record", nil)
			createNewRead = true
		} else {
			// Try parsing as date first (YYYY-MM-DD), then as full timestamp
			var lastFinished time.Time
			var parseErr error

			// First try parsing as date (YYYY-MM-DD)
			lastFinished, parseErr = time.Parse("2006-01-02", *hasFinished.LastFinishedAt)
			if parseErr != nil {
				// If that fails, try parsing as full timestamp
				lastFinished, parseErr = time.Parse(time.RFC3339, *hasFinished.LastFinishedAt)
			}

			if parseErr != nil {
				log.Warn("Failed to parse last finished date, creating new read record", map[string]interface{}{
					"error":            parseErr.Error(),
					"last_finished_at": *hasFinished.LastFinishedAt,
				})
				createNewRead = true
			} else if time.Since(lastFinished) > 30*24*time.Hour {
				log.Info("Found existing finished read from more than 30 days ago, creating new read record", map[string]interface{}{
					"last_finished_at": *hasFinished.LastFinishedAt,
				})
				createNewRead = true
			} else {
				log.Info("Skipping creation of new read record - book was finished recently", map[string]interface{}{
					"last_finished_at": *hasFinished.LastFinishedAt,
				})
			}
		}

		if createNewRead {
			// Create a new read record for the re-read
			finishedAt := time.Now().Format(time.RFC3339)
			_, err = s.hardcover.InsertUserBookRead(ctx, hardcover.InsertUserBookReadInput{
				UserBookID: userBookID,
				DatesRead: hardcover.DatesReadInput{
					FinishedAt: &finishedAt,
				},
			})

			if err != nil {
				log.Error("Failed to create new read record for re-read", map[string]interface{}{
					"error": err.Error(),
				})
				return fmt.Errorf("error creating new read record: %w", err)
			}

			log.Infof("Successfully created new read record for re-read")
		}
	} else {
		log.Info("First time finishing this book", nil)
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
	hcBook, err := s.hardcover.GetUserBook(ctx, userBookID)
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
	if hcBook != nil && hcBook.Progress != nil {
		logCtx["hardcover_progress"] = *hcBook.Progress
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

	// Check if we need to update the progress
	needsUpdate := false
	var latestRead *hardcover.UserBookRead

	// Find the most recent read status
	for i, read := range readStatuses {
		if latestRead == nil {
			latestRead = &readStatuses[i]
			continue
		}

		if read.FinishedAt == nil || latestRead.FinishedAt == nil {
			// If either timestamp is nil, skip comparison
			continue
		}

		// Parse the timestamps for comparison
		readTime, err1 := time.Parse(time.RFC3339, *read.FinishedAt)
		latestTime, err2 := time.Parse(time.RFC3339, *latestRead.FinishedAt)

		// If parsing fails, skip this comparison
		if err1 != nil || err2 != nil {
			continue
		}

		if readTime.After(latestTime) {
			latestRead = &readStatuses[i]
		}
	}

	// Check if we need to update based on progress difference
	if latestRead != nil {
		progressDiff := math.Abs(float64(book.Progress.CurrentTime - latestRead.Progress))
		minDiff := 60.0 // Minimum 1 minute difference to trigger an update

		if progressDiff >= minDiff {
			needsUpdate = true
			reason := fmt.Sprintf("progress difference (%.2f) >= min threshold (%.2f)", progressDiff, minDiff)
			logCtx["update_reason"] = reason
			logCtx["abs_progress"] = book.Progress.CurrentTime
			logCtx["hc_progress"] = latestRead.Progress
			logCtx["progress_diff"] = progressDiff
			log.With(logCtx).Info(reason, nil)
		} else {
			reason := fmt.Sprintf("progress difference (%.2f) < min threshold (%.2f)", progressDiff, minDiff)
			logCtx["update_reason"] = reason
			logCtx["abs_progress"] = book.Progress.CurrentTime
			logCtx["hc_progress"] = latestRead.Progress
			logCtx["progress_diff"] = progressDiff
			log.With(logCtx).Info(reason, nil)
		}
	} else if hcBook == nil || hcBook.Progress == nil {
		// No existing progress in Hardcover, we need to update
		needsUpdate = true
		log.With(logCtx).Info("No existing progress in Hardcover", nil)
	} else {
		// Fallback to book progress if read status is not available
		progressDiff := math.Abs(book.Progress.CurrentTime - *hcBook.Progress)
		minDiff := 60.0 // Minimum 1 minute difference to trigger an update
		logCtx["hc_book_progress"] = *hcBook.Progress
		logCtx["progress_diff"] = progressDiff

		if progressDiff >= minDiff {
			needsUpdate = true
			reason := fmt.Sprintf("book progress difference (%.2f) >= min threshold (%.2f)", progressDiff, minDiff)
			logCtx["update_reason"] = reason
			s.log.With(logCtx).Info(reason, nil)
		} else {
			reason := fmt.Sprintf("book progress difference (%.2f) < min threshold (%.2f)", progressDiff, minDiff)
			logCtx["update_reason"] = reason
			s.log.With(logCtx).Info(reason, nil)
		}
	}

	if needsUpdate && len(readStatuses) > 0 {
		// Prepare the update object with required fields
		updateObj := map[string]interface{}{
			"progress_seconds":  int64(book.Progress.CurrentTime),
			"reading_format_id": 2, // 2 = Audiobook format
		}

		// Format dates as YYYY-MM-DD strings
		if book.Progress.StartedAt > 0 {
			updateObj["started_at"] = time.Unix(book.Progress.StartedAt/1000, 0).Format("2006-01-02")
		}

		if book.Progress.IsFinished && book.Progress.FinishedAt > 0 {
			updateObj["finished_at"] = time.Unix(book.Progress.FinishedAt/1000, 0).Format("2006-01-02")
		}

		// Include edition_id if available from the existing read
		if readStatuses[0].EditionID != nil {
			updateObj["edition_id"] = *readStatuses[0].EditionID
		}

		// Create the update input
		updateInput := hardcover.UpdateUserBookReadInput{
			ID:     readStatuses[0].ID,
			Object: updateObj,
		}

		// Update the read with the current progress
		_, err = s.hardcover.UpdateUserBookRead(ctx, updateInput)
		if err != nil {
			errCtx := make(map[string]interface{}, len(logCtx)+1)
			for k, v := range logCtx {
				errCtx[k] = v
			}
			errCtx["error"] = err.Error()
			s.log.With(errCtx).Error("Failed to update progress")
			return fmt.Errorf("failed to update progress: %w", err)
		}

		// Log successful update
		logCtx["updated"] = true
		log = s.log.With(logCtx)
		log.Info("Successfully updated reading progress in Hardcover", nil)
	} else if needsUpdate {
		log = s.log.With(logCtx)
		log.Info("No read statuses available to update", nil)
	} else {
		log = s.log.With(logCtx)
		log.Info("Skipped progress update - no significant change", nil)
	}

	return nil
}

// updateInProgressReads updates the in-progress reads
func (s *Service) updateInProgressReads(ctx context.Context, log *logger.Logger, reads []hardcover.UserBookRead, now time.Time) error {
	// Initialize logger with context if not provided
	if log == nil {
		log = s.log.With(map[string]interface{}{
			"function": "updateInProgressReads",
		})
	}

	// Add context about the operation
	logCtx := map[string]interface{}{
		"function":   "updateInProgressReads",
		"read_count": len(reads),
		"dry_run":    s.config.App.DryRun,
	}

	// Create a new logger with the combined context
	log = s.log.With(logCtx)

	if len(reads) == 0 {
		log.Info("No reads to update", nil)
		return nil
	}

	// Log the update operation with context
	log.Info("Updating in-progress reads", nil)

	// In dry-run mode, log what would be updated and return
	if s.config.App.DryRun {
		log.Info("Dry-run: would update reads", nil)
		return nil
	}

	// Process each read
	for i, read := range reads {
		// Create a logger with read-specific context
		readLogCtx := make(map[string]interface{}, len(logCtx)+3)
		for k, v := range logCtx {
			readLogCtx[k] = v
		}
		readLogCtx["read_index"] = i
		readLogCtx["read_id"] = read.ID
		readLogCtx["progress"] = read.Progress

		readLog := log.With(readLogCtx)

		// Log the read being processed
		readLog.Info("Processing read", nil)

		// Prepare the update input
		updateInput := hardcover.UpdateUserBookReadInput{
			ID: read.ID,
			Object: map[string]interface{}{
				"progress_seconds": read.ProgressSeconds,
			},
		}

		// Only include started_at if it's not nil
		if read.StartedAt != nil {
			updateInput.Object["started_at"] = *read.StartedAt
		}

		// Only include finished_at if it's not nil
		if read.FinishedAt != nil {
			updateInput.Object["finished_at"] = *read.FinishedAt
		}

		// Update the read with the current progress
		updated, err := s.hardcover.UpdateUserBookRead(ctx, updateInput)

		if err != nil {
			errCtx := make(map[string]interface{}, len(readLogCtx)+1)
			for k, v := range readLogCtx {
				errCtx[k] = v
			}
			errCtx["error"] = err.Error()
			log.With(errCtx).Error("Failed to update read")
			continue
		}

		// Log successful update with updated status
		readLogCtx["updated"] = updated
		readLog = log.With(readLogCtx)
		readLog.Info("Successfully updated read", nil)
	}

	return nil
}

// updateBookStatus updates the status of a book
func (s *Service) updateBookStatus(ctx context.Context, log *logger.Logger, userBookID int64, dryRun bool) error {
	// Initialize context for the logger
	logCtx := map[string]interface{}{
		"function":     "updateBookStatus",
		"user_book_id": userBookID,
		"dry_run":      dryRun,
		"status":       "FINISHED",
	}

	// Create or update logger with context
	if log == nil {
		log = s.log.With(logCtx)
	} else {
		// Create a new logger with combined context
		log = log.With(logCtx)
	}

	// Log the status update
	log.Info("Updating book status", nil)

	// In dry-run mode, log what would be done and return
	if dryRun {
		log.Info("Dry-run: would update book status", nil)
		return nil
	}

	// Update the book status to FINISHED
	err := s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
		ID:     userBookID,
		Status: "FINISHED",
	})

	if err != nil {
		// Log error with context
		errCtx := make(map[string]interface{}, len(logCtx)+1)
		for k, v := range logCtx {
			errCtx[k] = v
		}
		errCtx["error"] = err.Error()
		log.Error("Failed to update book status", errCtx)
		return fmt.Errorf("failed to update book status: %w", err)
	}

	log.Info("Successfully updated book status", nil)
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
			log.Warn("Failed to get or create user book ID", map[string]interface{}{
				"edition_id": hcBook.EditionID,
				"error":      err.Error(),
			})
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

	// Search for books using the search API with a default limit of 10 results
	searchResults, err := s.hardcover.SearchBooks(ctx, searchQuery, 10)
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
