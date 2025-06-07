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
	"strconv"
	"time"
)

// syncToHardcover syncs a single audiobook to Hardcover with comprehensive book matching
// and conditional sync logic to avoid unnecessary API calls
func syncToHardcover(a Audiobook) error {
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
					ID       json.Number `json:"id"`
					Editions []struct {
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
			bookId = result.Data.Books[0].ID.String()
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
					ID       json.Number `json:"id"`
					Editions []struct {
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
			bookId = result.Data.Books[0].ID.String()
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
					ID       json.Number `json:"id"`
					Editions []struct {
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
			bookId = result.Data.Books[0].ID.String()
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
				addBookMismatchWithMetadata(a.Metadata, "", "", reason, a.TotalDuration)

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

			addBookMismatchWithMetadata(a.Metadata, bookId, editionId, reason, a.TotalDuration)

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
			addBookMismatchWithMetadata(a.Metadata, bookId, editionId, reason, a.TotalDuration)

			debugLog("To change this behavior, set AUDIOBOOK_MATCH_MODE=skip or AUDIOBOOK_MATCH_MODE=fail")
		}
	}

	// Step 2: Check if user already has this book and compare status/progress
	existingUserBookId, existingStatusId, existingProgressSeconds, existingOwned, existingEditionId, err := checkExistingUserBook(bookId)
	if err != nil {
		return fmt.Errorf("failed to check existing user book for '%s': %v", a.Title, err)
	}

	// Determine the target status for this book
	targetStatusId := 3 // default to read
	if a.Progress == 0 && getSyncWantToRead() {
		targetStatusId = 1 // want to read
	} else if a.Progress < 0.99 {
		targetStatusId = 2 // currently reading
	}

	// Calculate target progress in seconds
	var targetProgressSeconds int
	if a.Progress > 0 {
		if a.CurrentTime > 0 {
			targetProgressSeconds = int(math.Round(a.CurrentTime))
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
			// EXPECTATION #4: Book has finished read in Hardcover but shows in-progress in AudiobookShelf
			// This is a re-read scenario - always sync to create new reading session
			needsSync = true
			debugLog("RE-READ: Book '%s' has finished read in Hardcover (finished on %s) but shows %.2f%% progress in AudiobookShelf - syncing as new reading session",
				a.Title, finishDate, a.Progress*100)
		} else {
			// Normal sync logic for other scenarios
			statusChanged := existingStatusId != targetStatusId

			// Consider progress changed if the difference is significant (more than 30 seconds or 10%)
			progressThreshold := int(math.Max(30, float64(targetProgressSeconds)*0.1))
			progressChanged := targetProgressSeconds > 0 &&
				(existingProgressSeconds == 0 ||
					math.Abs(float64(targetProgressSeconds-existingProgressSeconds)) > float64(progressThreshold))

			// Check if owned flag needs updating
			targetOwned := getSyncOwned()
			ownedChanged := targetOwned != existingOwned

			if statusChanged || progressChanged {
				needsSync = true
				debugLog("Book '%s' needs update - status changed: %t (%d->%d), progress changed: %t (%ds->%ds)",
					a.Title, statusChanged, existingStatusId, targetStatusId, progressChanged, existingProgressSeconds, targetProgressSeconds)
			} else if ownedChanged {
				// Handle owned flag change separately
				if targetOwned && !existingOwned && existingEditionId > 0 {
					// Need to mark as owned and we have an edition ID
					debugLog("Book '%s' status/progress up-to-date but needs to be marked as owned (edition_id: %d)",
						a.Title, existingEditionId)
					if err := markBookAsOwned(fmt.Sprintf("%d", existingEditionId)); err != nil {
						debugLog("Failed to mark book '%s' as owned: %v", a.Title, err)
					} else {
						debugLog("Successfully marked book '%s' as owned", a.Title)
					}
				} else if targetOwned && !existingOwned && existingEditionId == 0 {
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
		if getSyncOwned() {
			userBookInput["owned"] = true
		}
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
		payloadBytes, _ := json.Marshal(payload)
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
			if resp.StatusCode != 200 {
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
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(body, &result); err != nil {
				continue
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
		existingReadId, existingProgressSeconds, err := checkExistingUserBookRead(userBookId, today)
		if err != nil {
			return fmt.Errorf("failed to check existing user book read for '%s': %v", a.Title, err)
		}

		if existingReadId > 0 {
			// Update existing user_book_read if progress has changed
			if existingProgressSeconds != targetProgressSeconds {
				debugLog("Updating existing user_book_read id=%d for '%s': progressSeconds=%d -> %d", existingReadId, a.Title, existingProgressSeconds, targetProgressSeconds)
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

				// CRITICAL FIX: Always set started_at to prevent null values from wiping out reading history
				// If the book is finished, also set finished_at
				updateObject := map[string]interface{}{
					"progress_seconds": targetProgressSeconds,
					"started_at":       time.Now().Format("2006-01-02"), // Ensure started_at is never null
				}

				// If book is finished (>= 99%), also set finished_at
				if a.Progress >= 0.99 {
					updateObject["finished_at"] = time.Now().Format("2006-01-02")
				}

				variables := map[string]interface{}{
					"id":     existingReadId,
					"object": updateObject,
				}
				payload := map[string]interface{}{
					"query":     updateMutation,
					"variables": variables,
				}
				payloadBytes, err := json.Marshal(payload)
				if err != nil {
					return fmt.Errorf("failed to marshal update_user_book_read payload: %v", err)
				}
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

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to read response: %v", err)
				}

				debugLog("update_user_book_read response: %s", string(body))

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

				if len(result.Errors) > 0 {
					return fmt.Errorf("graphql errors: %v", result.Errors)
				}

				if result.Data.UpdateUserBookRead.Error != nil {
					return fmt.Errorf("update error: %s", *result.Data.UpdateUserBookRead.Error)
				}

				debugLog("Successfully updated user_book_read with id: %d", result.Data.UpdateUserBookRead.ID)
				if result.Data.UpdateUserBookRead.UserBookRead != nil {
					debugLog("Confirmed progress update: %d seconds", result.Data.UpdateUserBookRead.UserBookRead.ProgressSeconds)
				}
			} else {
				debugLog("No update needed for existing user_book_read id=%d (progress already %d seconds)", existingReadId, existingProgressSeconds)
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
				// Use the enhanced insertUserBookRead function which includes reading_format_id for audiobooks
				// This ensures Hardcover recognizes it as an audiobook and doesn't ignore progress_seconds
				if err := insertUserBookRead(userBookId, targetProgressSeconds, a.Progress >= 0.99); err != nil {
					return fmt.Errorf("failed to sync progress for '%s': %v", a.Title, err)
				}
			}

			debugLog("Successfully synced progress for '%s': %d seconds", a.Title, targetProgressSeconds)
		}
	}
	return nil
}

// runSync orchestrates the complete sync process with support for incremental sync:
// 1. Loads sync state and determines sync mode (full vs incremental)
// 2. Fetches audiobooks from AudiobookShelf (full library or recent changes)
// 3. Filters books based on progress thresholds and incremental criteria
// 4. Attempts enhanced detection for potentially finished books
// 5. Syncs each qualifying book to Hardcover with rate limiting
// 6. Updates and saves sync state
// 7. Reports sync results and potential mismatches
func runSync() {
	// Clear cached user at start of each sync run to ensure fresh authentication
	clearCurrentUserCache()

	// Load sync state to determine sync mode
	state, err := loadSyncState()
	if err != nil {
		log.Printf("Warning: Failed to load sync state, performing full sync: %v", err)
		state = &SyncState{
			LastSyncTimestamp: 0,
			LastFullSync:      0,
			Version:           StateVersion,
		}
	}

	// Determine if we should perform full or incremental sync
	isFullSync := shouldPerformFullSync(state)
	syncMode := "full"
	if !isFullSync {
		syncMode = "incremental"
	}

	log.Printf("Starting %s sync (incremental mode: %s)", syncMode, getIncrementalSyncMode())

	var books []Audiobook
	var recentItemIds []string

	if isFullSync {
		// Full sync: fetch all audiobooks
		books, err = fetchAudiobookShelfStats()
		if err != nil {
			log.Printf("Failed to fetch library items: %v", err)
			return
		}
		debugLog("Full sync: fetched %d total books", len(books))
	} else {
		// Incremental sync: fetch recent changes first, then filter audiobooks
		timestampThreshold := getTimestampThreshold(state)
		debugLog("Incremental sync: looking for changes since timestamp %d", timestampThreshold)

		recentItemIds, err = fetchRecentListeningSessions(timestampThreshold)
		if err != nil {
			log.Printf("Failed to fetch recent listening sessions, falling back to full sync: %v", err)
			// Fallback to full sync
			books, err = fetchAudiobookShelfStats()
			if err != nil {
				log.Printf("Failed to fetch library items: %v", err)
				return
			}
			isFullSync = true
			syncMode = "full (fallback)"
			debugLog("Fallback to full sync: fetched %d total books", len(books))
		} else {
			debugLog("Incremental sync: found %d items with recent activity", len(recentItemIds))

			if len(recentItemIds) == 0 {
				log.Printf("No recent activity found since last sync - nothing to sync")
				// Update timestamp even if no changes found
				updateSyncTimestamp(state, false)
				if err := saveSyncState(state); err != nil {
					log.Printf("Warning: Failed to save sync state: %v", err)
				}
				return
			}

			// Fetch all audiobooks and filter to only recently updated ones
			allBooks, err := fetchAudiobookShelfStats()
			if err != nil {
				log.Printf("Failed to fetch library items: %v", err)
				return
			}
			// Create a map for quick lookup of recent item IDs
			recentItemMap := make(map[string]bool)
			for _, itemId := range recentItemIds {
				recentItemMap[itemId] = true
			}

			// Filter books to only include those with recent activity
			for _, book := range allBooks {
				if recentItemMap[book.ID] {
					books = append(books, book)
				}
			}

			debugLog("Incremental sync: filtered to %d books with recent activity (from %d total books)", len(books), len(allBooks))
		}
	}

	// Clear any previous mismatches before starting new sync
	clearMismatches()

	// Filter books by configured progress threshold
	minProgress := getMinimumProgressThreshold()
	syncWantToRead := getSyncWantToRead()
	var booksToSync []Audiobook

	for _, book := range books {
		// Allow books with 0% progress if SYNC_WANT_TO_READ is enabled
		shouldSync := book.Progress >= minProgress || (book.Progress == 0 && syncWantToRead)

		if shouldSync {
			booksToSync = append(booksToSync, book)
			if book.Progress == 0 && syncWantToRead {
				debugLog("Including book '%s' with 0%% progress for 'Want to read' sync", book.Title)
			}
		} else {
			// Enhanced detection for books that may be actually finished
			// Some audiobooks might show lower progress due to credits/silence at the end
			if book.Progress >= 0.95 && book.TotalDuration > 0 {
				actualProgress := float64(book.CurrentTime) / book.TotalDuration
				if actualProgress >= minProgress {
					booksToSync = append(booksToSync, book)
					debugLog("Book '%s' has better calculated progress (%.6f) - adding to sync", book.Title, actualProgress)
				} else {
					debugLog("Skipping book '%s' with progress %.6f (< %.2f%%)", book.Title, book.Progress, minProgress*100)
				}
			} else {
				debugLog("Skipping book '%s' with progress %.6f (< %.2f%%)", book.Title, book.Progress, minProgress*100)
			}
		}
	}

	log.Printf("Found %d books with progress to sync (out of %d candidate books)", len(booksToSync), len(books))

	if len(booksToSync) == 0 {
		log.Printf("No books with progress found to sync")
		// Update sync state even if no books to sync
		updateSyncTimestamp(state, isFullSync)
		if err := saveSyncState(state); err != nil {
			log.Printf("Warning: Failed to save sync state: %v", err)
		}
		return
	}

	// Sync books to Hardcover with rate limiting
	delay := getHardcoverSyncDelay()
	successCount := 0
	for i, book := range booksToSync {
		if i > 0 {
			time.Sleep(delay)
		}
		if err := syncToHardcover(book); err != nil {
			log.Printf("Failed to sync book '%s' (Progress: %.2f%%): %v", book.Title, book.Progress*100, err)
		} else {
			log.Printf("Synced book: %s (Progress: %.2f%%)", book.Title, book.Progress*100)
			successCount++
		}
	}

	// Update sync state after successful sync
	updateSyncTimestamp(state, isFullSync)
	if err := saveSyncState(state); err != nil {
		log.Printf("Warning: Failed to save sync state: %v", err)
	}

	// Print sync summary
	log.Printf("Sync complete: %d/%d books synced successfully (%s)", successCount, len(booksToSync), syncMode)

	// Print summary of books that may need manual review
	printMismatchSummary()
}
