package sync

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
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

	editionIDForLog := int(editionIDInt)
	logCtx := s.log.With().Int("editionID", editionIDForLog).Logger()

	// Check if we already have a user book ID for this edition
	userBookID, err := s.hardcover.GetUserBookID(context.Background(), editionIDForLog)
	if err != nil {
		logCtx.Error().
			Err(err).
			Msg("Error checking for existing user book ID")
		return 0, fmt.Errorf("error checking for existing user book ID: %w", err)
	}

	// If we found an existing user book ID, return it
	if userBookID > 0 {
		logCtx.Debug().
			Int("userBookID", userBookID).
			Msg("Found existing user book ID")
		return int64(userBookID), nil
	}

	logCtx.Debug().Msg("No existing user book ID found, will create new one")

	// If dry-run mode is enabled, log and return early without creating
	if s.config.App.DryRun {
		logCtx.Info().
			Str("status", status).
			Msg("[DRY-RUN] Would create new user book with status")
		// Return a negative value to indicate dry-run mode
		return -1, nil
	}

	logCtx.Info().
		Str("status", status).
		Msg("Creating new user book with status")

	// Double-check if the user book exists to prevent race conditions
	userBookID, err = s.hardcover.GetUserBookID(context.Background(), editionIDForLog)
	if err != nil {
		logCtx.Error().
			Err(err).
			Msg("Error in second check for existing user book ID")
		return 0, fmt.Errorf("error in second check for existing user book ID: %w", err)
	}

	// If we found an existing user book ID in the second check, return it
	if userBookID > 0 {
		logCtx.Debug().
			Int("userBookID", userBookID).
			Msg("Found existing user book ID in second check")
		return int64(userBookID), nil
	}

	// Create a new user book with the specified status
	newUserBookID, err := s.hardcover.CreateUserBook(ctx, editionID, status)
	if err != nil {
		s.log.Error().
			Err(err).
			Int64("editionID", editionIDInt).
			Str("status", status).
			Msg("Failed to create user book")
		return 0, fmt.Errorf("failed to create user book: %w", err)
	}

	// Convert the new user book ID to an integer64
	userBookID64, err := strconv.ParseInt(newUserBookID, 10, 64)
	if err != nil {
		s.log.Error().
			Err(err).
			Str("userBookID", newUserBookID).
			Msg("Invalid user book ID format")
		return 0, fmt.Errorf("invalid user book ID format: %w", err)
	}

	s.log.Info().
		Int64("editionID", editionIDInt).
		Int64("userBookID", userBookID64).
		Str("status", status).
		Msg("Successfully created new user book with status")

	return userBookID64, nil
}

