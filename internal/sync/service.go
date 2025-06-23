package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
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

	// Get the current user's ID to use in the query
	userID, err := s.hardcover.GetCurrentUserID(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get current user ID: %w", err)
	}

	s.log.Debug().
		Int("book_id", bookIDInt).
		Int("user_id", userID).
		Msg("Checking if book is marked as owned in Hardcover")

	// Query to check if the book is in the user's "Owned" list
	query := `
	query CheckBookOwnership($userId: Int!, $bookId: Int!) {
	  lists(
		where: {
		  user_id: { _eq: $userId }
		  name: { _eq: "Owned" }
		  list_books: { book_id: { _eq: $bookId } }
		}
	  ) {
		id
		name
		list_books(where: { book_id: { _eq: $bookId } }) {
		  id
		  book_id
		  edition_id
		}
	  }
	}`

	// Define the response structure to match the actual API response
	type ListBook struct {
		ID        int  `json:"id"`
		BookID    int  `json:"book_id"`
		EditionID *int `json:"edition_id"`
	}

	type List struct {
		ID        int         `json:"id"`
		Name      string      `json:"name"`
		ListBooks []*ListBook `json:"list_books"`
	}

	// The response is a direct array of lists (no data wrapper)
	type ListResponse []struct {
		ID        int `json:"id"`
		Name      string `json:"name"`
		ListBooks []struct {
			ID        int `json:"id"`
			BookID    int `json:"book_id"`
			EditionID int `json:"edition_id"`
		} `json:"list_books"`
	}

	// Execute the GraphQL query
	var response ListResponse
	err = s.hardcover.GraphQLQuery(ctx, query, map[string]interface{}{
		"userId": userID,
		"bookId": bookIDInt,
	}, &response)

	if err != nil {
		s.log.Error().
			Err(err).
			Int("book_id", bookIDInt).
			Int("user_id", userID).
			Msg("GraphQL query failed when checking book ownership")
		return false, fmt.Errorf("failed to check book ownership: %w", err)
	}

	// Debug log the response for troubleshooting
	s.log.Debug().
		Int("book_id", bookIDInt).
		Int("user_id", userID).
		Int("lists_count", len(response)).
		Msg("Received response from Hardcover API")

	// If we have a list with list_books, the book is owned
	if len(response) > 0 && len(response[0].ListBooks) > 0 {
		s.log.Debug().
			Int("book_id", bookIDInt).
			Int("user_id", userID).
			Int("list_id", response[0].ID).
			Int("list_books_count", len(response[0].ListBooks)).
			Msg("Book is marked as owned in user's 'Owned' list")
		return true, nil
	}

	s.log.Debug().
		Int("book_id", bookIDInt).
		Int("user_id", userID).
		Msg("Book is not marked as owned in user's 'Owned' list")

	return false, nil
}

