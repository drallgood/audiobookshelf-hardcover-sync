package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/hardcover"
	httpclient "github.com/drallgood/audiobookshelf-hardcover-sync/http"
	"github.com/drallgood/audiobookshelf-hardcover-sync/types"
)

// shouldSyncBook determines if a book should be synced based on configuration and book state
func shouldSyncBook(book Audiobook, cfg *config.Config) bool {
	// Always sync if we're in debug mode
	if cfg.App.Debug {
		return true
	}

	// Skip if the book has no progress
	if book.Progress <= 0 {
		return false
	}

	// Skip if the book is finished (progress >= 100%)
	if book.Progress >= 100 {
		return false
	}

	// Sync if the book has been started but not finished
	return book.CurrentTime > 0
}

// syncToHardcover syncs a single audiobook to Hardcover with comprehensive book matching
// and conditional sync logic to avoid unnecessary API calls
func syncToHardcover(a *Audiobook) error {
	if a == nil {
		return fmt.Errorf("cannot sync nil audiobook")
	}
	var bookId, editionId string
	var asinLookupSucceeded bool // Track if ASIN lookup found audiobook edition

	debugLog("Starting book matching for '%s' by '%s' (ISBN: %s, ASIN: %s)", a.Title, a.Author, a.ISBN, a.ASIN)

	// PRIORITY 1: Try ASIN first since it's most likely to match the actual audiobook edition
	if a.ASIN != "" {
		query := `
		query BookByASIN($asin: String!) {
		  books(where: { editions: { asin: { _eq: $asin }, reading_format_id: { _eq: 2 } } }, limit: 1) {
			id
			title
			book_status_id
			canonical_id
			editions(where: { asin: { _eq: $asin }, reading_format_id: { _eq: 2 } }) {
			  id
			  asin
			  isbn_13
			  isbn_10
			  reading_format_id
			  audio_seconds
			}
		  }
		}`
		variables := map[string]interface{}{"asin": a.ASIN}
		payload := map[string]interface{}{"query": query, "variables": variables}
		payloadBytes, _ := json.Marshal(payload)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("hardcover books query error: %s - %s", resp.Status, string(body))
		}
		var result struct {
			Data struct {
				Books []struct {
					ID           json.Number `json:"id"`
					BookStatusID int         `json:"book_status_id"`
					CanonicalID  *int        `json:"canonical_id"`
					Editions     []struct {
						ID              json.Number `json:"id"`
						ASIN            string      `json:"asin"`
						ReadingFormatID *int        `json:"reading_format_id"`
						AudioSeconds    *int        `json:"audio_seconds"`
					} `json:"editions"`
				} `json:"books"`
			} `json:"data"`
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		if len(result.Data.Books) > 0 && len(result.Data.Books[0].Editions) > 0 {
			book := result.Data.Books[0]
			bookId = book.ID.String()
			// Handle deduped books: use canonical_id if book_status_id = 4 (deduped)
			if book.BookStatusID == 4 && book.CanonicalID != nil {
				bookId = fmt.Sprintf("%d", *book.CanonicalID)
				debugLog("Book ID %s is deduped (status 4), using canonical_id %d instead", book.ID.String(), *book.CanonicalID)
			}
			editionId = result.Data.Books[0].Editions[0].ID.String()
			asinLookupSucceeded = true
			debugLog("Found audiobook via ASIN: bookId=%s, editionId=%s, format_id=%v, audio_seconds=%v",
				bookId, editionId, result.Data.Books[0].Editions[0].ReadingFormatID, result.Data.Books[0].Editions[0].AudioSeconds)
		} else {
			debugLog("No audiobook edition found for ASIN %s", a.ASIN)
		}
	}

	// PRIORITY 2: Try ISBN/ISBN13 only if ASIN didn't work and ISBN is different from ASIN
	if bookId == "" && a.ISBN != "" && (a.ASIN == "" || a.ISBN != a.ASIN) {
		// Query for book and edition by ISBN/ISBN13
		query := `
		query BookByISBN($isbn: String!) {
		  books(where: { editions: { isbn_13: { _eq: $isbn } } }, limit: 1) {
			id
			title
			book_status_id
			canonical_id
			editions(where: { isbn_13: { _eq: $isbn } }) {
			  id
			  isbn_13
			  isbn_10
			  asin
			}
		  }
		}`
		variables := map[string]interface{}{"isbn": a.ISBN}
		payload := map[string]interface{}{"query": query, "variables": variables}
		payloadBytes, _ := json.Marshal(payload)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("hardcover books query error: %s - %s", resp.Status, string(body))
		}
		var result struct {
			Data struct {
				Books []struct {
					ID           json.Number `json:"id"`
					BookStatusID int         `json:"book_status_id"`
					CanonicalID  *int        `json:"canonical_id"`
					Editions     []struct {
						ID     json.Number `json:"id"`
						ISBN10 string      `json:"isbn_10"`
						ASIN   string      `json:"asin"`
					} `json:"editions"`
				} `json:"books"`
			} `json:"data"`
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		if len(result.Data.Books) > 0 {
			book := result.Data.Books[0]
			bookId = book.ID.String()
			// Handle deduped books: use canonical_id if book_status_id = 4 (deduped)
			if book.BookStatusID == 4 && book.CanonicalID != nil {
				bookId = fmt.Sprintf("%d", *book.CanonicalID)
				debugLog("Book ID %s is deduped (status 4), using canonical_id %d instead", book.ID.String(), *book.CanonicalID)
			}
			if len(result.Data.Books[0].Editions) > 0 {
				editionId = result.Data.Books[0].Editions[0].ID.String()
			}
		}
	}
	// 1b. Try ISBN10 if present and not already tried
	if bookId == "" && a.ISBN10 != "" {
		query := `
		query BookByISBN10($isbn10: String!) {
		  books(where: { editions: { isbn_10: { _eq: $isbn10 } } }, limit: 1) {
			id
			title
			book_status_id
			canonical_id
			editions(where: { isbn_10: { _eq: $isbn10 } }) {
			  id
			  isbn_13
			  isbn_10
			  asin
			}
		  }
		}`
		variables := map[string]interface{}{"isbn10": a.ISBN10}
		payload := map[string]interface{}{"query": query, "variables": variables}
		payloadBytes, _ := json.Marshal(payload)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("hardcover books query error: %s - %s", resp.Status, string(body))
		}
		var result struct {
			Data struct {
				Books []struct {
					ID           json.Number `json:"id"`
					BookStatusID int         `json:"book_status_id"`
					CanonicalID  *int        `json:"canonical_id"`
					Editions     []struct {
						ID     json.Number `json:"id"`
						ISBN10 string      `json:"isbn_10"`
					} `json:"editions"`
				} `json:"books"`
			} `json:"data"`
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		if len(result.Data.Books) > 0 {
			book := result.Data.Books[0]
			bookId = book.ID.String()
			// Handle deduped books: use canonical_id if book_status_id = 4 (deduped)
			if book.BookStatusID == 4 && book.CanonicalID != nil {
				bookId = fmt.Sprintf("%d", *book.CanonicalID)
				debugLog("Book ID %s is deduped (status 4), using canonical_id %d instead", book.ID.String(), *book.CanonicalID)
			}
			if len(result.Data.Books[0].Editions) > 0 {
				editionId = result.Data.Books[0].Editions[0].ID.String()
			}
		}
	}

	// PRIORITY 3: Fallback to title/author lookup
	if bookId == "" {
		var err error
		bookId, err = lookupHardcoverBookID(a.Title, a.Author)
		if err != nil {
			// Apply the audiobook match mode when title/author lookup fails too
			matchMode := getAudiobookMatchMode()
			switch matchMode {
			case "fail":
				return fmt.Errorf("book lookup failed for audiobook '%s' by '%s' (ISBN: %s, ASIN: %s) and AUDIOBOOK_MATCH_MODE=fail: %v", a.Title, a.Author, a.ISBN, a.ASIN, err)
			case "skip":
				// Collect this mismatch for manual review before skipping
				reason := fmt.Sprintf("Book lookup failed - not found in Hardcover database using ASIN %s, ISBN %s, or title/author search", a.ASIN, a.ISBN)
				addBookMismatchWithMetadata(a.Metadata, "", "", reason, a.TotalDuration, a.ID)

				debugLog("SKIPPING: Book lookup failed for '%s' by '%s' (ISBN: %s, ASIN: %s) and AUDIOBOOK_MATCH_MODE=skip: %v", a.Title, a.Author, a.ISBN, a.ASIN, err)
				return nil // Skip this book entirely
			default: // "continue"
				return fmt.Errorf("could not find Hardcover bookId for '%s' by '%s' (ISBN: %s, ASIN: %s): %v", a.Title, a.Author, a.ISBN, a.ASIN, err)
			}
		}
	}

	// Validate that we have a valid bookId before proceeding
	if bookId == "" {
		return fmt.Errorf("failed to find valid Hardcover bookId for '%s' by '%s' (ISBN: %s, ASIN: %s) - all lookup methods returned empty bookId", a.Title, a.Author, a.ISBN, a.ASIN)
	}

	// SAFETY CHECK: Handle audiobook edition matching behavior
	// This helps prevent syncing audiobook progress to wrong editions (ebook/physical)
	if editionId == "" || (a.ASIN != "" && !asinLookupSucceeded) {
		matchMode := getAudiobookMatchMode()

		switch matchMode {
		case "fail":
			if a.ASIN != "" {
				return fmt.Errorf("ASIN lookup failed for audiobook '%s' (ASIN: %s) and AUDIOBOOK_MATCH_MODE=fail. Cannot guarantee correct audiobook edition match", a.Title, a.ASIN)
			} else {
				return fmt.Errorf("no audiobook edition found for '%s' by '%s' (ISBN: %s) and AUDIOBOOK_MATCH_MODE=fail. Cannot guarantee correct audiobook edition match", a.Title, a.Author, a.ISBN)
			}
		case "skip":
			// Collect this mismatch for manual review before skipping
			var reason string
			if a.ASIN != "" {
				reason = fmt.Sprintf("ASIN lookup failed for ASIN %s - no audiobook edition found. Book was skipped to avoid wrong edition sync.", a.ASIN)
				debugLog("SKIPPING: ASIN lookup failed for '%s' (ASIN: %s) and AUDIOBOOK_MATCH_MODE=skip. Avoiding potential wrong edition sync.", a.Title, a.ASIN)
			} else {
				reason = fmt.Sprintf("No audiobook edition found using ISBN %s - only non-audiobook editions available. Book was skipped to avoid wrong edition sync.", a.ISBN)
				debugLog("SKIPPING: No audiobook edition found for '%s' by '%s' (ISBN: %s) and AUDIOBOOK_MATCH_MODE=skip. Avoiding potential wrong edition sync.", a.Title, a.Author, a.ISBN)
			}

			addBookMismatchWithMetadata(a.Metadata, bookId, editionId, reason, a.TotalDuration, a.ID)

			return nil // Skip this book entirely
		default: // "continue"
			var reason string
			if a.ASIN != "" {
				reason = fmt.Sprintf("ASIN lookup failed for ASIN %s, using fallback book matching. Progress may not sync correctly if this isn't the audiobook edition.", a.ASIN)
				debugLog("WARNING: ASIN lookup failed for '%s' (ASIN: %s), using fallback book matching. Progress may not sync correctly if this isn't the audiobook edition.", a.Title, a.ASIN)
			} else {
				reason = fmt.Sprintf("No audiobook edition found using ISBN %s, using general book matching. Progress may not sync correctly if this isn't the audiobook edition.", a.ISBN)
				debugLog("WARNING: No audiobook edition found for '%s' by '%s' (ISBN: %s), using general book matching. Progress may not sync correctly if this isn't the audiobook edition.", a.Title, a.Author, a.ISBN)
			}

			// Collect this mismatch for manual review
			addBookMismatchWithMetadata(a.Metadata, bookId, editionId, reason, a.TotalDuration, a.ID)

			debugLog("To change this behavior, set AUDIOBOOK_MATCH_MODE=skip or AUDIOBOOK_MATCH_MODE=fail")
		}
	}

	// Step 2: Check if user already has this book and compare status/progress
	existingUserBookId, existingStatusId, existingProgressSeconds, existingOwned, existingEditionId, userBookReadEditionId, err := checkExistingUserBook(bookId)
	if err != nil {
		return fmt.Errorf("failed to check existing user book for '%s': %v", a.Title, err)
	}

	debugLog("DEBUG: checkExistingUserBook returned - userBookId=%d, statusId=%d, progressSeconds=%d, owned=%t, editionId=%d, userBookReadEditionId=%v",
		existingUserBookId, existingStatusId, existingProgressSeconds, existingOwned, existingEditionId, userBookReadEditionId)

	// Determine the target status for this book with enhanced finished book detection
	targetStatusId := 3 // default to read

	// Check if book is actually finished despite showing 0% progress
	// This can happen when enhanced detection fails to properly identify finished books
	isBookFinished := a.Progress >= 0.99
	debugLog("Initial finished status for '%s': isBookFinished=%t (progress=%.6f)", a.Title, isBookFinished, a.Progress)

	// Additional checks for finished status using enhanced detection via /api/authorize
	if !isBookFinished && a.Progress == 0 {
		debugLog("Running enhanced finished book detection for '%s' (0%% progress)", a.Title)

		// Use the new authorize endpoint detection
		if enhanceFinishedBookDetection(a.Title, a.ID, a.Progress) {
			isBookFinished = true
			debugLog("✅ FINISHED BOOK DETECTED: Enhanced detection found book '%s' is actually finished via /api/authorize", a.Title)
		}

		// Also check if book appears to be finished based on listening position
		if !isBookFinished && a.CurrentTime > 0 && a.TotalDuration > 0 {
			actualProgress := a.CurrentTime / a.TotalDuration
			remainingTime := a.TotalDuration - a.CurrentTime
			debugLog("Position-based detection for '%s': actualProgress=%.6f (%.2f%%), remainingTime=%.1fs",
				a.Title, actualProgress, actualProgress*100, remainingTime)
			if actualProgress >= 0.95 || remainingTime <= 300 { // Within 95% or 5 minutes of end
				isBookFinished = true
				debugLog("✅ FINISHED BOOK DETECTED: Book '%s' appears finished based on position: %.2f%% complete, %.1f minutes remaining",
					a.Title, actualProgress*100, remainingTime/60)
			}
		}
	}

	// Set status based on actual finished state
	if isBookFinished {
		targetStatusId = 3 // read
	} else if a.Progress > 0 || (a.CurrentTime > 0 && a.TotalDuration > 0) {
		targetStatusId = 2 // currently reading
	} else if getSyncWantToRead() {
		// Only set to "want to read" if we're certain the book is not finished
		targetStatusId = 1 // want to read
	} else {
		// If want to read sync is disabled and book shows no progress, skip it
		debugLog("Skipping book '%s' with 0%% progress (SYNC_WANT_TO_READ disabled)", a.Title)
		targetStatusId = 2 // default to currently reading
	}

	// Calculate target progress in seconds with unit conversion
	var targetProgressSeconds int
	if a.Progress > 0 {
		if a.CurrentTime > 0 {
			// Apply unit conversion to fix millisecond/second issues
			convertedCurrentTime := convertTimeUnits(a.CurrentTime, a.TotalDuration)
			targetProgressSeconds = int(math.Round(convertedCurrentTime))
			debugLog("Progress calculation for '%s': originalCurrentTime=%.2f, convertedCurrentTime=%.2f, targetProgressSeconds=%d",
				a.Title, a.CurrentTime, convertedCurrentTime, targetProgressSeconds)
		} else if a.TotalDuration > 0 && a.Progress > 0 {
			targetProgressSeconds = int(math.Round(a.Progress * a.TotalDuration))
		} else {
			// Fallback: use progress percentage * reasonable audiobook duration (10 hours)
			fallbackDuration := 36000.0 // 10 hours in seconds
			targetProgressSeconds = int(math.Round(a.Progress * fallbackDuration))
		}
		// Ensure we have at least 1 second of progress
		if targetProgressSeconds < 1 {
			targetProgressSeconds = 1
		}
	}

	// Check if we need to sync (book doesn't exist OR status/progress has changed OR re-read scenario)
	var userBookId int
	needsSync := false

	if existingUserBookId == 0 {
		// Book doesn't exist - need to create it
		needsSync = true
		debugLog("Book '%s' not found in user's Hardcover library - will create", a.Title)
	} else {
		// Book exists - check if status or progress has meaningfully changed OR if this is a re-read scenario
		userBookId = existingUserBookId

		// EXPECTATION #4 IMPLEMENTATION: Check for re-read scenario
		// If there's a finished read in Hardcover but AudiobookShelf shows in-progress (< 99%),
		// this indicates a re-read and should always sync
		hasExistingFinishedRead, finishDate, err := checkExistingFinishedRead(userBookId)
		if err != nil {
			debugLog("Warning: Failed to check existing finished reads for '%s': %v", a.Title, err)
			// Continue with normal logic if check fails
		} else if hasExistingFinishedRead && a.Progress < 0.99 {
			// CRITICAL FIX: Be more conservative about re-read detection
			// Check if this is actually a finished book that AudiobookShelf is reporting incorrectly
			debugLog("FINISHED BOOK CHECK: Book '%s' has finished read in Hardcover and shows %.2f%% progress in ABS, but isBookFinished=%t",
				a.Title, a.Progress*100, isBookFinished)

			if isBookFinished {
				// This is a manually finished book that AudiobookShelf is reporting with 0% progress
				// Don't treat as re-read, treat as already finished
				debugLog("FINISHED BOOK: Book '%s' is actually finished in AudiobookShelf despite 0%% progress - skipping sync (already finished in both systems)",
					a.Title)
				return nil // Skip this book entirely
			} else if a.Progress == 0 && a.CurrentTime == 0 {
				// CONSERVATIVE APPROACH: If book shows 0% progress AND 0 current time,
				// it might be a manually finished book that we can't detect properly.
				// Don't assume it's a re-read - skip to avoid creating duplicate entries
				debugLog("CONSERVATIVE SKIP: Book '%s' has finished read in Hardcover but shows 0%% progress and 0 current time in AudiobookShelf - likely manually finished, skipping to avoid duplicate",
					a.Title)
				return nil // Skip this book entirely
			} else {
				// This appears to be a genuine re-read scenario (has some progress/current time)
				needsSync = true
				debugLog("RE-READ: Book '%s' has finished read in Hardcover (finished on %s) but shows %.2f%% progress in AudiobookShelf - syncing as new reading session",
					a.Title, finishDate, a.Progress*100)
			}
		} else {
			// Normal sync logic for other scenarios
			statusChanged := existingStatusId != targetStatusId

			// Consider progress changed if the difference is significant (more than 30 seconds or 10%)
			progressThreshold := int(math.Max(30, float64(targetProgressSeconds)*getProgressChangeThreshold()))
			progressChanged := targetProgressSeconds > 0 &&
				(existingProgressSeconds == 0 ||
					math.Abs(float64(targetProgressSeconds-existingProgressSeconds)) > float64(progressThreshold))

			// Check if owned flag needs updating using proper ownership checking
			targetOwned := getSyncOwned()
			var ownedChanged bool
			var actualOwned bool

			if targetOwned {
				// Use proper ownership checking instead of unreliable user_books.owned field
				bookIDInt := toInt(bookId)
				var err error
				actualOwned, _, err = isBookOwnedDirect(bookIDInt)
				if err != nil {
					debugLog("Warning: Failed to check actual ownership status for '%s': %v", a.Title, err)
					// Fall back to existingOwned if ownership check fails
					actualOwned = existingOwned
				}
				ownedChanged = targetOwned != actualOwned

				if existingOwned != actualOwned {
					debugLog("Ownership status mismatch for '%s': user_books.owned=%t, actual_owned_list=%t (using actual)",
						a.Title, existingOwned, actualOwned)
				}
			} else {
				// If sync owned is disabled, use the existing unreliable field for compatibility
				ownedChanged = targetOwned != existingOwned
			}

			// EDITION MISMATCH DETECTION: Check if user_book_read.edition_id is null or different from user_book.edition_id
			editionMismatch := false
			if existingEditionId > 0 {
				if userBookReadEditionId == nil {
					editionMismatch = true
					debugLog("EDITION MISMATCH: Book '%s' has user_book.edition_id=%d but user_book_read.edition_id is NULL - forcing update",
						a.Title, existingEditionId)
				} else if *userBookReadEditionId != existingEditionId {
					editionMismatch = true
					debugLog("EDITION MISMATCH: Book '%s' has user_book.edition_id=%d but user_book_read.edition_id=%d - forcing update",
						a.Title, existingEditionId, *userBookReadEditionId)
				}
			}

			if statusChanged || progressChanged || editionMismatch {
				needsSync = true
				debugLog("Book '%s' needs update - status changed: %t (%d->%d), progress changed: %t (%ds->%ds), edition mismatch: %t",
					a.Title, statusChanged, existingStatusId, targetStatusId, progressChanged, existingProgressSeconds, targetProgressSeconds, editionMismatch)
			} else if ownedChanged {
				// Handle owned flag change separately
				if targetOwned && !actualOwned && existingEditionId > 0 {
					// Need to mark as owned and we have an edition ID
					debugLog("Book '%s' status/progress up-to-date but needs to be marked as owned (edition_id: %d)",
						a.Title, existingEditionId)
					if err := markBookAsOwned(fmt.Sprintf("%d", existingEditionId)); err != nil {
						debugLog("Failed to mark book '%s' as owned: %v", a.Title, err)
					} else {
						debugLog("Successfully marked book '%s' as owned", a.Title)
					}
				} else if targetOwned && !actualOwned && existingEditionId == 0 {
					debugLog("Book '%s' needs to be marked as owned but no edition_id available - cannot use edition_owned mutation",
						a.Title)
				} else if !targetOwned && existingOwned {
					debugLog("Book '%s' should not be owned but currently is - cannot unmark owned flag",
						a.Title)
				}

				debugLog("Book '%s' already up-to-date in Hardcover (status: %d, progress: %ds) - skipping sync",
					a.Title, existingStatusId, existingProgressSeconds)
				return nil
			} else {
				debugLog("Book '%s' already up-to-date in Hardcover (status: %d, progress: %ds, owned: %t) - skipping",
					a.Title, existingStatusId, existingProgressSeconds, existingOwned)
				return nil
			}
		}
	}

	// Only proceed with sync if needed
	if !needsSync {
		return nil
	}

	// If book doesn't exist, create it
	if existingUserBookId == 0 {
		userBookInput := map[string]interface{}{
			"book_id":   toInt(bookId),
			"status_id": targetStatusId,
		}
		if editionId != "" {
			userBookInput["edition_id"] = toInt(editionId)
		}
		// Note: ownership is handled via the "Owned" list, not user_books.owned field
		debugLog("Creating new user book for '%s' by '%s' (Progress: %.6f) with status_id=%d, userBookInput=%+v", a.Title, a.Author, a.Progress, targetStatusId, userBookInput)

		insertUserBookMutation := `
		mutation InsertUserBook($object: UserBookCreateInput!) {
		  insert_user_book(object: $object) {
			id
			user_book { id status_id }
			error
		  }
		}`
		variables := map[string]interface{}{"object": userBookInput}
		payload := map[string]interface{}{"query": insertUserBookMutation, "variables": variables}

		debugLog("=== GraphQL Mutation: insert_user_book ===")
		debugLog("Book: '%s' by '%s'", a.Title, a.Author)
		debugLog("Target status_id: %d, Progress: %.6f", targetStatusId, a.Progress)
		debugLog("Mutation variables: %+v", variables)
		debugLog("Full mutation payload: %+v", payload)

		payloadBytes, _ := json.Marshal(payload)
		debugLog("insert_user_book request body: %s", string(payloadBytes))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for attempt := 0; attempt < 3; attempt++ {
			req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
			resp, err := httpClient.Do(req)
			if err != nil {
				continue
			}
			defer resp.Body.Close()
			if resp.StatusCode == 429 {
				if retry := resp.Header.Get("Retry-After"); retry != "" {
					if sec, err := strconv.Atoi(retry); err == nil && sec > 0 {
						time.Sleep(time.Duration(sec) * time.Second)
						continue
					}
				}
				time.Sleep(3 * time.Second)
				continue
			}
			debugLog("insert_user_book response status: %d %s", resp.StatusCode, resp.Status)
			debugLog("insert_user_book response headers: %+v", resp.Header)
			if resp.StatusCode != 200 {
				debugLog("insert_user_book non-200 response, headers: %+v", resp.Header)
				continue
			}

			var result struct {
				Data struct {
					InsertUserBook struct {
						ID       int `json:"id"`
						UserBook struct {
							ID       int `json:"id"`
							StatusID int `json:"status_id"`
						} `json:"user_book"`
						Error *string `json:"error"`
					} `json:"insert_user_book"`
				} `json:"data"`
				Errors []struct {
					Message string        `json:"message"`
					Path    []interface{} `json:"path"`
				} `json:"errors"`
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				debugLog("Failed to read response body: %v", err)
				continue
			}

			// Log the raw response for debugging
			debugLog("insert_user_book raw response: %s", string(body))

			if err := json.Unmarshal(body, &result); err != nil {
				debugLog("Failed to unmarshal response: %v", err)
				continue
			}

			// Check for GraphQL errors first
			if len(result.Errors) > 0 {
				for _, gqlErr := range result.Errors {
					debugLog("GraphQL error: %s (path: %v)", gqlErr.Message, gqlErr.Path)
				}
				return fmt.Errorf("GraphQL errors: %s", result.Errors[0].Message)
			}

			if result.Data.InsertUserBook.Error != nil {
				debugLog("insert_user_book error: %s", *result.Data.InsertUserBook.Error)
				return fmt.Errorf("insert_user_book error: %s", *result.Data.InsertUserBook.Error)
			}
			userBookId = result.Data.InsertUserBook.UserBook.ID
			debugLog("insert_user_book: id=%d, status_id=%d", userBookId, result.Data.InsertUserBook.UserBook.StatusID)
			if result.Data.InsertUserBook.UserBook.StatusID != targetStatusId {
				debugLog("Warning: Hardcover returned status_id=%d, expected %d", result.Data.InsertUserBook.UserBook.StatusID, targetStatusId)
			}

			// Handle ownership marking if enabled and edition_id is available
			if getSyncOwned() && editionId != "" {
				debugLog("Marking newly created user book as owned (edition_id: %s)", editionId)
				if err := markBookAsOwned(editionId); err != nil {
					debugLog("Warning: Failed to mark book '%s' as owned: %v", a.Title, err)
				} else {
					debugLog("Successfully marked book '%s' as owned", a.Title)
				}
			} else if getSyncOwned() && editionId == "" {
				debugLog("Warning: Cannot mark book '%s' as owned - no edition_id available", a.Title)
			}

			break
		}
		if userBookId == 0 {
			return fmt.Errorf("failed to insert user book for '%s'", a.Title)
		}
	}

	// Step 3: Insert user book read (progress) - only if we have meaningful progress
	if targetProgressSeconds > 0 {
		debugLog("Syncing progress for '%s': %d seconds (%.2f%%)", a.Title, targetProgressSeconds, a.Progress*100)

		// Check if a user_book_read already exists for today
		today := time.Now().Format("2006-01-02")
		existingData, err := checkExistingUserBookRead(userBookId, today)
		if err != nil {
			return fmt.Errorf("failed to check existing user book read for '%s': %v", a.Title, err)
		}

		if existingData != nil {
			// Update existing user_book_read if progress has changed
			existingProgressSeconds := 0
			if existingData.ProgressSeconds != nil {
				existingProgressSeconds = *existingData.ProgressSeconds
			}

			if existingProgressSeconds != targetProgressSeconds {
				debugLog("Updating existing user_book_read id=%d for '%s': progressSeconds=%d -> %d", existingData.ID, a.Title, existingProgressSeconds, targetProgressSeconds)

				updateMutation := `
				mutation UpdateUserBookRead($id: Int!, $object: DatesReadInput!) {
				  update_user_book_read(id: $id, object: $object) {
					id
					error
					user_book_read {
					  id
					  progress_seconds
					  started_at
					  finished_at
					}
				  }
				}`

				// CRITICAL FIX: Preserve ALL existing fields to prevent data loss
				updateObject := map[string]interface{}{
					"progress_seconds": targetProgressSeconds,
				}

				// Preserve existing edition_id to prevent it from being set to NULL
				if existingData.EditionID != nil {
					updateObject["edition_id"] = *existingData.EditionID
					debugLog("Preserving existing edition_id in update: %d", *existingData.EditionID)
				} else if existingEditionId > 0 {
					updateObject["edition_id"] = existingEditionId
					debugLog("Setting edition_id from lookup in update: %d", existingEditionId)
				} else {
					debugLog("WARNING: No edition_id available for update - edition field may become null")
				}

				// Preserve existing reading_format_id to prevent it from being set to NULL
				if existingData.ReadingFormatID != nil {
					updateObject["reading_format_id"] = *existingData.ReadingFormatID
					debugLog("Preserving existing reading_format_id in update: %d", *existingData.ReadingFormatID)
				}

				// Preserve the original started_at date if available, otherwise use current date
				if existingData.StartedAt != nil && *existingData.StartedAt != "" {
					updateObject["started_at"] = *existingData.StartedAt
					debugLog("Preserving original started_at date: %s", *existingData.StartedAt)
				} else {
					updateObject["started_at"] = time.Now().Format("2006-01-02")
					debugLog("No existing started_at found, using current date: %s", time.Now().Format("2006-01-02"))
				}

				// If book is finished (>= 99%), also set finished_at
				if a.Progress >= 0.99 {
					updateObject["finished_at"] = time.Now().Format("2006-01-02")
				}

				variables := map[string]interface{}{
					"id":     existingData.ID,
					"object": updateObject,
				}
				payload := map[string]interface{}{
					"query":     updateMutation,
					"variables": variables,
				}

				debugLog("=== GraphQL Mutation: update_user_book_read ===")
				debugLog("Book: '%s' by '%s'", a.Title, a.Author)
				startedAtStr := "null"
				if existingData.StartedAt != nil {
					startedAtStr = *existingData.StartedAt
				}
				debugLog("Existing read ID: %d, preserving started_at: %s", existingData.ID, startedAtStr)
				debugLog("Progress update: %d -> %d seconds", existingProgressSeconds, targetProgressSeconds)
				debugLog("Update object: %+v", updateObject)
				debugLog("Mutation variables: %+v", variables)
				debugLog("Full mutation payload: %+v", payload)

				payloadBytes, err := json.Marshal(payload)
				if err != nil {
					return fmt.Errorf("failed to marshal update_user_book_read payload: %v", err)
				}

				debugLog("update_user_book_read request body: %s", string(payloadBytes))
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
				if err != nil {
					return fmt.Errorf("failed to create request: %v", err)
				}
				req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

				resp, err := httpClient.Do(req)
				if err != nil {
					return fmt.Errorf("http request failed: %v", err)
				}
				defer resp.Body.Close()

				debugLog("update_user_book_read response status: %d %s", resp.StatusCode, resp.Status)
				debugLog("update_user_book_read response headers: %+v", resp.Header)

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to read response: %v", err)
				}

				debugLog("update_user_book_read raw response: %s", string(body))

				var result struct {
					Data struct {
						UpdateUserBookRead struct {
							ID           int     `json:"id"`
							Error        *string `json:"error"`
							UserBookRead *struct {
								ID              int     `json:"id"`
								ProgressSeconds int     `json:"progress_seconds"`
								StartedAt       string  `json:"started_at"`
								FinishedAt      *string `json:"finished_at"`
							} `json:"user_book_read"`
						} `json:"update_user_book_read"`
					} `json:"data"`
					Errors []struct {
						Message string `json:"message"`
					} `json:"errors"`
				}

				if err := json.Unmarshal(body, &result); err != nil {
					return fmt.Errorf("failed to parse response: %v", err)
				}

				debugLog("update_user_book_read result data: %+v", result.Data)

				if len(result.Errors) > 0 {
					debugLog("GraphQL errors in update_user_book_read: %+v", result.Errors)
					return fmt.Errorf("graphql errors: %v", result.Errors)
				}

				if result.Data.UpdateUserBookRead.Error != nil {
					debugLog("update_user_book_read error: %s", *result.Data.UpdateUserBookRead.Error)
					return fmt.Errorf("update error: %s", *result.Data.UpdateUserBookRead.Error)
				}

				debugLog("Successfully updated user_book_read with id: %d", result.Data.UpdateUserBookRead.ID)
				if result.Data.UpdateUserBookRead.UserBookRead != nil {
					ubr := result.Data.UpdateUserBookRead.UserBookRead
					debugLog("Updated reading progress details - ID: %d, Progress: %d seconds, Started: %s, Finished: %v",
						ubr.ID, ubr.ProgressSeconds, ubr.StartedAt, ubr.FinishedAt)
				}
				if result.Data.UpdateUserBookRead.UserBookRead != nil {
					debugLog("Confirmed progress update: %d seconds", result.Data.UpdateUserBookRead.UserBookRead.ProgressSeconds)
				}

				// Diagnose potential null edition issues in debug mode
				if debugMode && result.Data.UpdateUserBookRead.ID > 0 {
					diagnoseNullEdition(result.Data.UpdateUserBookRead.ID)
				}
			} else {
				debugLog("No update needed for existing user_book_read id=%d (progress already %d seconds)", existingData.ID, existingProgressSeconds)
			}
		} else {
			// IMPLEMENTATION OF USER'S EXACT EXPECTATIONS:
			// 1) If book is finished in ABS AND already has ANY finished read in Hardcover → do nothing (EXPECTATION #3)
			// 2) If book is finished in ABS but has NO finished read in Hardcover → create read entry
			// 3) If book is in-progress in ABS → handle reading sessions appropriately
			// 4) If book has finished read in Hardcover but is in-progress in ABS → create new read entry (re-read scenario)

			shouldCreateReadEntry := true

			if a.Progress >= 0.99 {
				// Book is finished in AudiobookShelf
				// Check if there's ANY existing finished read for this book (regardless of date)
				hasExistingFinishedRead, finishDate, err := checkExistingFinishedRead(userBookId)
				if err != nil {
					debugLog("Warning: Failed to check existing finished reads for '%s': %v", a.Title, err)
					// Continue with normal flow even if check fails
				} else if hasExistingFinishedRead {
					// EXPECTATION #3: Book is finished in ABS AND has finished read in Hardcover → DO NOTHING
					debugLog("SKIP: Book '%s' is finished in ABS but already has a finished read in Hardcover (finished on %s) - doing nothing to avoid duplicate", a.Title, finishDate)

					// Even though we're skipping progress sync, we might still need to update the status
					if existingUserBookId > 0 && existingStatusId != targetStatusId {
						debugLog("Updating user book status for '%s': %d -> %d (skipped progress sync)", a.Title, existingStatusId, targetStatusId)
						if err := updateUserBookStatus(existingUserBookId, targetStatusId); err != nil {
							debugLog("Warning: Failed to update status for '%s': %v", a.Title, err)
						} else {
							debugLog("Successfully updated status for '%s' from %d to %d", a.Title, existingStatusId, targetStatusId)
						}
					}
					return nil
				}
				// EXPECTATION #2: Book is finished in ABS but has NO finished read in Hardcover → CREATE READ ENTRY
				debugLog("CREATE: Book '%s' is finished in ABS but has no finished read in Hardcover - creating read entry with today's dates", a.Title)
			} else {
				// EXPECTATION #3 & #4: Book is in progress in ABS → handle reading sessions appropriately
				// This includes both new reading sessions and re-read scenarios (handled by conditional sync logic above)
				debugLog("UPDATE: Book '%s' is in progress in ABS (%.2f%%) - creating/updating read entry", a.Title, a.Progress*100)
			}

			// Only create new user book read entry if the expectation logic determined we should
			if shouldCreateReadEntry {
				debugLog("Creating new user_book_read for '%s': %d seconds (%.2f%%)", a.Title, targetProgressSeconds, a.Progress*100)
				debugLog("DEBUG: existingEditionId value being passed to insertUserBookRead: %d", existingEditionId)
				// Use the enhanced insertUserBookRead function which includes reading_format_id for audiobooks
				// This ensures Hardcover recognizes it as an audiobook and doesn't ignore progress_seconds
				// CRITICAL FIX: Pass edition_id to prevent edition field from being null
				if err := insertUserBookRead(userBookId, targetProgressSeconds, a.Progress >= 0.99, existingEditionId); err != nil {
					return fmt.Errorf("failed to sync progress for '%s': %v", a.Title, err)
				}
			}

			debugLog("Successfully synced progress for '%s': %d seconds", a.Title, targetProgressSeconds)
		}
	}

	// Step 4: Update user book status if needed (after successful progress sync or skip)
	// This handles cases like changing from "Want to Read" (1) to "Read" (3) when books are finished
	if existingUserBookId > 0 && existingStatusId != targetStatusId {
		debugLog("Updating user book status for '%s': %d -> %d", a.Title, existingStatusId, targetStatusId)
		if err := updateUserBookStatus(existingUserBookId, targetStatusId); err != nil {
			debugLog("Warning: Failed to update status for '%s': %v", a.Title, err)
			// Don't fail the entire sync for status update issues, just log the warning
		} else {
			debugLog("Successfully updated status for '%s' from %d to %d", a.Title, existingStatusId, targetStatusId)
		}
	}

	return nil
}