// Sync performs a full synchronization between Audiobookshelf and Hardcover
func (s *Service) Sync(ctx context.Context) error {
	// Log the start of the sync
	s.log.Info().
		Bool("dry_run", s.config.App.DryRun).
		Str("test_book_filter", s.config.App.TestBookFilter).
		Int("test_book_limit", s.config.App.TestBookLimit).
		Msg("========================================")
	s.log.Info().Msg("STARTING FULL SYNCHRONIZATION")
	s.log.Info().Msg("========================================")

	// Log service configuration (without accessing unexported fields directly)
	s.log.Info().Msg("SYNC CONFIGURATION")
	s.log.Info().Msg("========================================")
	s.log.Info().
		Str("audiobookshelf_url", s.config.Audiobookshelf.URL).
		Bool("has_audiobookshelf_token", s.config.Audiobookshelf.Token != "").
		Msg("Audiobookshelf Configuration")

	s.log.Info().
		Bool("has_hardcover_token", s.config.Hardcover.Token != "").
		Msg("Hardcover Configuration")

	s.log.Info().
		Float64("minimum_progress", s.config.App.MinimumProgress).
		Str("audiobook_match_mode", s.config.App.AudiobookMatchMode).
		Bool("sync_want_to_read", s.config.App.SyncWantToRead).
		Bool("sync_owned", s.config.App.SyncOwned).
		Msg("Sync Settings")

	s.log.Info().Msg("========================================")

	// Initialize the total books limit from config
	totalBooksLimit := s.config.App.TestBookLimit

	// Log configuration for debugging
	s.log.Debug().
		Str("audiobookshelf_url", s.config.Audiobookshelf.URL).
		Bool("has_audiobookshelf_token", s.config.Audiobookshelf.Token != "").
		Bool("has_hardcover_token", s.config.Hardcover.Token != "").
		Float64("minimum_progress_threshold", s.config.App.MinimumProgress).
		Bool("sync_want_to_read", s.config.App.SyncWantToRead).
		Int("test_book_limit", totalBooksLimit).
		Msg("Debug configuration")

	// Fetch user progress data from Audiobookshelf
	s.log.Info().Msg("Fetching user progress data from Audiobookshelf...")
	userProgress, err := s.audiobookshelf.GetUserProgress(ctx)
	if err != nil {
		s.log.Warn().Err(err).Msg("Failed to fetch user progress data, falling back to basic progress tracking")
	} else {
		s.log.Info().
			Int("media_progress_items", len(userProgress.MediaProgress)).
			Int("listening_sessions", len(userProgress.ListeningSessions)).
			Msg("Fetched user progress data")
	}

	// Get all libraries from Audiobookshelf
	s.log.Info().Msg("Fetching libraries from Audiobookshelf...")
	libraries, err := s.audiobookshelf.GetLibraries(ctx)
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to fetch libraries")
		return fmt.Errorf("failed to fetch libraries: %w", err)
	}

	s.log.Info().
		Int("libraries_count", len(libraries)).
		Msg("Found libraries")

	// Track total books processed across all libraries
	totalBooksProcessed := 0

	// Log the test book limit if it's set
	if totalBooksLimit > 0 {
		s.log.Info().
			Int("test_book_limit", totalBooksLimit).
			Msg("Test book limit is active")
	}

	// Process each library
	for i := range libraries {
		// Skip processing if we've reached the limit
		if totalBooksLimit > 0 && totalBooksProcessed >= totalBooksLimit {
			s.log.Info().
				Int("limit", totalBooksLimit).
				Int("already_processed", totalBooksProcessed).
				Msg("Reached test book limit before processing library")
			break
		}

		// Process the library and get the number of books processed
		processed, err := s.processLibrary(ctx, &libraries[i], totalBooksLimit-totalBooksProcessed, userProgress)
		if err != nil {
			s.log.Error().Err(err).Str("library_id", libraries[i].ID).Msg("Failed to process library")
			continue
		}

		totalBooksProcessed += processed

		// Log progress
		if totalBooksLimit > 0 {
			s.log.Info().
				Int("processed", totalBooksProcessed).
				Int("limit", totalBooksLimit).
				Msg("Progress towards test book limit")

			if totalBooksProcessed >= totalBooksLimit {
				s.log.Info().
					Int("limit", totalBooksLimit).
					Int("processed", totalBooksProcessed).
					Msg("Successfully reached test book limit, stopping processing")
				break
			}
		}
	}

	s.log.Info().Msg("Sync completed successfully")
	return nil
}

// processLibrary processes a library and returns the number of books processed
func (s *Service) processLibrary(ctx context.Context, library *audiobookshelf.AudiobookshelfLibrary, maxBooks int, userProgress *models.AudiobookshelfUserProgress) (int, error) {
	// Create a logger with library context
	libraryLog := s.log.With().
		Str("library_id", library.ID).
		Str("library_name", library.Name).
		Logger()

	libraryLog.Info().Msg("Processing library")

	// Get all items from the library
	items, err := s.audiobookshelf.GetLibraryItems(ctx, library.ID)
	if err != nil {
		return 0, fmt.Errorf("failed to get library items: %w", err)
	}

	libraryLog.Info().
		Str("library_id", library.ID).
		Str("library_name", library.Name).
		Int("items_count", len(items)).
		Msg("Found items in library")

	// If we have a maxBooks limit, apply it
	if maxBooks > 0 && len(items) > maxBooks {
		libraryLog.Info().
			Int("original_count", len(items)).
			Int("limit", maxBooks).
			Msg("Limiting number of books to process based on remaining test book limit")
		items = items[:maxBooks]
	}

	// Process each item in the library
	processed := 0
	for _, book := range items {
		// Process the item
		err := s.processBook(ctx, book, userProgress)
		if err != nil {
			libraryLog.Error().Err(err).Str("item_id", book.ID).Msg("Failed to process item")
			continue
		}

		processed++
	}

	libraryLog.Info().
		Str("library_id", library.ID).
		Str("library_name", library.Name).
		Int("processed", processed).
		Msg("Finished processing library")

	return processed, nil
}