// markBookAsOwned marks a book as owned in Hardcover using the edition_owned mutation
// This works with edition IDs, not book IDs
func (s *Service) markBookAsOwned(ctx context.Context, bookID, editionID string) error {
	if editionID == "" {
		return fmt.Errorf("edition ID is required for marking book as owned")
	}

	// Convert IDs to integers
	editionIDInt, err := strconv.Atoi(editionID)
	if err != nil {
		return fmt.Errorf("invalid edition ID format: %w", err)
	}

	bookIDInt, err := strconv.Atoi(bookID)
	if err != nil {
		return fmt.Errorf("invalid book ID format: %w", err)
	}

	mutation := `
	mutation EditionOwned($id: Int!) {
	  ownership: edition_owned(id: $id) {
		id
		list_book {
		  id
		  book_id
		  edition_id
		}
	  }
	}`

	variables := map[string]interface{}{
		"id": editionIDInt,
	}

	// Log the ownership marking attempt
	s.log.Debug().
		Int("book_id", bookIDInt).
		Int("edition_id", editionIDInt).
		Msg("Attempting to mark book as owned in Hardcover")

	var result struct {
		Data struct {
			Ownership struct {
				ID       *int `json:"id"`
				ListBook *struct {
					ID        int `json:"id"`
					BookID    int `json:"book_id"`
					EditionID int `json:"edition_id"`
				} `json:"list_book"`
			} `json:"ownership"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	err = s.hardcover.GraphQLMutation(ctx, mutation, variables, &result)
	if err != nil {
		return fmt.Errorf("failed to mark book as owned: %w", err)
	}

	if len(result.Errors) > 0 {
		errMsgs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		return fmt.Errorf("graphql errors: %v", strings.Join(errMsgs, "; "))
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

	case "READING":
		// For in-progress books, update the reading progress
		bookLog.Info().
			Str("status", status).
			Float64("progress", progress).
			Msg("Processing in-progress book")
		if err := s.handleInProgressBook(ctx, userBookID, editionID, book); err != nil {
			bookLog.Error().Err(err).Msg("Failed to handle in-progress book")
			return fmt.Errorf("error handling in-progress book: %w", err)
		}

	case "WANT_TO_READ":
		// Only update status to WANT_TO_READ if explicitly configured
		if s.config.App.SyncWantToRead {
			bookLog.Info().
				Str("status", status).
				Msg("Updating book status to WANT_TO_READ")
			if err := s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
				ID:     userBookID,
				Status: "WANT_TO_READ",
			}); err != nil {
				bookLog.Error().Err(err).Msg("Failed to update book status to WANT_TO_READ")
				return fmt.Errorf("error updating status to WANT_TO_READ: %w", err)
			}
		} else {
			bookLog.Debug().
				Str("status", status).
				Msg("Skipping WANT_TO_READ update as SyncWantToRead is disabled")
		}

	default:
		bookLog.Debug().
			Str("status", status).
			Msg("No status update needed for book")
	}

	// Update ownership status if configured
	if s.config.App.SyncOwned && hcBook != nil && hcBook.ID != "" {
		// Check if the book is already owned
		isOwned, err := s.isBookOwned(ctx, hcBook.ID)
		if err != nil {
			bookLog.Error().Err(err).Msg("Failed to check book ownership")
		} else if !isOwned {
			// If not owned, mark it as owned
			bookLog.Info().Str("book_id", hcBook.ID).Msg("Marking book as owned in Hardcover")
			err = s.markBookAsOwned(ctx, hcBook.ID, hcBook.EditionID)
			if err != nil {
				bookLog.Error().Err(err).Msg("Failed to mark book as owned")
			} else {
				bookLog.Info().Msg("Successfully marked book as owned")
			}
		} else {
			bookLog.Debug().Msg("Book is already marked as owned")
		}
	}

	// Note: Cover images are not synced as they are handled during edition creation

	return nil
}

// determineBookStatus determines the book status based on progress, isFinished flag, and FinishedAt timestamp
func (s *Service) determineBookStatus(progress float64, isFinished bool, finishedAt int64) string {
	// Log the decision-making process for debugging
	logCtx := s.log.With().
		Float64("progress", progress).
		Bool("is_finished", isFinished).
		Int64("finished_at", finishedAt).
		Logger()

	// Check if the book is marked as finished in Audiobookshelf
	if isFinished {
		logCtx.Debug().Msg("Book is marked as finished in Audiobookshelf")
	}

	// Check if progress is 100% or more
	if progress >= 1.0 {
		logCtx.Debug().
			Float64("progress", progress).
			Msg("Book has 100% or more progress")
	}

	// Check if there's a valid finishedAt timestamp
	if finishedAt > 0 {
		logCtx.Debug().
			Int64("finished_at", finishedAt).
			Msg("Book has a valid finishedAt timestamp")
	}

	switch {
	// Mark as FINISHED if any of these conditions are true:
	// 1. Progress is 100% or more
	// 2. Book is marked as finished in Audiobookshelf
	// 3. There's a valid finishedAt timestamp
	case progress >= 1.0 || isFinished || finishedAt > 0:
		logCtx.Debug().
			Str("reason", fmt.Sprintf("Book meets finished criteria (progress: %.2f%%, isFinished: %v, finishedAt: %d)", 
				progress*100, isFinished, finishedAt)).
			Msg("Marking as FINISHED")
		return "FINISHED"

	// A book is in progress if there's any progress but less than 100%
	case progress > 0 && progress < 1.0:
		logCtx.Debug().
			Str("reason", fmt.Sprintf("Book has progress (%.2f%%) but is not finished", progress*100)).
			Msg("Marking as READING")
		return "READING"

	// A book is "want to read" only if explicitly configured and no progress
	case s.config.App.SyncWantToRead && progress == 0:
		logCtx.Debug().
			Str("reason", "Book has no progress and SyncWantToRead is enabled").
			Msg("Marking as WANT_TO_READ")
		return "WANT_TO_READ"

	// Default to empty status if none of the above conditions are met
	default:
		logCtx.Debug().
			Str("reason", "No status change needed").
			Msg("No status update")
		return ""
	}
}

// handleFinishedBook handles the special case of a finished book, including re-reads
// handleFinishedBook handles the special case of a finished book, including re-reads
// stringValue safely dereferences a string pointer, returning an empty string if the pointer is nil
// stringValue safely dereferences a string pointer, returning an empty string if the pointer is nil
func stringValue(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// int64Value safely dereferences an int64 pointer, returning 0 if the pointer is nil
func int64Value(i *int64) int64 {
	if i != nil {
		return *i
	}
	return 0
}

func (s *Service) handleFinishedBook(ctx context.Context, userBookID int64, editionID string, book models.AudiobookshelfBook) error {
	// Create a logger with comprehensive context
	log := s.log.With().
		Int64("user_book_id", userBookID).
		Str("edition_id", editionID).
		Str("book_id", book.ID).
		Str("title", book.Media.Metadata.Title).
		Float64("progress", book.Progress.CurrentTime).
		Bool("is_finished", book.Progress.IsFinished).
		Int64("finished_at", book.Progress.FinishedAt).
		Str("book_type", fmt.Sprintf("%T", book)).
		Bool("dry_run", s.config.App.DryRun).
		Logger()

	log.Info().Msg("Handling finished book")

	// In dry-run mode, log that we're in dry-run and continue with checks
	if s.config.App.DryRun {
		log.Info().Msg("DRY RUN: Checking book status (no changes will be made)")
	}

	// If we don't have a valid user book ID, we can't update progress
	if userBookID == 0 {
		err := fmt.Errorf("missing user book ID")
		log.Warn().Err(err).Msg("Cannot update finished status")
		return err
	}

	// Get the current book status from Hardcover
	userBook, err := s.hardcover.GetUserBook(ctx, userBookID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get current book status from Hardcover")
		return fmt.Errorf("failed to get current book status: %w", err)
	}

	// Log the current book status for debugging
	if userBook != nil {
		logCtx := log.With().
			Str("current_status", userBook.Status)
		
		// Only log progress if it's not nil
		if userBook.Progress != nil {
			logCtx = logCtx.Float64("current_progress", *userBook.Progress)
		}
		
		log = logCtx.Logger()
	}

	// Get all existing reads for this book to check for duplicates
	existingReads, err := s.hardcover.GetUserBookReads(ctx, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to get existing reads")
		return fmt.Errorf("failed to get existing reads: %w", err)
	}

	// Get the current time in RFC3339 format
	now := time.Now()
	nowStr := now.Format(time.RFC3339)
	today := now.Format("2006-01-02")

	// Initialize variables for tracking reads
	hasRecentFinishedRead := false
	existingFinishedReads := make([]hardcover.UserBookRead, 0)
	inProgressReads := make([]hardcover.UserBookRead, 0)
	lastFinishedRead := (*hardcover.UserBookRead)(nil)

	// Analyze existing reads and categorize them
	for i := range existingReads {
		read := &existingReads[i]
		if read.FinishedAt != nil {
			existingFinishedReads = append(existingFinishedReads, *read)

			// Parse the finished at time
			finishedTime, err := parseDate(*read.FinishedAt)
			if err != nil {
				log.Warn().Err(err).Str("finished_at", *read.FinishedAt).Msg("Failed to parse finished_at time")
				continue
			}

			// Check if this is the most recent finished read
			if lastFinishedRead == nil || finishedTime.After(func() time.Time {
				if lastFinishedRead.FinishedAt == nil {
					return time.Time{}
				}
				t, _ := parseDate(*lastFinishedRead.FinishedAt)
				return t
			}()) {
				lastFinishedRead = read
			}

			// Check if there's a finished read from today
			finishedDate := finishedTime.Format("2006-01-02")
			if finishedDate == today {
				hasRecentFinishedRead = true
				log.Info().
					Int64("read_id", read.ID).
					Str("finished_date", finishedDate).
					Msg("Found existing finished read from today")
			}
		} else {
			// Track in-progress reads
			inProgressReads = append(inProgressReads, *read)
		}
	}

	// Log the current state
	logCtx := log.With().
		Int("total_reads", len(existingReads)).
		Int("finished_reads", len(existingFinishedReads)).
		Int("in_progress_reads", len(inProgressReads)).
		Bool("has_recent_finished_read", hasRecentFinishedRead)
	
	// Only add last_finished_read if we have one
	if lastFinishedRead != nil && lastFinishedRead.FinishedAt != nil {
		logCtx = logCtx.Str("last_finished_read", *lastFinishedRead.FinishedAt)
	}
	
	log = logCtx.Logger()

	// If the book is already marked as FINISHED in Hardcover and we have a recent finished read, no need to proceed
	if userBook != nil && userBook.Status == "FINISHED" && hasRecentFinishedRead {
		log.Info().
			Msg("Book is already marked as FINISHED in Hardcover with a recent finished read - no update needed")
		return nil
	}

	// Check for existing finished reads to determine if this is a new reading session
	// We consider it a new reading session if there's no existing finished read for today
	// or if the progress has significantly changed since the last finished read

	// First, check for a finished read from today
	hasFinishedReadToday := false
	var mostRecentFinishedRead *hardcover.UserBookRead
	var mostRecentFinishTime time.Time

	for i := range existingFinishedReads {
		read := &existingFinishedReads[i]
		if read.FinishedAt == nil {
			continue
		}

		// Parse the finished time
		finishedTime, err := parseDate(*read.FinishedAt)
		if err != nil {
			log.Warn().
				Err(err).
				Int64("read_id", read.ID).
				Str("finished_at", *read.FinishedAt).
				Msg("Failed to parse finished_at time for existing read")
			continue
		}

		// Track the most recent finished read
		if mostRecentFinishedRead == nil || finishedTime.After(mostRecentFinishTime) {
			mostRecentFinishedRead = read
			mostRecentFinishTime = finishedTime
		}

		// Check if there's a finished read from today
		if finishedTime.YearDay() == now.YearDay() && finishedTime.Year() == now.Year() {
			hasFinishedReadToday = true
			log.Info().
				Int64("read_id", read.ID).
				Time("finished_time", finishedTime).
				Msg("Found existing finished read from today - no new read needed")
		}
	}

	// If we have a finished read from today, don't create another one
	if hasFinishedReadToday {
		log.Info().Msg("Book already has a finished read from today - no update needed")

		// In dry-run mode, just log what we would do
		if s.config.App.DryRun {
			log.Info().Msg("DRY RUN: Would update book status to FINISHED if needed")
			return nil
		}

		// Ensure the book status is set to FINISHED in case it's not
		if userBook == nil || userBook.Status != "FINISHED" {
			log.Info().Msg("Updating book status to FINISHED to match existing finished reads")
			if err := s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
				ID:     userBookID,
				Status: "FINISHED",
			}); err != nil {
				log.Error().Err(err).Msg("Failed to update book status to FINISHED")
				return fmt.Errorf("failed to update book status to FINISHED: %w", err)
			}
		}
		
		return nil
	}

	// If we have a most recent finished read, log it for debugging
	if mostRecentFinishedRead != nil && mostRecentFinishedRead.FinishedAt != nil {
		log.Info().
			Int64("most_recent_read_id", mostRecentFinishedRead.ID).
			Str("most_recent_finish", *mostRecentFinishedRead.FinishedAt).
			Msg("Found most recent finished read")
	}

	// If the book was previously finished, check if this is a re-read or just a duplicate sync
	if lastFinishedRead != nil {
		log.Info().
			Str("last_finished_at", *lastFinishedRead.FinishedAt).
			Msg("Book was previously marked as finished")

		// If we have in-progress reads, mark the most recent one as finished
		if len(inProgressReads) > 0 {
			// Sort by started_at in descending order to get the most recent one
			sort.Slice(inProgressReads, func(i, j int) bool {
				iTime, _ := parseDate(*inProgressReads[i].StartedAt)
				jTime, _ := parseDate(*inProgressReads[j].StartedAt)
				return iTime.After(jTime)
			})

			existingRead := inProgressReads[0]
			finishedAt := nowStr

			// In dry-run mode, just log what we would do
			if s.config.App.DryRun {
				log.Info().
					Int64("read_id", existingRead.ID).
					Str("started_at", *existingRead.StartedAt).
					Str("finished_at", finishedAt).
					Msg("DRY RUN: Would mark existing in-progress read as finished")
			} else {
				// Update the existing read entry to mark as finished
				_, err = s.hardcover.UpdateUserBookRead(ctx, hardcover.UpdateUserBookReadInput{
					ID: existingRead.ID,
					Object: map[string]interface{}{
						"progress_seconds": existingRead.ProgressSeconds,
						"started_at":      existingRead.StartedAt,
						"finished_at":     finishedAt,
					},
				})
				if err != nil {
					log.Error().
						Err(err).
						Int64("read_id", existingRead.ID).
						Msg("Failed to update existing read entry")
					return fmt.Errorf("failed to update existing read entry: %w", err)
				}
				log.Info().
					Int64("read_id", existingRead.ID).
					Msg("Successfully marked existing read as finished")
			}
			return nil
		}

		log.Info().Msg("Book is already marked as finished with no in-progress reads - no action needed")
		return nil
	}
	
	// If we get here, we need to find or create a new read entry
	var mostRecentRead *hardcover.UserBookRead
	if len(inProgressReads) > 0 {
		// Sort by started_at in descending order to get the most recent one
		sort.Slice(inProgressReads, func(i, j int) bool {
			iTime, _ := parseDate(*inProgressReads[i].StartedAt)
			jTime, _ := parseDate(*inProgressReads[j].StartedAt)
			return iTime.After(jTime)
		})
		mostRecentRead = &inProgressReads[0]

		// Use the existing read's started_at or default to now - 1 hour
		startedAt := mostRecentRead.StartedAt
		if startedAt == nil || *startedAt == "" {
			hourAgo := now.Add(-1 * time.Hour).Format(time.RFC3339)
			startedAt = &hourAgo
		}

		// Log the update
		log.Info().
			Int64("read_id", mostRecentRead.ID).
			Str("started_at", *startedAt).
			Msg("Marking existing in-progress read as finished")

		// In dry-run mode, just log what we would do
		if s.config.App.DryRun {
			log.Info().
				Int64("read_id", mostRecentRead.ID).
				Str("started_at", *startedAt).
				Str("finished_at", nowStr).
				Msg("DRY RUN: Would mark existing read as finished")
		} else {
			// Update the existing read entry to mark as finished
			_, err = s.hardcover.UpdateUserBookRead(ctx, hardcover.UpdateUserBookReadInput{
				ID: mostRecentRead.ID,
				Object: map[string]interface{}{
					"progress_seconds": mostRecentRead.ProgressSeconds,
					"started_at":      startedAt,
					"finished_at":     nowStr,
				},
			})
			if err != nil {
				log.Error().
					Err(err).
					Int64("read_id", mostRecentRead.ID).
					Msg("Failed to update read entry")
				return fmt.Errorf("failed to update read entry: %w", err)
			}
			log.Info().
				Int64("read_id", mostRecentRead.ID).
				Msg("Successfully marked existing read as finished")
		}
	} else {
		// No existing in-progress read found, create a new finished entry
		// Use the book's progress data to determine start time if available
		var startedAtStr string
		if book.Progress.StartedAt > 0 {
			startedAt := time.Unix(book.Progress.StartedAt/1000, 0)
			startedAtStr = startedAt.Format(time.RFC3339)
		} else {
			// Default to 1 hour ago if no start time is available
			startedAtStr = now.Add(-1 * time.Hour).Format(time.RFC3339)
		}

		finishedAt := nowStr
		log.Info().
			Str("started_at", startedAtStr).
			Msg("Creating new finished read entry")

		if s.config.App.DryRun {
			log.Info().
				Int64("user_book_id", userBookID).
				Str("started_at", startedAtStr).
				Str("finished_at", finishedAt).
				Msg("DRY RUN: Would create new finished read entry")
		} else {
			editionIDInt, _ := strconv.ParseInt(editionID, 10, 64)
			userBookIDInt64 := userBookID // Convert to int64 if needed
			datesRead := hardcover.DatesReadInput{
				EditionID:  &editionIDInt,
				StartedAt:  &startedAtStr,
				FinishedAt: &finishedAt,
			}
			_, err = s.hardcover.InsertUserBookRead(ctx, hardcover.InsertUserBookReadInput{
				UserBookID: userBookIDInt64,
				DatesRead:  datesRead,
			})
			if err != nil {
				log.Error().
					Err(err).
					Str("started_at", startedAtStr).
					Str("finished_at", finishedAt).
					Msg("Failed to create new finished read entry")
				return fmt.Errorf("failed to create new finished read entry: %w", err)
			}
			log.Info().Msg("Successfully created new finished read entry")
		}
	}

	// Update the book status to FINISHED
	if s.config.App.DryRun {
		log.Info().Msg("DRY RUN: Would update book status to FINISHED")
	} else {
		log.Info().Msg("Updating book status to FINISHED")
		if err := s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
			ID:     userBookID,
			Status: "FINISHED",
		}); err != nil {
			log.Error().
				Err(err).
				Int64("user_book_id", userBookID).
				Msg("Failed to update book status to FINISHED")
			return fmt.Errorf("failed to update book status to FINISHED: %w", err)
		}

		log.Info().
			Int64("user_book_id", userBookID).
			Str("status", "FINISHED").
			Msg("Successfully updated book status")
	}

	return nil
}

// hasExistingFinishedRead checks if there are any existing finished reads for a user book
func (s *Service) hasExistingFinishedRead(ctx context.Context, userBookID int64) (bool, error) {
	// Use the more reliable CheckExistingFinishedRead function which checks for progress >= 0.99
	result, err := s.hardcover.CheckExistingFinishedRead(ctx, hardcover.CheckExistingFinishedReadInput{
		UserBookID: int(userBookID), // Convert to int since UserBookID is int in the input struct
	})
	if err != nil {
		s.log.Error().
			Err(err).
			Int64("user_book_id", userBookID).
			Msg("Failed to check for existing finished reads")
		return false, fmt.Errorf("failed to check for existing finished reads: %w", err)
	}

	// The result already has HasFinishedRead which is set based on the progress check
	hasFinishedRead := result.HasFinishedRead

	// Log the result for debugging
	s.log.Debug().
		Int64("user_book_id", userBookID).
		Bool("has_finished_read", hasFinishedRead).
		Str("last_finished_at", stringValue(result.LastFinishedAt)).
		Msg("Checked for existing finished reads")

	return hasFinishedRead, nil
}

func (s *Service) handleInProgressBook(ctx context.Context, userBookID int64, editionID string, book models.AudiobookshelfBook) error {
	// Enhanced logging context with comprehensive book details
	log := s.log.With().
		Int64("user_book_id", userBookID).
		Str("edition_id", editionID).
		Float64("progress_percent", book.Progress.CurrentTime).
		Float64("duration", book.Media.Duration).
		Str("book_id", book.ID).
		Str("title", book.Media.Metadata.Title).
		Logger()

	// Validate inputs
	if userBookID == 0 {
		err := fmt.Errorf("missing user book ID")
		log.Error().Err(err).Msg("Cannot update reading progress")
		return err
	}

	// Calculate progress percentage (0-1 range)
	progress := 0.0
	if book.Media.Duration > 0 {
		progress = book.Progress.CurrentTime / book.Media.Duration
	}

	// Calculate progress in seconds
	var progressSeconds int
	if book.Progress.CurrentTime > 0 {
		// Use CurrentTime directly as it's already in seconds
		progressSeconds = int(math.Round(book.Progress.CurrentTime))
	} else if book.Media.Duration > 0 && progress > 0 {
		// Fall back to progress percentage * duration if current time not available
		progressSeconds = int(math.Round(progress * book.Media.Duration))
	} else {
		// Fallback: use progress percentage * reasonable audiobook duration (10 hours)
		fallbackDuration := 36000.0 // 10 hours in seconds
		progressSeconds = int(math.Round(progress * fallbackDuration))
	}

	// Ensure we have at least 1 second of progress if there is any progress
	if progressSeconds < 1 && progress > 0 {
		progressSeconds = 1
	}

	log.Debug().
		Float64("progress", progress).
		Float64("current_time", book.Progress.CurrentTime).
		Float64("duration", book.Media.Duration).
		Int("progress_seconds", progressSeconds).
		Msg("Calculated progress")

	// Parse edition ID if available
	editionIDInt := int64(0)
	if editionID != "" {
		var err error
		editionIDInt, err = strconv.ParseInt(editionID, 10, 64)
		if err != nil {
			log.Error().Err(err).Str("edition_id", editionID).Msg("Failed to parse edition ID")
			// Continue with 0 edition ID if parsing fails
		}
	}

	now := time.Now()
	today := now.Format("2006-01-02")

	// Get all existing read entries for this book
	existingReads, err := s.hardcover.GetUserBookReads(ctx, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch existing read entries")
		return fmt.Errorf("failed to fetch existing reads: %w", err)
	}

	// Find the most appropriate existing read to update
	var existingRead *hardcover.UserBookRead
	updateExisting := false

	// First, try to find an existing read entry from today
	for i := range existingReads {
		read := &existingReads[i]
		if read.StartedAt != nil {
			if startedAt, err := parseDate(*read.StartedAt); err == nil {
				if startedAt.Format("2006-01-02") == today {
					existingRead = read
					updateExisting = true
					break
				}
			}
		}
	}

	// If no read from today, use the most recent in-progress read
	if !updateExisting && len(existingReads) > 0 {
		for i := range existingReads {
			read := &existingReads[i]
			if read.FinishedAt == nil || *read.FinishedAt == "" {
				existingRead = read
				updateExisting = true
				break
			}
		}

		// If still no suitable read found, use the most recent one
		if !updateExisting {
			existingRead = &existingReads[0]
		}
	}

	// Check if we should update based on progress
	if updateExisting && existingRead != nil && existingRead.ProgressSeconds != nil {
		existingProgress := *existingRead.ProgressSeconds
		
		// Calculate progress threshold (30 seconds or 1% of duration, whichever is larger)
		progressThreshold := 30 // Minimum threshold in seconds
		if book.Media.Duration > 0 {
			// Use 1% of total duration as threshold, with reasonable bounds
			percentageThreshold := int(book.Media.Duration * 0.01)
			if percentageThreshold < 30 {
				progressThreshold = 30 // Minimum 30 seconds
			} else if percentageThreshold > 300 {
				progressThreshold = 300 // Maximum 5 minutes
			} else {
				progressThreshold = percentageThreshold
			}
		}

		// Calculate progress difference and check if it exceeds the threshold
		progressDiff := int(math.Abs(float64(progressSeconds - existingProgress)))
		if progressDiff < progressThreshold {
			log.Debug().
				Int("existing_progress", existingProgress).
				Int("new_progress", progressSeconds).
				Int("progress_diff", progressDiff).
				Int("progress_threshold", progressThreshold).
				Msg("Progress change below threshold, skipping update")
			return nil
		}

		log.Debug().
			Int("existing_progress", existingProgress).
			Int("new_progress", progressSeconds).
			Int("progress_diff", progressDiff).
			Int("progress_threshold", progressThreshold).
			Msg("Significant progress change detected - updating progress")
	}

	// Update or create read entry based on whether we're updating existing or creating new
	if updateExisting && existingRead != nil {
		// Update existing read entry with all preserved fields
		updateObject := map[string]interface{}{
			"progress_seconds": progressSeconds,
		}

		// Preserve existing fields to prevent data loss
		if existingRead.EditionID != nil {
			updateObject["edition_id"] = *existingRead.EditionID
		} else if editionIDInt > 0 {
			updateObject["edition_id"] = editionIDInt
		}

		// Set default reading format to audiobook (2)
		updateObject["reading_format_id"] = 2

		// Preserve the original started_at date if available
		if existingRead.StartedAt != nil && *existingRead.StartedAt != "" {
			updateObject["started_at"] = *existingRead.StartedAt
		} else {
			updateObject["started_at"] = now
		}

		log.Debug().
			Int64("read_id", existingRead.ID).
			Interface("update_object", updateObject).
			Msg("Updating existing read entry")

		// Update the existing read entry
		_, err = s.hardcover.UpdateUserBookRead(ctx, hardcover.UpdateUserBookReadInput{
			ID:     existingRead.ID,
			Object: updateObject,
		})
		if err != nil {
			log.Error().Err(err).Msg("Failed to update read entry")
			return fmt.Errorf("failed to update read entry: %w", err)
		}

		log.Info().
			Int64("progress_seconds", int64(progressSeconds)).
			Msg("Updated existing read entry with new progress")
	} else {
		// Create a new read entry
		editionIDInt, _ := strconv.ParseInt(editionID, 10, 64)
		startedAtStr := now.Format(time.RFC3339)
		readInput := hardcover.InsertUserBookReadInput{
			UserBookID: userBookID,
			DatesRead: hardcover.DatesReadInput{
				Action:     nil, // No action needed for in-progress books
				EditionID:  &editionIDInt,
				StartedAt:  &startedAtStr,
			},
		}

		log.Debug().
			Interface("read_input", readInput).
			Msg("Creating new read entry")

		_, err = s.hardcover.InsertUserBookRead(ctx, readInput)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create new read entry")
			return fmt.Errorf("failed to create new read entry: %w", err)
		}

		log.Info().
			Int64("progress_seconds", int64(progressSeconds)).
			Msg("Created new read entry")

		// After creating a new read entry, check if we need to update the edition_id
		// on the user_book record if it's missing or different
		if editionID != "" {
			userBook, err := s.hardcover.GetUserBook(ctx, userBookID)
			if err == nil && userBook != nil {
				// If we have an edition ID, update the user book with it
				editionIDInt, err := strconv.ParseInt(editionID, 10, 64)
				if err != nil {
					log.Error().Err(err).Str("edition_id", editionID).Msg("Failed to parse edition ID")
					return fmt.Errorf("failed to parse edition ID: %w", err)
				}

				editionIDInt64 := editionIDInt // Create a new variable to ensure we're using int64
				err = s.hardcover.UpdateUserBook(ctx, hardcover.UpdateUserBookInput{
					ID:        userBookID,
					EditionID: &editionIDInt64,
				})
				if err != nil {
					log.Error().Err(err).Msg("Failed to update user book with edition ID")
					return fmt.Errorf("failed to update user book with edition ID: %w", err)
				}

				log.Info().
					Int64("edition_id", editionIDInt).
					Msg("Updated user book with edition ID")
			}
		}
	}

	// Update the book status to READING if not already
	if err := s.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{
		ID:     userBookID,
		Status: "READING",
	}); err != nil {
		log.Error().Err(err).Msg("Failed to update book status to READING")
		return fmt.Errorf("failed to update book status: %w", err)
	}

	log.Info().
		Int("progress_seconds", progressSeconds).
		Msg("Successfully updated reading progress")

	return nil
}

// findBookInHardcover finds a book in Hardcover by various identifiers in order: ASIN → ISBN → ISBN10 → title/author
// It returns the book details along with the user's book ID if found.
func (s *Service) findBookInHardcover(ctx context.Context, book models.AudiobookshelfBook) (*models.HardcoverBook, error) {
	// Create a logger with book context
	log := s.log.With().
		Str("book_id", book.ID).
		Str("title", book.Media.Metadata.Title).
		Str("author", book.Media.Metadata.AuthorName).
		Str("isbn", book.Media.Metadata.ISBN).
		Str("asin", book.Media.Metadata.ASIN).
		Logger()

	log.Debug().
		Str("search_methods", "ASIN → ISBN → ISBN10 → title/author").
		Msg("Starting book search in Hardcover")

	// Get the current user ID
	userID, err := s.hardcover.GetCurrentUserID(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get current user ID from Hardcover API")
		return nil, fmt.Errorf("failed to get current user ID: %w", err)
	}

	// Convert userID to int64 for consistent logging
	userID64 := int64(userID)
	log.Debug().Int64("user_id", userID64).Msg("Successfully retrieved current user ID from Hardcover API")

	// Note: The findOrCreateUserBookID function is used instead of this inline function

	// 1. Try to find by ASIN first (most reliable for audiobooks)
	if book.Media.Metadata.ASIN != "" {
		log.Info().
			Str("search_method", "asin").
			Str("asin", book.Media.Metadata.ASIN).
			Msg("Searching for book by ASIN")

		// Build the GraphQL query to find the book by ASIN
		query := `
		query BookByASIN($asin: String!) {
		  books(where: { 
		    editions: { 
		      _and: [
		        { asin: { _eq: $asin } },
		        { reading_format: { id: { _eq: 2 } } }
		      ]
		    } 
		  }, limit: 1) {
		    id
		    title
		    book_status_id
		    canonical_id
		    editions(where: { 
		      _and: [
		        { asin: { _eq: $asin } },
		        { reading_format: { id: { _eq: 2 } } }
		      ]
		    }, limit: 1) {
		      id
		      asin
		      isbn_13
		      isbn_10
		      reading_format_id
		      audio_seconds
		    }
		  }
		}`

		type Publisher struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}

		type Language struct {
			ID       int    `json:"id"`
			Language string `json:"language"`
		}

		// Define the response structure to match the GraphQL response
		type Edition struct {
			ID              json.Number `json:"id"`
			ASIN            string      `json:"asin"`
			ISBN13          string      `json:"isbn_13"`
			ISBN10          string      `json:"isbn_10"`
			ReadingFormatID *int        `json:"reading_format_id"`
			AudioSeconds    *int        `json:"audio_seconds"`
		}

		type Book struct {
			ID           json.Number `json:"id"`
			Title        string      `json:"title"`
			BookStatusID int         `json:"book_status_id"`
			CanonicalID  *int        `json:"canonical_id"`
			Editions     []Edition   `json:"editions"`
		}

		// Define the response structure to match the GraphQL response
		// The GraphQL client returns an array of books directly
		type Response []struct {
			ID           json.Number `json:"id"`
			Title        string      `json:"title"`
			BookStatusID int         `json:"book_status_id"`
			CanonicalID  *int        `json:"canonical_id"`
			Editions     []struct {
				ID              json.Number `json:"id"`
				ASIN            string      `json:"asin"`
				ISBN13          *string     `json:"isbn_13"`
				ISBN10          *string     `json:"isbn_10"`
				ReadingFormatID *int        `json:"reading_format_id"`
				AudioSeconds    *int        `json:"audio_seconds"`
			} `json:"editions"`
		}

		var result Response

		// Create variables map for the query
		asin := book.Media.Metadata.ASIN
		variables := map[string]interface{}{
			"asin": asin,
		}

		// Log the query and variables for debugging
		s.log.Debug().
			Str("search_method", "asin").
			Str("asin", asin).
			Str("query", query).
			Interface("variables", variables).
			Msg("Executing GraphQL query to find book by ASIN")

		// Format the query by removing leading whitespace from each line
		formattedQuery := strings.TrimSpace(query)
		formattedQuery = strings.ReplaceAll(formattedQuery, "\n\t\t", " ")
		formattedQuery = strings.ReplaceAll(formattedQuery, "\n\t", " ")
		formattedQuery = strings.ReplaceAll(formattedQuery, "\n", " ")
		formattedQuery = strings.Join(strings.Fields(formattedQuery), " ")

		err = s.hardcover.GraphQLQuery(ctx, formattedQuery, variables, &result)

		if err != nil {
			s.log.Error().
				Err(err).
				Str("query", formattedQuery).
				Interface("variables", variables).
				Msg("Error executing GraphQL query for book by ASIN")
			log.Warn().
				Err(err).
				Str("search_method", "asin").
				Msg("Failed to execute GraphQL query")
			return nil, fmt.Errorf("failed to execute GraphQL query: %w", err)
		}

		// Log the full response for debugging
		logEntry := s.log.Debug().
			Str("search_method", "asin").
			Str("asin", asin)

		// Only include the full response in debug mode to avoid log spam
		if s.config.Logging.Level == "debug" {
			logEntry = logEntry.Interface("full_response", result)
		}

		logEntry.Msg("GraphQL query response")

		// Log more details about the response
		if len(result) > 0 {
			bookData := result[0]
			
			// Convert book ID to int64 for logging
			bookID, err := bookData.ID.Int64()
			if err != nil {
				s.log.Warn().
					Err(err).
					Str("book_id", bookData.ID.String()).
					Msg("Failed to convert book ID to int64 for logging")
				bookID = 0 // Use 0 as fallback for logging
			}

			// Create a debug log entry with the book data
			logEntry := s.log.Debug().
				Str("search_method", "asin").
				Str("book_id", bookData.ID.String()).
				Str("title", bookData.Title).
				Int("editions_count", len(bookData.Editions))

			// Only add the int64 book_id if conversion was successful
			if bookID != 0 {
				logEntry = logEntry.Int64("book_id_int64", bookID)
			}

			logEntry.Msg("Found matching book by ASIN")

			// Log edition details
			for i, edition := range bookData.Editions {
				editionID, err := edition.ID.Int64()
				editionLog := s.log.Debug().
					Int("edition_index", i).
					Str("edition_id", edition.ID.String()).
					Str("edition_asin", edition.ASIN)

				// Add ISBN fields if they are not nil
				if edition.ISBN13 != nil {
					editionLog = editionLog.Str("isbn13", *edition.ISBN13)
				}
				if edition.ISBN10 != nil {
					editionLog = editionLog.Str("isbn10", *edition.ISBN10)
				}

				// Only add the int64 edition_id if conversion was successful
				if err == nil {
					editionLog = editionLog.Int64("edition_id_int64", editionID)
				} else {
					s.log.Warn().
						Err(err).
						Str("edition_id", edition.ID.String()).
						Msg("Failed to convert edition ID to int64 for logging")
				}

				// Add optional fields if they exist
				if edition.ReadingFormatID != nil {
					editionLog = editionLog.Interface("reading_format_id", edition.ReadingFormatID)
				}
				if edition.AudioSeconds != nil {
					editionLog = editionLog.Interface("audio_seconds", edition.AudioSeconds)
				}

				editionLog.Msg("Edition details")
			}
		}

		// Check if we found any books
		if len(result) == 0 {
			s.log.Debug().
				Str("search_method", "asin").
				Str("asin", asin).
				Msg("No books found with matching ASIN")
			return nil, nil
		}

		// Get the first book and its first edition
		hcBook := result[0]

		// Convert book ID to int64
		bookID, err := hcBook.ID.Int64()
		if err != nil {
			s.log.Error().
				Err(err).
				Str("book_id", hcBook.ID.String()).
				Msg("Failed to parse book ID")
			return nil, fmt.Errorf("failed to parse book ID: %w", err)
		}

		s.log.Debug().
			Str("search_method", "asin").
			Str("asin", asin).
			Int64("book_id", bookID).
			Str("title", hcBook.Title).
			Msg("Found book by ASIN")

		// Get the first edition (we only requested one with audio format)
		if len(hcBook.Editions) == 0 {
			s.log.Warn().
				Str("search_method", "asin").
				Str("asin", asin).
				Str("book_id", hcBook.ID.String()).
				Msg("Book found but no audio editions available")
			return nil, nil
		}

		edition := hcBook.Editions[0]

		// Convert edition ID to int64
		editionID, err := edition.ID.Int64()
		if err != nil {
			s.log.Error().
				Err(err).
				Str("edition_id", edition.ID.String()).
				Msg("Failed to parse edition ID")
			return nil, fmt.Errorf("failed to parse edition ID: %w", err)
		}

		s.log.Debug().
			Str("search_method", "asin").
			Str("asin", asin).
			Int64("book_id", bookID).
			Int64("edition_id", editionID).
			Str("edition_asin", edition.ASIN)

		// Add ISBN fields if they are not nil
		if edition.ISBN13 != nil {
			s.log.Debug().
				Str("search_method", "asin").
				Str("asin", asin).
				Int64("book_id", bookID).
				Int64("edition_id", editionID).
				Str("isbn_13", *edition.ISBN13)
		}
		if edition.ISBN10 != nil {
			s.log.Debug().
				Str("search_method", "asin").
				Str("asin", asin).
				Int64("book_id", bookID).
				Int64("edition_id", editionID).
				Str("isbn_10", *edition.ISBN10)
		}

		// Create a log entry with common fields
		editionLog := s.log.Debug().
			Str("search_method", "asin").
			Str("asin", asin).
			Int64("book_id", bookID).
			Int64("edition_id", editionID).
			Str("edition_asin", edition.ASIN)

		// Add optional fields if they are not nil
		if edition.ISBN13 != nil {
			editionLog = editionLog.Str("isbn_13", *edition.ISBN13)
		}
		if edition.ISBN10 != nil {
			editionLog = editionLog.Str("isbn_10", *edition.ISBN10)
		}
		if edition.ReadingFormatID != nil {
			editionLog = editionLog.Int("reading_format_id", *edition.ReadingFormatID)
		}
		if edition.AudioSeconds != nil {
			editionLog = editionLog.Int("audio_seconds", *edition.AudioSeconds)
		}

		editionLog.Msg("Found audio edition for book")

		// Get or create user book ID for this edition
		editionIDStr := strconv.FormatInt(editionID, 10)
		// Determine the book status based on progress and finished state
		progress := 0.0
		isFinished := false
		var finishedAt int64
		if book.Media.Duration > 0 {
			progress = book.Progress.CurrentTime / book.Media.Duration
			isFinished = book.Progress.IsFinished
			finishedAt = book.Progress.FinishedAt
		}
		status := s.determineBookStatus(progress, isFinished, finishedAt)
		userBookID, err := s.findOrCreateUserBookID(ctx, editionIDStr, status)
		if err != nil {
			s.log.Warn().
				Err(err).
				Int64("edition_id", editionID).
				Msg("Failed to get or create user book ID, using edition ID")
			// Use edition ID as fallback
			userBookID = editionID
		}

		s.log.Debug().
			Int64("user_book_id", userBookID).
			Int64("edition_id", editionID).
			Msg("Found user book ID for edition")

		// Create the HardcoverBook instance
		return &models.HardcoverBook{
			ID:            hcBook.ID.String(), // Convert json.Number to string
			UserBookID:    strconv.FormatInt(userBookID, 10),
			EditionID:     editionIDStr,
			Title:         book.Media.Metadata.Title,
			BookStatusID:  hcBook.BookStatusID,
			CanonicalID:   hcBook.CanonicalID,
			EditionASIN:   edition.ASIN,
			EditionISBN13: stringValue(edition.ISBN13),
			EditionISBN10: stringValue(edition.ISBN10),
		}, nil
	}

	// 2. Try to find by ISBN if ASIN search failed
	if book.Media.Metadata.ISBN != "" {
		log.Warn().
			Str("search_method", "asin").
			Str("asin", book.Media.Metadata.ASIN).
			Msg("No book found by ASIN, will try other methods")

		// Normalize ISBN (remove dashes, spaces, etc.)
		normalizedISBN := strings.ReplaceAll(book.Media.Metadata.ISBN, "-", "")
		normalizedISBN = strings.ReplaceAll(normalizedISBN, " ", "")

		// Try with ISBN-13 first, then ISBN-10 if needed
		type isbnType struct {
			field string
			value string
		}
		var isbnTypes []isbnType
		if normalizedISBN != "" {
			isbnTypes = append(isbnTypes, isbnType{field: "isbn_13", value: normalizedISBN})
		}

		// If it's a valid ISBN-10, try that too
		if len(normalizedISBN) == 10 {
			isbnTypes = append(isbnTypes, struct {
				field string
				value string
			}{
				field: "isbn_10",
				value: normalizedISBN,
			})
		}

		for _, isbnType := range isbnTypes {
			// Define the GraphQL query with proper variable definitions
			query := `
			query BookByISBN($identifier: String!) {
			  books(
			    where: { 
			      editions: { 
			        _and: [
			          { ` + isbnType.field + `: { _eq: $identifier } },
			          { reading_format: { id: { _eq: 2 } } }
			        ]
			      } 
			    },
			    limit: 1
			  ) {
			    id
			    title
			    book_status_id
			    canonical_id
			    editions(
			      where: { 
			        _and: [
			          { ` + isbnType.field + `: { _eq: $identifier } },
			          { reading_format: { id: { _eq: 2 } } }
			        ]
			      },
			      limit: 1
			    ) {
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
			type Response struct {
				Data struct {
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
				} `json:"data"`
			}

			var result Response

			// Execute the GraphQL query
			err := s.hardcover.GraphQLQuery(ctx, query, map[string]interface{}{
				"identifier": isbnType.value,
			}, &result)

			if err != nil {
				s.log.Warn().
					Err(err).
					Str("isbn_type", isbnType.field).
					Msg(fmt.Sprintf("Search by %s failed, trying next method", isbnType.field))
				continue
			}

			// Check if we got any results
			if len(result.Data.Books) == 0 {
				s.log.Debug().
					Str("isbn_type", isbnType.field).
					Str("identifier", isbnType.value).
					Msg("No books found with the specified identifier")
				continue
			}

			bookData := result.Data.Books[0]

			// Skip if no editions found
			if len(bookData.Editions) == 0 {
				s.log.Warn().
					Str("book_id", bookData.ID).
					Msg("No editions found for book, skipping")
				continue
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
					Msg("Failed to get or create user book ID")
			} else {
				hcBook.UserBookID = strconv.FormatInt(userBookID, 10)
			}

			s.log.Info().
				Str("search_method", "isbn").
				Str("book_id", bookData.ID).
				Str("edition_id", edition.ID).
				Msg("Found book by ISBN")

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
				Str("hardcover_id", bookData.ID).
				Str("edition_id", edition.ID).
				Str("user_book_id", hcBook.UserBookID).
				Str("isbn_type", isbnType.field).
				Msg(fmt.Sprintf("Found book in Hardcover by %s", isbnType.field))

			return hcBook, nil
		}

		errMsg := "failed to find book by ISBN/ASIN"
		s.log.Error().
			Str("book_id", book.ID).
			Str("title", book.Media.Metadata.Title).
			Str("author", book.Media.Metadata.AuthorName).
			Str("isbn", book.Media.Metadata.ISBN).
			Str("asin", book.Media.Metadata.ASIN).
			Msg(errMsg)

		return nil, fmt.Errorf(errMsg)
	}

	// 3. Try to find by title and author (least reliable, but better than nothing)
	if book.Media.Metadata.Title != "" && book.Media.Metadata.AuthorName != " " {
		s.log.Info().
			Str("search_method", "title_author").
			Str("title", book.Media.Metadata.Title).
			Str("author", book.Media.Metadata.AuthorName).
			Msg("Searching for book by title and author")

		// Build the GraphQL query to find the book by title and author
		query := `
		query BookByTitleAuthor($title: String!, $author: String!) {
		  books(where: { 
		    _and: [
		      { title: { _ilike: $title } },
		      { authors: { name: { _ilike: $author } } },
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
			Data struct {
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
			} `json:"data"`
		}

		// Execute the GraphQL query
		err := s.hardcover.GraphQLQuery(ctx, query, map[string]interface{}{
			"title":  "%" + book.Media.Metadata.Title + "%",
			"author": "%" + book.Media.Metadata.AuthorName + "%",
		}, &result)

		if err != nil {
			s.log.Warn().
				Err(err).
				Str("title", book.Media.Metadata.Title).
				Str("author", book.Media.Metadata.AuthorName).
				Msg("Search by title and author failed")
		} else if len(result.Data.Books) > 0 {
			bookData := result.Data.Books[0]

			// Skip if no editions found
			if len(bookData.Editions) == 0 {
				s.log.Warn().
					Str("book_id", bookData.ID).
					Msg("No editions found for book, skipping")
			} else {
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
						Msg("Failed to get or create user book ID")
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
					Msg("Found book by title and author")

				return hcBook, nil
			}
		}
	}
	
	// If we get here, we couldn't find the book by any method
	s.log.Warn().
		Str("book_id", book.ID).
		Str("title", book.Media.Metadata.Title).
		Str("author", book.Media.Metadata.AuthorName).
		Str("isbn", book.Media.Metadata.ISBN).
		Str("asin", book.Media.Metadata.ASIN).
		Msg("Book not found in Hardcover by any search method")

	return nil, fmt.Errorf("book not found in Hardcover by any search method")
}