// Use the shared SyncState from the types package
// type SyncState is now defined in types/sync_state.go

// Default concurrency settings
const (
	defaultBatchSize = 10
	defaultRPS       = 10
)

func runSync() {
	// Load or create sync state
	state := &types.SyncState{
		Version:           types.StateVersion,
		LastSync:          time.Now(),
		LastSyncTimestamp: time.Now().UnixMilli(),
		SyncCount:         0,
		SyncStatus:        "started",
		SyncMode:          "full", // Will be updated based on sync type
	}

	// Load configuration
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize HTTP client with configuration
	httpClient := httpclient.NewClient(cfg)


	// Initialize Hardcover batch client
	hcClient, err := hardcover.NewBatchClient(cfg, httpClient)
	if err != nil {
		log.Fatalf("Failed to initialize Hardcover client: %v", err)
	}

	// Configure client with concurrency settings
	hcClient = hcClient.WithBatchSize(types.DefaultBatchSize).
		WithWorkers(cfg.Concurrency.MaxWorkers).
		WithRateLimit(types.DefaultRPS)

	// Get all books from Audiobookshelf
	log.Println("Fetching books from Audiobookshelf...")
	abBooks, err := fetchAudiobookShelfStatsEnhanced()
	if err != nil {
		log.Fatalf("Failed to get books from Audiobookshelf: %v", err)
	}
	log.Printf("Found %d books in Audiobookshelf", len(abBooks))

	// Filter books to sync
	var booksToSync []*Audiobook
	for i := range abBooks {
		if shouldSyncBook(abBooks[i], cfg) {
			booksToSync = append(booksToSync, &abBooks[i])
		}
	}

	// Apply test filters if configured
	testBookFilter := os.Getenv("TEST_BOOK_FILTER")
	if testBookFilter != "" {
		var filteredBooks []*Audiobook
		originalCount := len(booksToSync)
		for _, book := range booksToSync {
			if strings.Contains(strings.ToLower(book.Title), strings.ToLower(testBookFilter)) {
				filteredBooks = append(filteredBooks, book)
			}
		}
		booksToSync = filteredBooks
		log.Printf("TEST_BOOK_FILTER: filtered from %d to %d books matching '%s'", originalCount, len(booksToSync), testBookFilter)
	} else {
		debugLog("No TEST_BOOK_FILTER configured, skipping filtering")
	}

	testBookLimitStr := os.Getenv("TEST_BOOK_LIMIT")
	testBookLimit := 0
	if testBookLimitStr != "" {
		var err error
		testBookLimit, err = strconv.Atoi(testBookLimitStr)
		if err != nil {
			log.Printf("Invalid TEST_BOOK_LIMIT value '%s', using 0 (no limit)", testBookLimitStr)
			testBookLimit = 0
		}
	}

	if testBookLimit > 0 && len(booksToSync) > testBookLimit {
		debugLog("Applying TEST_BOOK_LIMIT: %d", testBookLimit)
		booksToSync = booksToSync[:testBookLimit]
		log.Printf("TEST_BOOK_LIMIT: limited to %d books", testBookLimit)
	}

	log.Printf("Found %d books to sync after filtering", len(booksToSync))

	// Filter books by configured progress threshold
	minProgress := getMinimumProgressThreshold()
	syncWantToRead := getSyncWantToRead()

	var filteredBooks []*Audiobook
	for _, book := range booksToSync {
		// Skip books with no progress tracking
		if book.Progress <= 0 {
			continue
		}

		// Calculate progress percentage
		progress := 0.0
		if book.TotalDuration > 0 {
			progress = float64(book.CurrentTime) / float64(book.TotalDuration)
		}

		// Allow books with 0% progress if SYNC_WANT_TO_READ is enabled
		shouldSync := progress >= minProgress || (progress == 0 && syncWantToRead)

		if shouldSync {
			filteredBooks = append(filteredBooks, book)
			if progress == 0 && syncWantToRead {
				debugLog("Including book '%s' with 0%% progress for 'Want to read' sync", book.Title)
			}
		} else {
			// Enhanced detection for books that may be actually finished
			// Some audiobooks might show lower progress due to credits/silence at the end
			if progress >= 0.95 && book.TotalDuration > 0 {
				actualProgress := float64(book.CurrentTime) / float64(book.TotalDuration)
				if actualProgress >= minProgress {
					filteredBooks = append(filteredBooks, book)
					debugLog("Book '%s' has better calculated progress (%.6f) - adding to sync", book.Title, actualProgress)
				} else {
					debugLog("Skipping book '%s' with progress %.6f (< %.2f%%)", book.Title, progress, minProgress*100)
				}
			} else {
				debugLog("Skipping book '%s' with progress %.6f (< %.2f%%)", book.Title, progress, minProgress*100)
			}
		}
	}

	booksToSync = filteredBooks

	log.Printf("Found %d books with progress to sync (out of %d total books)", len(booksToSync), len(abBooks))

	if len(booksToSync) == 0 {
		log.Println("No books to sync after filtering by progress")
		// Update sync state even if no books to sync
		state.LastSync = time.Now()
		if err := saveSyncState(state); err != nil {
			log.Printf("Warning: Failed to save sync state: %v", err)
		}
		return
	}

	// Process books concurrently using worker pool
	startTime := time.Now()
	successCount, errorCount := ProcessBooks(booksToSync, cfg.GetConcurrentWorkers())

	// Log sync statistics
	duration := time.Since(startTime)
	booksPerSecond := float64(len(booksToSync)) / duration.Seconds()
	log.Printf("Processed %d books in %s (%.2f books/sec)",
		len(booksToSync), duration.Round(time.Millisecond), booksPerSecond)

	if errorCount > 0 {
		log.Printf("Completed with %d errors (%.1f%% success rate)",
			errorCount, float64(successCount)/float64(len(booksToSync))*100)
	}

	// Update sync state with current timestamp
	state.LastSync = time.Now()
	state.SyncCount += len(booksToSync)

	// Update sync state with completion status
	state.SyncStatus = "completed"
	state.LastSync = time.Now()
	state.LastSyncTimestamp = time.Now().UnixMilli()

	// Save sync state
	err = saveSyncState(state)
	if err != nil {
		log.Printf("Error saving sync state: %v", err)
	} else {
		debugLog("Sync completed successfully. Processed %d books (%d synced, %d failed)",
			state.BooksProcessed, state.BooksSynced, state.BooksFailed)
	}

	// Print sync summary
	log.Printf("Sync complete: %d/%d books synced successfully (%s)", successCount, len(booksToSync), state.SyncMode)

	// Log final cache statistics
	stats := getCacheStats()
	log.Printf("[CACHE] Final cache stats: %d total entries (%d authors, %d narrators, %d publishers)",
		stats["total_entries"].(int), stats["authors"].(int), stats["narrators"].(int), stats["publishers"].(int))

	// Print summary of books that may need manual review
	printMismatchSummary()

	// Save mismatches to JSON file if enabled
	if err := saveMismatchesJSONFile(); err != nil {
		log.Printf("Warning: Failed to save mismatches to JSON file: %v", err)
	}
}

// getCurrentUserID fetches the current user's ID from Hardcover
func getCurrentUserID() (string, error) {
	query := `
	query Me {
	  me {
		id
		username
	  }
	}`

	payload := map[string]interface{}{"query": query}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	debugLog("Hardcover me response: %s", string(body))

	// Try different response structures
	var result1 struct {
		Data struct {
			Me struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"me"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result1); err == nil && result1.Data.Me.ID != "" {
		return result1.Data.Me.ID, nil
	}

	// Alternative structure with array
	var result2 struct {
		Data struct {
			Me []struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"me"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result2); err == nil && len(result2.Data.Me) > 0 {
		return result2.Data.Me[0].ID, nil
	}

	return "", fmt.Errorf("failed to parse user ID from response: %s", string(body))
}