// isBookOwned checks if a book is already marked as owned in Hardcover by checking the user's "Owned" list
func (s *Service) isBookOwned(ctx context.Context, bookID string) (bool, error) {
	// Convert bookID to int for the query
	bookIDInt, err := strconv.Atoi(bookID)
	if err != nil {
		return false, fmt.Errorf("invalid book ID format: %w", err)
	}

	s.log.Debug().
		Int("book_id", bookIDInt).
		Msg("Checking if book is marked as owned in Hardcover")

	// Use the hardcover client's CheckBookOwnership method
	isOwned, err := s.hardcover.CheckBookOwnership(ctx, bookIDInt)
	if err != nil {
		s.log.Error().
			Err(err).
			Int("book_id", bookIDInt).
			Msg("Failed to check book ownership")
		return false, fmt.Errorf("failed to check book ownership: %w", err)
	}

	s.log.Debug().
		Int("book_id", bookIDInt).
		Bool("is_owned", isOwned).
		Msg("Checked book ownership in Hardcover")

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
	s.log.Debug().
		Int("book_id", bookIDInt).
		Int("edition_id", editionIDInt).
		Msg("Attempting to mark book as owned in Hardcover")

	// Use the hardcover client to mark the edition as owned
	err = s.hardcover.MarkEditionAsOwned(ctx, editionIDInt)
	if err != nil {
		s.log.Error().
			Err(err).
			Int("book_id", bookIDInt).
			Int("edition_id", editionIDInt).
			Msg("Failed to mark book as owned")
		return fmt.Errorf("failed to mark book as owned: %w", err)
	}

	s.log.Debug().
		Int("book_id", bookIDInt).
		Int("edition_id", editionIDInt).
		Msg("Successfully marked book as owned in Hardcover")

	return nil
}

// processBook processes a single book and updates its status in Hardcover
func (s *Service) processBook(ctx context.Context, book models.AudiobookshelfBook, userProgress *models.AudiobookshelfUserProgress) error {
	// Create a logger with book context
	bookLog := s.log.With().
		Str("book_id", book.ID).
		Str("title", book.Media.Metadata.Title).
		Str("author", book.Media.Metadata.AuthorName).
		Logger()

	bookLog.Debug().Msg("Starting to process book")

	// Apply test book filter if configured
	if s.config.App.TestBookFilter != "" {
		// Check if the book title contains the filter string (case-insensitive)
		if !strings.Contains(strings.ToLower(book.Media.Metadata.Title), strings.ToLower(s.config.App.TestBookFilter)) {
			bookLog.Debug().
				Str("filter", s.config.App.TestBookFilter).
				Msg("Skipping book as it doesn't match test book filter")
			return nil
		}
		bookLog.Debug().
			Str("filter", s.config.App.TestBookFilter).
			Msg("Book matches test book filter, processing...")
	}

	// Enhance book data with user progress if available
	if userProgress != nil {
		// Try to find matching progress in mediaProgress (most accurate source)
		var bestProgress *struct {
			ID             string  `json:"id"`
			LibraryItemID  string  `json:"libraryItemId"`
			UserID         string  `json:"userId"`
			IsFinished     bool    `json:"isFinished"`
			Progress       float64 `json:"progress"`
			CurrentTime    float64 `json:"currentTime"`
			Duration       float64 `json:"duration"`
			StartedAt      int64   `json:"startedAt"`
			FinishedAt     int64   `json:"finishedAt"`
			LastUpdate     int64   `json:"lastUpdate"`
			TimeListening  float64 `json:"timeListening"`
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
			bookLog = bookLog.With().
				Bool("has_media_progress", true).
				Float64("progress", bestProgress.Progress).
				Bool("is_finished", bestProgress.IsFinished).
				Int64("last_update", bestProgress.LastUpdate).
				Logger()

			// Update book progress with the most accurate data
			book.Progress.CurrentTime = bestProgress.CurrentTime
			book.Progress.IsFinished = bestProgress.IsFinished
			book.Progress.FinishedAt = bestProgress.FinishedAt
			book.Progress.StartedAt = bestProgress.StartedAt

			bookLog.Debug().
				Float64("current_time", book.Progress.CurrentTime).
				Int64("finished_at", book.Progress.FinishedAt).
				Msg("Using enhanced progress from media progress data")
		} else {
			// Fall back to listening sessions if no media progress found
			var bestSession *struct {
				ID             string  `json:"id"`
				UserID         string  `json:"userId"`
				LibraryItemID  string  `json:"libraryItemId"`
				MediaType      string  `json:"mediaType"`
				MediaMetadata  struct {
					Title    string `json:"title"`
					Author   string `json:"author"`
				} `json:"mediaMetadata"`
				Duration       float64 `json:"duration"`
				CurrentTime    float64 `json:"currentTime"`
				Progress       float64 `json:"progress"`
				IsFinished     bool    `json:"isFinished"`
				StartedAt      int64   `json:"startedAt"`
				UpdatedAt      int64   `json:"updatedAt"`
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
				bookLog = bookLog.With().
					Bool("has_session_progress", true).
					Float64("session_progress", bestSession.Progress).
					Bool("session_finished", bestSession.IsFinished).
					Int64("session_updated", bestSession.UpdatedAt).
					Logger()

				book.Progress.CurrentTime = bestSession.CurrentTime
				book.Progress.IsFinished = bestSession.IsFinished

                bookLog.Debug().
                    Float64("current_time", book.Progress.CurrentTime).
                    Msg("Using progress from listening session")
            } else {
                bookLog.Debug().Msg("No enhanced progress data found in /api/me response")
            }
        }
    }

    // Skip books that haven't been started
    if book.Progress.CurrentTime <= 0 {
        bookLog.Debug().Float64("current_time", book.Progress.CurrentTime).Msg("Skipping unstarted book")
        return nil
    }

    // Calculate progress percentage based on current time and total duration
    var progress float64
    if book.Media.Duration > 0 && book.Progress.CurrentTime > 0 {
        progress = book.Progress.CurrentTime / book.Media.Duration
    }

    // Update logger with progress information
    bookLog = bookLog.With().
        Float64("progress", progress).
        Float64("current_time", book.Progress.CurrentTime).
        Float64("total_duration", book.Media.Duration).
        Bool("is_finished", book.Progress.IsFinished).
        Int64("finished_at", book.Progress.FinishedAt).
        Logger()

    bookLog.Debug().Msg("Calculated book progress")

    // Skip books below minimum progress threshold
    if progress < s.config.App.MinimumProgress && progress > 0 {
        bookLog.Debug().
            Float64("minimum_progress", s.config.App.MinimumProgress).
            Msg("Skipping book below minimum progress threshold")
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

    // Log the planned action
    bookLog.Info().
        Str("status", targetStatus).
        Str("action", action).
        Msg("Processing book")

	// Use the target status determined earlier
	status := targetStatus
	
	bookLog.Debug().
		Bool("is_finished", book.Progress.IsFinished).
		Int64("finished_at", book.Progress.FinishedAt).
		Float64("progress", progress).
		Str("status", status).
		Msg("Determined book status")

	// Find the book in Hardcover
	hcBook, err := s.findBookInHardcover(ctx, book)
	if err != nil {
		errMsg := "error finding book in Hardcover"
		bookLog.Error().
			Err(err).
			Msg("Error finding book in Hardcover, skipping")
		
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
				Duration:      book.Media.Duration,
			},
			"", // bookID
			"", // editionID
			fmt.Sprintf("%s: %v", errMsg, err),
			book.Media.Duration,
			book.ID,
		)
		return nil // Skip this book but continue with others
	}

	if hcBook == nil {
		errMsg := "could not find book in Hardcover"
		if book.Media.Metadata.Title == "" {
			errMsg = "book title is empty, cannot search by title/author"
		}
		bookLog.Error().
			Str("book_id", book.ID).
			Str("title", book.Media.Metadata.Title).
			Str("author", book.Media.Metadata.AuthorName).
			Str("isbn", book.Media.Metadata.ISBN).
			Str("asin", book.Media.Metadata.ASIN).
			Msg(errMsg)
		
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
				Duration:      book.Media.Duration,
			},
			"", // bookID
			"", // editionID
			errMsg,
			book.Media.Duration,
			book.ID,
		)
		return nil
	}

	// Get or create a user book ID for this edition
	editionID := hcBook.EditionID
	if editionID == "" {
		editionID = hcBook.ID // Fall back to book ID if no edition ID
	}

	// Log the edition ID we're using
	bookLog.Debug().
		Str("edition_id", editionID).
		Msg("Looking up or creating user book ID for edition")

	// Find or create a user book ID for this edition with the determined status
	userBookID, err := s.findOrCreateUserBookID(ctx, editionID, status)
	if err != nil {
		bookLog.Error().
			Err(err).
			Str("editionID", editionID).
			Msg("Failed to get or create user book ID")
		return fmt.Errorf("failed to get or create user book ID: %w", err)
	}

	// Log the book we're processing
	editionIDStr := editionID // Keep original string for logging
	bookLog = bookLog.With().
		Str("hardcover_id", hcBook.ID).
		Str("edition_id", editionIDStr).
		Int64("user_book_id", userBookID).
		Logger()

	bookLog.Debug().
		Int64("user_book_id", userBookID).
		Msg("Using user book ID for updates")

	// Handle progress update based on status
	switch status {
	case "FINISHED":
		// For finished books, we need to handle re-reads specially
		bookLog.Info().
			Str("status", status).
			Float64("progress", progress).
			Msg("Processing finished book")
		if err := s.handleFinishedBook(ctx, userBookID, editionID, book); err != nil {
			bookLog.Error().Err(err).Msg("Failed to handle finished book")
			return fmt.Errorf("error handling finished book: %w", err)
		}

	case "IN_PROGRESS", "READING":
		// Handle in-progress book
		bookLog.Info().
			Str("status", status).
			Float64("progress", progress).
			Msg("Processing in-progress book")

		// Call handleInProgressBook to update the progress
		if err := s.handleInProgressBook(ctx, userBookID, book); err != nil {
			bookLog.Error().
				Err(err).
				Msg("Failed to handle in-progress book")
			return fmt.Errorf("error handling in-progress book: %w", err)
		}
		return nil
	
	default:
		bookLog.Info().
			Str("status", status).
			Msg("No specific handling for book status")
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
	fields["dry_run"] = s.config.App.DryRun

	return logger.WithContext(fields)
}

// handleFinishedBook processes a book that has been marked as finished
func (s *Service) handleFinishedBook(ctx context.Context, userBookID int64, editionID string, book models.AudiobookshelfBook) error {
	// Create a logger with context
	log := s.createFinishedBookLogger(userBookID, editionID, book)
	log.Info().Msg("Handling finished book")

	// Check if this is a re-read by looking for existing finished reads
	hasFinished, err := s.hardcover.CheckExistingFinishedRead(ctx, hardcover.CheckExistingFinishedReadInput{
		UserBookID: int(userBookID),
	})

	if err != nil {
		log.Error().Err(err).Msg("Failed to check for existing finished reads")
		return fmt.Errorf("error checking for existing finished reads: %w", err)
	}

	// If this is a re-read, we need to check if we should create a new read record
	if hasFinished.HasFinishedRead {
		// Only create a new read record if the book was finished more than 30 days ago
		// or if we don't have a finished_at date (for backward compatibility)
		createNewRead := false
		if hasFinished.LastFinishedAt == nil {
			log.Info().Msg("Found existing finished read without a finish date, creating new read record")
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
				log.Warn().Err(parseErr).Str("last_finished_at", *hasFinished.LastFinishedAt).
					Msg("Failed to parse last finished date, creating new read record")
				createNewRead = true
			} else if time.Since(lastFinished) > 30*24*time.Hour {
				log.Info().
					Str("last_finished_at", *hasFinished.LastFinishedAt).
					Msg("Found existing finished read from more than 30 days ago, creating new read record")
				createNewRead = true
			} else {
				log.Info().
					Str("last_finished_at", *hasFinished.LastFinishedAt).
					Msg("Skipping creation of new read record - book was finished recently")
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
				log.Error().Err(err).Msg("Failed to create new read record for re-read")
				return fmt.Errorf("error creating new read record: %w", err)
			}

			log.Info().Msg("Successfully created new read record for re-read")
		}
	} else {
		log.Info().Msg("First time finishing this book")
	}

	return nil
}

// handleInProgressBook processes a book that is currently in progress
func (s *Service) handleInProgressBook(ctx context.Context, userBookID int64, book models.AudiobookshelfBook) error {
	// Create a logger with context from the service logger
	log := logger.WithContext(map[string]interface{}{
		"function":     "handleInProgressBook",
		"user_book_id": userBookID,
		"book_id":      book.ID,
		"title":        book.Media.Metadata.Title,
		"author":       book.Media.Metadata.AuthorName,
	})

	log.Info()

	// Get current book status from Hardcover
	hcBook, err := s.hardcover.GetUserBook(ctx, userBookID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get current book status from Hardcover")
		return fmt.Errorf("failed to get current book status: %w", err)
	}

	// Get the current read status to check progress
	readStatuses, err := s.hardcover.GetUserBookReads(ctx, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to get current read status from Hardcover")
		// Continue with update as we can't determine current progress
	}

	// Add progress information to log context
	logCtx := map[string]interface{}{
		"function":           "handleInProgressBook",
		"user_book_id":       userBookID,
		"book_id":            book.ID,
		"title":              book.Media.Metadata.Title,
		"author":             book.Media.Metadata.AuthorName,
		"current_time_sec":   book.Progress.CurrentTime,
		"is_finished":        book.Progress.IsFinished,
		"started_at":         book.Progress.StartedAt,
		"finished_at":        book.Progress.FinishedAt,
		"duration_seconds":   book.Media.Duration,
	}

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
	log = logger.WithContext(logCtx)

	// In dry-run mode, log that we're in dry-run and continue with checks
	if s.config.App.DryRun {
		logCtx["action"] = "dry_run_skipped"
		logCtx["reason"] = "dry-run mode is enabled"
		logger.WithContext(logCtx).Info()
		return nil
	}

	// Check if we have valid progress data
	if book.Progress.CurrentTime <= 0 {
		logCtx := map[string]interface{}{
			"current_time": book.Progress.CurrentTime,
			"reason":      "no progress data or zero progress",
		}
		logger.WithContext(logCtx).Warn()
		return nil
	}

	// Add progress percentage to log context
	progressPct := 0.0
	if book.Media.Duration > 0 {
		progressPct = (book.Progress.CurrentTime / book.Media.Duration) * 100
	}
	logCtx["progress_percent"] = fmt.Sprintf("%.1f%%", progressPct)
	log = logger.WithContext(logCtx)

	// Add more context before logging
	logCtx["action"] = "processing_in_progress"
	logCtx["progress_seconds"] = book.Progress.CurrentTime
	logCtx["started_at"] = book.Progress.StartedAt
	if book.Progress.IsFinished {
		logCtx["status"] = "finished"
		logCtx["finished_at"] = book.Progress.FinishedAt
	} else {
		logCtx["status"] = "in_progress"
	}
	log = logger.WithContext(logCtx)
	log.Info() // Processing in-progress book

	// Log the current progress from Audiobookshelf
	absProgress := book.Progress.CurrentTime
	logCtx["abs_progress"] = absProgress
	log.Debug().
		Float64("abs_progress", absProgress).
		Int64("started_at", book.Progress.StartedAt).
		Int64("finished_at", book.Progress.FinishedAt).
		Bool("is_finished", book.Progress.IsFinished).
		Msg("Current progress from Audiobookshelf")
	
	// Check if we need to update the progress
	needsUpdate := false
	minDiff := 60.0 // Minimum 1 minute difference to trigger an update
	
	if len(readStatuses) > 0 && readStatuses[0].ProgressSeconds != nil {
		// We have a valid read status with progress
		hcProgress := float64(*readStatuses[0].ProgressSeconds)
		progressDiff := math.Abs(absProgress - hcProgress)
		
		logCtx["hc_progress"] = hcProgress
		logCtx["progress_diff"] = progressDiff
		
		// Log detailed progress comparison
		log.Debug().
			Float64("abs_progress", absProgress).
			Float64("hc_progress", hcProgress).
			Float64("progress_diff", progressDiff).
			Float64("min_diff", minDiff).
			Msg("Comparing progress with Hardcover")
		
		// Only update if the difference is significant
		if progressDiff >= minDiff {
			needsUpdate = true
			reason := fmt.Sprintf("progress difference (%.2f) >= min threshold (%.2f)", progressDiff, minDiff)
			logCtx["update_reason"] = reason
			log.Info().
				Float64("abs_progress", absProgress).
				Float64("hc_progress", hcProgress).
				Float64("progress_diff", progressDiff).
				Msg(reason)
		} else {
			reason := fmt.Sprintf("progress difference (%.2f) < min threshold (%.2f)", progressDiff, minDiff)
			logCtx["update_reason"] = reason
			log.Info().
				Float64("abs_progress", absProgress).
				Float64("hc_progress", hcProgress).
				Float64("progress_diff", progressDiff).
				Msg(reason)
		}
	} else if hcBook == nil || hcBook.Progress == nil {
		// No existing progress in Hardcover, we need to update
		needsUpdate = true
		logCtx["update_reason"] = "no existing progress in Hardcover"
	} else {
		// Fallback to book progress if read status is not available
		progressDiff := math.Abs(book.Progress.CurrentTime - *hcBook.Progress)
		logCtx["hc_book_progress"] = *hcBook.Progress
		logCtx["progress_diff"] = progressDiff
		
		if progressDiff >= minDiff {
			needsUpdate = true
			logCtx["update_reason"] = fmt.Sprintf("book progress difference (%.2f) >= min threshold (%.2f)", progressDiff, minDiff)
		} else {
			logCtx["update_reason"] = fmt.Sprintf("book progress difference (%.2f) < min threshold (%.2f)", progressDiff, minDiff)
		}
	}
	
	if needsUpdate && len(readStatuses) > 0 {
		// Prepare the update object with required fields
		updateObj := map[string]interface{}{
			"progress_seconds": int64(book.Progress.CurrentTime),
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
			logger.WithContext(errCtx).Error()
			return fmt.Errorf("failed to update progress: %w", err)
		}

		// Log successful update
		logCtx["updated"] = true
		log = logger.WithContext(logCtx)
		log.Info() // Successfully updated reading progress in Hardcover
	} else if needsUpdate {
		log = logger.WithContext(logCtx)
		log.Info() // No read statuses available to update
	} else {
		log = logger.WithContext(logCtx)
		log.Info() // Skipped progress update - no significant change
	}

	return nil
}

// updateInProgressReads updates the in-progress reads
func (s *Service) updateInProgressReads(ctx context.Context, log *logger.Logger, reads []hardcover.UserBookRead, now time.Time) error {
	// Initialize logger with context if not provided
	if log == nil {
		log = logger.WithContext(map[string]interface{}{
			"function": "updateInProgressReads",
		})
	}

	// Add context about the operation
	logCtx := map[string]interface{}{
		"function":  "updateInProgressReads",
		"read_count": len(reads),
		"dry_run":   s.config.App.DryRun,
	}

	// Create a new logger with the combined context
	log = logger.WithContext(logCtx)

	if len(reads) == 0 {
		log.Info()
		return nil
	}

	// Log the update operation with context
	log.Info()

	// In dry-run mode, log what would be updated and return
	if s.config.App.DryRun {
		log.Info()
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

		readLog := logger.WithContext(readLogCtx)

		// Log the read being processed
		readLog.Info()

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
			logger.WithContext(errCtx).Error()
			continue
		}

		// Log successful update with updated status
		readLogCtx["updated"] = updated
		readLog = logger.WithContext(readLogCtx)
		readLog.Info()
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
		log = logger.WithContext(logCtx)
	} else {
		// Create a new logger with combined context
		log = logger.WithContext(logCtx)
	}

	// Log the status update
	log.Info()

	// In dry-run mode, log what would be done and return
	if dryRun {
		log.Info()
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
		logger.WithContext(errCtx).Error()
		return fmt.Errorf("failed to update book status: %w", err)
	}

	log.Info()
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
		s.log.Warn().
			Err(err).
			Str("edition_id", editionIDStr).
			Msg("Failed to get or create user book ID")
	} else {
		hcBook.UserBookID = strconv.FormatInt(userBookID, 10)
	}

	s.log.Info().
		Str("book_id", hcBook.ID).
		Str("edition_id", hcBook.EditionID).
		Str("user_book_id", hcBook.UserBookID).
		Msg("Processed found book")

	return hcBook, nil
}

// findBookInHardcoverByTitleAuthor searches for a book in Hardcover by title and author
// This should only be used for mismatch handling when a book can't be found by ASIN/ISBN
func (s *Service) findBookInHardcoverByTitleAuthor(ctx context.Context, book models.AudiobookshelfBook) (*models.HardcoverBook, error) {
	s.log.Info().
		Str("search_method", "title_author").
		Str("title", book.Media.Metadata.Title).
		Str("author", book.Media.Metadata.AuthorName).
		Msg("Searching for book by title and author for mismatch resolution")

	// Build the GraphQL query to find the book by title and author
	query := `
	query BookByTitleAuthor($title: String!, $author: String!) {
	  books(where: { 
	    _and: [
	      { title: { _eq: $title } },
	      { contributions: { author: { name: { _eq: $author } } } },
	      { editions: { reading_format: { id: { _eq: 2 } } } }
	    ]
	  }, limit: 1) {
	    id
	    title
	    book_status_id
	    canonical_id
	    editions(where: { reading_format: { id: { _eq: 2 } } }, limit: 1) {
	      id
	      asin
	      isbn_13
	      isbn_10
	      reading_format_id
	      audio_seconds
	    }
	  }
	}`

	// Define the response structure
	var result struct {
		Books []struct {
			ID           string `json:"id"`
			Title        string `json:"title"`
			BookStatusID int    `json:"book_status_id"`
			CanonicalID  *int   `json:"canonical_id"`
			Editions     []struct {
				ID              string `json:"id"`
				ASIN            string `json:"asin"`
				ISBN13          string `json:"isbn_13"`
				ISBN10          string `json:"isbn_10"`
				ReadingFormatID *int   `json:"reading_format_id"`
				AudioSeconds    *int   `json:"audio_seconds"`
			} `json:"editions"`
		} `json:"books"`
	}

	// Execute the GraphQL query
	err := s.hardcover.GraphQLQuery(ctx, query, map[string]interface{}{
		"title":  book.Media.Metadata.Title,
		"author": book.Media.Metadata.AuthorName,
	}, &result)

	if err != nil {
		s.log.Warn().
			Err(err).
			Str("title", book.Media.Metadata.Title).
			Str("author", book.Media.Metadata.AuthorName).
			Msg("Search by title and author failed for mismatch resolution")
		return nil, fmt.Errorf("title/author search failed: %w", err)
	}

	if len(result.Books) == 0 {
		s.log.Info().
			Str("title", book.Media.Metadata.Title).
			Str("author", book.Media.Metadata.AuthorName).
			Msg("No books found by title and author for mismatch resolution")
		return nil, fmt.Errorf("no books found by title and author")
	}

	bookData := result.Books[0]

	// Skip if no editions found
	if len(bookData.Editions) == 0 {
		s.log.Warn().
			Str("book_id", bookData.ID).
			Msg("No editions found for book in title/author search, skipping")
		return nil, fmt.Errorf("no audio editions found for book")
	}

	edition := bookData.Editions[0]

	hcBook := &models.HardcoverBook{
		ID:           bookData.ID,
		Title:        book.Media.Metadata.Title,
		EditionID:    edition.ID,
		BookStatusID: bookData.BookStatusID,
		CanonicalID:  bookData.CanonicalID,
	}

	// Get or create user book ID for this edition
	editionIDStr := edition.ID
	progress := 0.0
	isFinished := book.Progress.IsFinished
	finishedAt := book.Progress.FinishedAt
	if book.Media.Duration > 0 {
		progress = book.Progress.CurrentTime / book.Media.Duration
	}
	status := s.determineBookStatus(progress, isFinished, finishedAt)
	userBookID, err := s.findOrCreateUserBookID(ctx, editionIDStr, status)
	if err != nil {
		s.log.Warn().
			Err(err).
			Str("edition_id", editionIDStr).
			Msg("Failed to get or create user book ID for mismatch resolution")
	} else {
		hcBook.UserBookID = strconv.FormatInt(userBookID, 10)
	}

	// Set optional fields if they exist
	if edition.ASIN != "" {
		hcBook.EditionASIN = edition.ASIN
	}
	if edition.ISBN13 != "" {
		hcBook.EditionISBN13 = edition.ISBN13
	}
	if edition.ISBN10 != "" {
		hcBook.EditionISBN10 = edition.ISBN10
	}

	s.log.Info().
		Str("search_method", "title_author").
		Str("hardcover_id", bookData.ID).
		Str("edition_id", edition.ID).
		Str("user_book_id", hcBook.UserBookID).
		Msg("Successfully found book by title and author for mismatch resolution")

	return hcBook, nil
}

// findBookInHardcover finds a book in Hardcover by various methods
// It first tries ASIN, then ISBN-13, then ISBN-10
// Title/author search is only used for mismatches and should be called separately
func (s *Service) findBookInHardcover(ctx context.Context, book models.AudiobookshelfBook) (*models.HardcoverBook, error) {
	// 1. First try to find by ASIN if available
	if book.Media.Metadata.ASIN != "" {
		s.log.Info().
			Str("search_method", "asin").
			Str("asin", book.Media.Metadata.ASIN).
			Msg("Searching for book by ASIN")

		hcBook, err := s.hardcover.SearchBookByASIN(ctx, book.Media.Metadata.ASIN)
		if err != nil {
			s.log.Warn().
				Err(err).
				Str("search_method", "asin").
				Str("asin", book.Media.Metadata.ASIN).
				Msg("Search by ASIN failed, will try other methods")
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
				s.log.Warn().
					Err(err).
					Str("edition_id", editionIDStr).
					Msg("Failed to get or create user book ID")
			} else {
				hcBook.UserBookID = strconv.FormatInt(userBookID, 10)
			}

			s.log.Info().
				Str("search_method", "asin").
				Str("book_id", hcBook.ID).
				Str("edition_id", hcBook.EditionID).
				Str("user_book_id", hcBook.UserBookID).
				Msg("Found book by ASIN")

			return hcBook, nil
		}
	}

	// 2. Try to find by ISBN if available
	if book.Media.Metadata.ISBN != "" {
		s.log.Info().
			Str("search_method", "isbn").
			Str("isbn", book.Media.Metadata.ISBN).
			Msg("Searching for book by ISBN")

		// Try to find by ISBN-13 first
	hcBook, err := s.hardcover.SearchBookByISBN13(ctx, book.Media.Metadata.ISBN)
	if err != nil {
		s.log.Warn().
			Err(err).
			Str("search_method", "isbn13").
			Str("isbn", book.Media.Metadata.ISBN).
			Msg("Search by ISBN-13 failed, will try ISBN-10")
	} else if hcBook != nil {
		return s.processFoundBook(ctx, hcBook, book)
	}

	// If ISBN-13 search failed or returned no results, try ISBN-10
	hcBook, err = s.hardcover.SearchBookByISBN10(ctx, book.Media.Metadata.ISBN)
	if err != nil {
		s.log.Warn().
			Err(err).
			Str("search_method", "isbn10").
			Str("isbn", book.Media.Metadata.ISBN).
			Msg("Search by ISBN-10 failed")
	} else if hcBook != nil {
		return s.processFoundBook(ctx, hcBook, book)
	}

	errMsg := "failed to find book by ISBN"
	s.log.Error().
		Str("book_id", book.ID).
		Str("title", book.Media.Metadata.Title).
		Str("author", book.Media.Metadata.AuthorName).
		Str("isbn", book.Media.Metadata.ISBN).
		Str("asin", book.Media.Metadata.ASIN).
		Msg(errMsg)

	return nil, fmt.Errorf(errMsg)
}

	// If we get here, we couldn't find the book by any method
	s.log.Warn().
		Str("book_id", book.ID).
		Str("title", book.Media.Metadata.Title).
		Str("author", book.Media.Metadata.AuthorName).
		Str("isbn", book.Media.Metadata.ISBN).
		Str("asin", book.Media.Metadata.ASIN).
		Msg("Book not found in Hardcover by standard search methods")

	// Return a specific error that indicates this is a potential mismatch
	return nil, fmt.Errorf("book not found by ASIN/ISBN, potential mismatch")
}
