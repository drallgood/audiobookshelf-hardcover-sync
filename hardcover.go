package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Lookup Hardcover bookId by title and author using the books(filter: {title, author}) GraphQL query
func lookupHardcoverBookID(title, author string) (string, error) {
	// Try original title first
	id, err := lookupHardcoverBookIDRaw(title, author)
	if err == nil {
		return id, nil
	}
	// Fallback: try normalized title
	normTitle := normalizeTitle(title)
	if normTitle != title {
		debugLog("Retrying Hardcover lookup with normalized title: '%s'", normTitle)
		id, err2 := lookupHardcoverBookIDRaw(normTitle, author)
		if err2 == nil {
			return id, nil
		}
	}
	return "", err
}

// Raw lookup (no normalization)
func lookupHardcoverBookIDRaw(title, author string) (string, error) {
	query := `
	query BooksByTitleAuthor($title: String!, $author: String!) {
	  books(where: {title: {_eq: $title}, contributions: {author: {name: {_eq: $author}}}}) {
		id
		title
		book_status_id
		canonical_id
		contributions {
		  author {
			name
		  }
		}
	  }
	}`
	variables := map[string]interface{}{
		"title":  title,
		"author": author,
	}
	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	payloadBytes, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
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
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("hardcover books query error: %s - %s", resp.Status, string(body))
	}
	var result struct {
		Data struct {
			Books []struct {
				ID            json.Number `json:"id"`
				Title         string      `json:"title"`
				BookStatusID  int         `json:"book_status_id"`
				CanonicalID   *int        `json:"canonical_id"`
				Contributions []struct {
					Author struct {
						Name string `json:"name"`
					} `json:"author"`
				} `json:"contributions"`
			} `json:"books"`
		} `json:"data"`
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	for _, book := range result.Data.Books {
		if strings.EqualFold(book.Title, title) {
			// Check if any of the book's authors match the requested author
			for _, contribution := range book.Contributions {
				if strings.Contains(strings.ToLower(contribution.Author.Name), strings.ToLower(author)) {
					// Handle deduped books: use canonical_id if book_status_id = 4 (deduped)
					bookID := book.ID.String()
					if book.BookStatusID == 4 && book.CanonicalID != nil {
						bookID = fmt.Sprintf("%d", *book.CanonicalID)
						debugLog("Book ID %s is deduped (status 4), using canonical_id %d instead", book.ID.String(), *book.CanonicalID)
					}
					return bookID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no matching book found for '%s' by '%s'", title, author)
}

// checkExistingUserBook checks if the user already has this book in their Hardcover library
// Returns: userBookId (0 if not found), currentStatusId, currentProgressSeconds, existingOwned, editionId (0 if not found), error
func checkExistingUserBook(bookId string) (int, int, int, bool, int, error) {
	// Get current user to ensure we only query their data
	currentUser, err := getCurrentUser()
	if err != nil {
		return 0, 0, 0, false, 0, fmt.Errorf("failed to get current user: %v", err)
	}

	// First try to find user books with reads, explicitly filtered by user
	query := `
	query CheckUserBook($bookId: Int!, $username: citext!) {
	  user_books(
		where: { 
		  book_id: { _eq: $bookId }
		  user: { username: { _eq: $username } }
		  user_book_reads: { id: { _is_null: false } }
		}, 
		order_by: { id: desc }, 
		limit: 1
	  ) {
		id
		status_id
		book_id
		edition_id
		owned
		user_book_reads(order_by: { started_at: desc }, limit: 1) {
		  progress_seconds
		  finished_at
		}
	  }
	}`

	variables := map[string]interface{}{
		"bookId":   toInt(bookId),
		"username": currentUser,
	}
	payload := map[string]interface{}{"query": query, "variables": variables}
	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, 0, 0, false, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, 0, false, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, 0, false, 0, fmt.Errorf("hardcover user_books query error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			UserBooks []struct {
				ID            int  `json:"id"`
				StatusID      int  `json:"status_id"`
				BookID        int  `json:"book_id"`
				EditionID     *int `json:"edition_id"`
				Owned         bool `json:"owned"`
				UserBookReads []struct {
					ProgressSeconds *int    `json:"progress_seconds"`
					FinishedAt      *string `json:"finished_at"`
				} `json:"user_book_reads"`
			} `json:"user_books"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0, false, 0, err
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, 0, false, 0, err
	}

	// If no user book with reads found, try fallback query for any user book
	if len(result.Data.UserBooks) == 0 {
		debugLog("No user book with reads found for bookId=%s, trying fallback query", bookId)

		fallbackQuery := `
		query CheckUserBookFallback($bookId: Int!, $username: citext!) {
		  user_books(
			where: { 
			  book_id: { _eq: $bookId }
			  user: { username: { _eq: $username } }
			}, 
			order_by: { id: desc }, 
			limit: 1
		  ) {
			id
			status_id
			book_id
			edition_id
			owned
			user_book_reads(order_by: { started_at: desc }, limit: 1) {
			  progress_seconds
			  finished_at
			}
		  }
		}`

		fallbackVariables := map[string]interface{}{
			"bookId":   toInt(bookId),
			"username": currentUser,
		}
		fallbackPayload := map[string]interface{}{"query": fallbackQuery, "variables": fallbackVariables}
		fallbackPayloadBytes, _ := json.Marshal(fallbackPayload)

		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()

		req2, err := http.NewRequestWithContext(ctx2, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(fallbackPayloadBytes))
		if err != nil {
			return 0, 0, 0, false, 0, err
		}
		req2.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

		resp2, err := httpClient.Do(req2)
		if err != nil {
			return 0, 0, 0, false, 0, err
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != 200 {
			body2, _ := io.ReadAll(resp2.Body)
			return 0, 0, 0, false, 0, fmt.Errorf("hardcover user_books fallback query error: %s - %s", resp2.Status, string(body2))
		}

		body2, err := io.ReadAll(resp2.Body)
		if err != nil {
			return 0, 0, 0, false, 0, err
		}

		if err := json.Unmarshal(body2, &result); err != nil {
			return 0, 0, 0, false, 0, err
		}
	}

	// If no user book found, return 0s to indicate we need to create it
	if len(result.Data.UserBooks) == 0 {
		debugLog("No existing user book found for bookId=%s", bookId)
		return 0, 0, 0, false, 0, nil
	}

	userBook := result.Data.UserBooks[0]
	userBookId := userBook.ID
	currentStatusId := userBook.StatusID
	currentOwned := userBook.Owned
	currentProgressSeconds := 0
	currentEditionId := 0

	// Get edition ID if available
	if userBook.EditionID != nil {
		currentEditionId = *userBook.EditionID
	}

	// Get the most recent progress if available
	if len(userBook.UserBookReads) > 0 && userBook.UserBookReads[0].ProgressSeconds != nil {
		currentProgressSeconds = *userBook.UserBookReads[0].ProgressSeconds
	}

	debugLog("Found existing user book: userBookId=%d, statusId=%d, progressSeconds=%d, owned=%t, editionId=%d",
		userBookId, currentStatusId, currentProgressSeconds, currentOwned, currentEditionId)

	return userBookId, currentStatusId, currentProgressSeconds, currentOwned, currentEditionId, nil
}

// checkExistingUserBookRead checks if a user_book_read already exists for the given user_book_id that isn't finished
// Returns: existingReadId (0 if not found), existingProgressSeconds, existingStartedAt, error
func checkExistingUserBookRead(userBookID int, targetDate string) (int, int, string, error) {
	// Get current user to ensure we only query their data
	currentUser, err := getCurrentUser()
	if err != nil {
		return 0, 0, "", fmt.Errorf("failed to get current user: %v", err)
	}

	query := `
	query CheckUserBookRead($userBookId: Int!, $username: citext!) {
	  user_book_reads(where: { 
		user_book_id: { _eq: $userBookId },
		finished_at: { _is_null: true },
		started_at: { _is_null: false },
		user_book: { user: { username: { _eq: $username } } }
	  }, order_by: { started_at: desc }, limit: 1) {
		id
		progress_seconds
		started_at
		finished_at
	  }
	}`

	variables := map[string]interface{}{
		"userBookId": userBookID,
		"username":   currentUser,
	}
	payload := map[string]interface{}{"query": query, "variables": variables}
	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, "", fmt.Errorf("hardcover user_book_reads query error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			UserBookReads []struct {
				ID              int     `json:"id"`
				ProgressSeconds *int    `json:"progress_seconds"`
				StartedAt       *string `json:"started_at"`
				FinishedAt      *string `json:"finished_at"`
			} `json:"user_book_reads"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, "", err
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, "", err
	}

	// If no user book read found for this book, try fallback query for entries with null started_at
	if len(result.Data.UserBookReads) == 0 {
		debugLog("No unfinished user_book_read with valid started_at found for userBookId=%d, trying fallback", userBookID)

		// Fallback query to find ANY unfinished reads (including those with null started_at)
		fallbackQuery := `
		query CheckUserBookReadFallback($userBookId: Int!, $username: citext!) {
		  user_book_reads(where: { 
			user_book_id: { _eq: $userBookId },
			finished_at: { _is_null: true },
			user_book: { user: { username: { _eq: $username } } }
		  }, order_by: { id: desc }, limit: 1) {
			id
			progress_seconds
			started_at
			finished_at
		  }
		}`

		fallbackPayload := map[string]interface{}{"query": fallbackQuery, "variables": variables}
		fallbackPayloadBytes, _ := json.Marshal(fallbackPayload)

		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()

		req2, err := http.NewRequestWithContext(ctx2, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(fallbackPayloadBytes))
		if err != nil {
			return 0, 0, "", err
		}
		req2.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

		resp2, err := httpClient.Do(req2)
		if err != nil {
			return 0, 0, "", err
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != 200 {
			body2, _ := io.ReadAll(resp2.Body)
			return 0, 0, "", fmt.Errorf("hardcover fallback user_book_reads query error: %s - %s", resp2.Status, string(body2))
		}

		body2, err := io.ReadAll(resp2.Body)
		if err != nil {
			return 0, 0, "", err
		}

		if err := json.Unmarshal(body2, &result); err != nil {
			return 0, 0, "", err
		}

		if len(result.Data.UserBookReads) == 0 {
			debugLog("No existing unfinished user_book_read found at all for userBookId=%d", userBookID)
			return 0, 0, "", nil
		}

		debugLog("Found unfinished user_book_read via fallback query for userBookId=%d", userBookID)
	}

	// Debug: log all found entries to understand selection logic
	debugLog("Found %d unfinished user_book_read entries for userBookId=%d:", len(result.Data.UserBookReads), userBookID)
	for i, read := range result.Data.UserBookReads {
		startedAtStr := "null"
		if read.StartedAt != nil {
			startedAtStr = *read.StartedAt
		}
		progressSeconds := 0
		if read.ProgressSeconds != nil {
			progressSeconds = *read.ProgressSeconds
		}
		debugLog("  Entry %d: id=%d, progressSeconds=%d, startedAt=%s", i+1, read.ID, progressSeconds, startedAtStr)
	}

	userBookRead := result.Data.UserBookReads[0]
	existingReadId := userBookRead.ID
	existingProgressSeconds := 0
	existingStartedAt := ""

	if userBookRead.ProgressSeconds != nil {
		existingProgressSeconds = *userBookRead.ProgressSeconds
	}

	if userBookRead.StartedAt != nil {
		existingStartedAt = *userBookRead.StartedAt
	}

	startedAtStr := "null"
	if userBookRead.StartedAt != nil {
		startedAtStr = *userBookRead.StartedAt
	}

	debugLog("Selected user_book_read: id=%d, progressSeconds=%d, startedAt=%s",
		existingReadId, existingProgressSeconds, startedAtStr)

	return existingReadId, existingProgressSeconds, existingStartedAt, nil
}

// checkExistingFinishedRead checks if ANY user_book_read with status "read" (finished) already exists
// for the given user_book_id to prevent duplicate finished reads entirely
func checkExistingFinishedRead(userBookID int) (bool, string, error) {
	// Get current user to ensure we only query their data
	currentUser, err := getCurrentUser()
	if err != nil {
		return false, "", fmt.Errorf("failed to get current user: %v", err)
	}

	query := `
	query CheckExistingFinishedRead($userBookId: Int!, $username: citext!) {
	  user_book_reads(
		where: {
		  user_book_id: { _eq: $userBookId }
		  finished_at: { _is_null: false }
		  user_book: { user: { username: { _eq: $username } } }
		}
		order_by: { finished_at: desc }
		limit: 1
	  ) {
		id
		finished_at
	  }
	}`

	variables := map[string]interface{}{
		"userBookId": userBookID,
		"username":   currentUser,
	}

	requestBody := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return false, "", fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(jsonBody))
	if err != nil {
		return false, "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("request failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", fmt.Errorf("failed to read response: %v", err)
	}

	debugLog("checkExistingFinishedRead response: %s", string(body))

	var result struct {
		Data struct {
			UserBookReads []struct {
				ID         int    `json:"id"`
				FinishedAt string `json:"finished_at"`
			} `json:"user_book_reads"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return false, "", fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return false, "", fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if len(result.Data.UserBookReads) > 0 {
		finishedAt := result.Data.UserBookReads[0].FinishedAt
		debugLog("Found existing finished read for user_book_id %d: finished on %s", userBookID, finishedAt)
		return true, finishedAt, nil
	}

	debugLog("No existing finished read found for user_book_id %d", userBookID)
	return false, "", nil
}

// insertUserBookRead is a function that uses the insert_user_book_read mutation
// to sync progress to Hardcover
func insertUserBookRead(userBookID int, progressSeconds int, isFinished bool) error {
	// Prepare the input for the mutation
	userBookRead := map[string]interface{}{
		"progress_seconds":  progressSeconds,
		"reading_format_id": 2, // Audiobook format
	}

	// Set dates based on completion status
	now := time.Now().Format("2006-01-02")
	userBookRead["started_at"] = now

	if isFinished {
		userBookRead["finished_at"] = now
	}

	// Use the direct insert_user_book_read mutation which works more reliably
	insertMutation := `
	mutation InsertUserBookRead($user_book_id: Int!, $user_book_read: DatesReadInput!) {
	  insert_user_book_read(user_book_id: $user_book_id, user_book_read: $user_book_read) {
		id
		error
	  }
	}`

	variables := map[string]interface{}{
		"user_book_id":   userBookID,
		"user_book_read": userBookRead,
	}

	payload := map[string]interface{}{
		"query":     insertMutation,
		"variables": variables,
	}

	debugLog("=== GraphQL Mutation: insert_user_book_read ===")
	debugLog("Mutation variables: %+v", variables)
	debugLog("Full mutation payload: %+v", payload)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal insert_user_book_read payload: %v", err)
	}

	debugLog("Mutation request body: %s", string(payloadBytes))

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

	debugLog("insert_user_book_read response: %s", string(body))

	var result struct {
		Data struct {
			InsertUserBookRead struct {
				ID    int     `json:"id"`
				Error *string `json:"error"`
			} `json:"insert_user_book_read"`
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

	if result.Data.InsertUserBookRead.Error != nil {
		return fmt.Errorf("insert error: %s", *result.Data.InsertUserBookRead.Error)
	}

	debugLog("Successfully inserted user_book_read with id: %d", result.Data.InsertUserBookRead.ID)
	return nil
}

// updateUserBookStatus updates the status of a user book in Hardcover
// This is used to change book status from "Want to Read" (1) to "Read" (3) when books are finished
func updateUserBookStatus(userBookID int, statusID int) error {
	mutation := `
	mutation UpdateUserBookStatus($id: Int!, $object: UserBookUpdateInput!) {
	  update_user_book(id: $id, object: $object) {
		id
		error
		user_book {
		  id
		  status_id
		}
	  }
	}`

	variables := map[string]interface{}{
		"id": userBookID,
		"object": map[string]interface{}{
			"status_id": statusID,
		},
	}

	payload := map[string]interface{}{
		"query":     mutation,
		"variables": variables,
	}

	debugLog("=== GraphQL Mutation: update_user_book (status) ===")
	debugLog("User Book ID: %d, New Status ID: %d", userBookID, statusID)
	debugLog("Mutation variables: %+v", variables)
	debugLog("Full mutation payload: %+v", payload)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal update_user_book payload: %v", err)
	}

	debugLog("update_user_book request body: %s", string(payloadBytes))

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

	debugLog("update_user_book response: %s", string(body))

	var result struct {
		Data struct {
			UpdateUserBook struct {
				ID       int     `json:"id"`
				Error    *string `json:"error"`
				UserBook *struct {
					ID       int `json:"id"`
					StatusID int `json:"status_id"`
				} `json:"user_book"`
			} `json:"update_user_book"`
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

	if result.Data.UpdateUserBook.Error != nil {
		return fmt.Errorf("update error: %s", *result.Data.UpdateUserBook.Error)
	}

	debugLog("Successfully updated user_book status with id: %d", result.Data.UpdateUserBook.ID)
	if result.Data.UpdateUserBook.UserBook != nil {
		debugLog("Confirmed status update - ID: %d, Status ID: %d", 
			result.Data.UpdateUserBook.UserBook.ID, 
			result.Data.UpdateUserBook.UserBook.StatusID)
	}
	return nil
}

// markBookAsOwned marks a book as owned in Hardcover using the edition_owned mutation
// This works with edition IDs, not book IDs
func markBookAsOwned(editionId string) error {
	if editionId == "" {
		return fmt.Errorf("edition ID is required for marking book as owned")
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
		"id": toInt(editionId),
	}

	payload := map[string]interface{}{
		"query":     mutation,
		"variables": variables,
	}

	debugLog("=== GraphQL Mutation: edition_owned ===")
	debugLog("Edition ID: %s (converted to int: %d)", editionId, toInt(editionId))
	debugLog("Mutation variables: %+v", variables)
	debugLog("Full mutation payload: %+v", payload)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal edition_owned payload: %v", err)
	}

	debugLog("Mutation request body: %s", string(payloadBytes))

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

	debugLog("edition_owned response status: %d %s", resp.StatusCode, resp.Status)
	debugLog("edition_owned response headers: %+v", resp.Header)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}

	debugLog("edition_owned raw response: %s", string(body))

	if resp.StatusCode != 200 {
		return fmt.Errorf("edition_owned mutation error: %s - %s", resp.Status, string(body))
	}

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

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		debugLog("GraphQL errors in edition_owned: %+v", result.Errors)
		return fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	debugLog("edition_owned result data: %+v", result.Data)

	if result.Data.Ownership.ID == nil {
		debugLog("Book with edition_id=%s was not marked as owned (may already be owned)", editionId)
	} else {
		debugLog("Successfully marked book as owned: edition_id=%s, list_book_id=%d", editionId, *result.Data.Ownership.ID)
		if result.Data.Ownership.ListBook != nil {
			debugLog("Ownership details - ListBook ID: %d, Book ID: %d, Edition ID: %d",
				result.Data.Ownership.ListBook.ID,
				result.Data.Ownership.ListBook.BookID,
				result.Data.Ownership.ListBook.EditionID)
		}
	}

	return nil
}

// OwnedBook represents a book in the user's "Owned" list
type OwnedBook struct {
	ListBookID int    `json:"list_book_id"`
	BookID     int    `json:"book_id"`
	EditionID  *int   `json:"edition_id"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	ImageURL   string `json:"image_url"`
	ISBN10     string `json:"isbn_10"`
	ISBN13     string `json:"isbn_13"`
	DateAdded  string `json:"date_added"`
}

// getOwnedBooks retrieves all books marked as owned by querying the user's "Owned" list
// This is the correct way to get owned books in Hardcover - NOT through user_books.owned field
func getOwnedBooks() ([]OwnedBook, error) {
	currentUser, err := getCurrentUser()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %v", err)
	}

	// Query to get the user's "Owned" list and all books in it
	query := `
	query GetOwnedBooks($username: citext!) {
	  lists(
		where: {
		  user: { username: { _eq: $username } }
		  name: { _eq: "Owned" }
		}
	  ) {
		id
		name
		books_count
		list_books {
		  id
		  book_id
		  edition_id
		  date_added
		  book {
			id
			title
			isbn_10
			isbn_13
			image {
			  url
			}
			contributions {
			  author {
				name
			  }
			}
		  }
		  edition {
			id
			title
			isbn_10
			isbn_13
			image {
			  url
			}
		  }
		}
	  }
	}`

	variables := map[string]interface{}{
		"username": currentUser,
	}

	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			Lists []struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				BooksCount int    `json:"books_count"`
				ListBooks  []struct {
					ID        int    `json:"id"`
					BookID    int    `json:"book_id"`
					EditionID *int   `json:"edition_id"`
					DateAdded string `json:"date_added"`
					Book      struct {
						ID    int    `json:"id"`
						Title string `json:"title"`
						Image *struct {
							URL string `json:"url"`
						} `json:"image"`
						ISBN10        string `json:"isbn_10"`
						ISBN13        string `json:"isbn_13"`
						Contributions []struct {
							Author struct {
								Name string `json:"name"`
							} `json:"author"`
						} `json:"contributions"`
					} `json:"book"`
					Edition *struct {
						ID     int    `json:"id"`
						Title  string `json:"title"`
						ISBN10 string `json:"isbn_10"`
						ISBN13 string `json:"isbn_13"`
						Image  *struct {
							URL string `json:"url"`
						} `json:"image"`
					} `json:"edition"`
				} `json:"list_books"`
			} `json:"lists"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if len(result.Data.Lists) == 0 {
		debugLog("No 'Owned' list found for user %s", currentUser)
		return []OwnedBook{}, nil
	}

	ownedList := result.Data.Lists[0]
	debugLog("Found 'Owned' list (ID: %d) with %d books", ownedList.ID, ownedList.BooksCount)

	var ownedBooks []OwnedBook
	for _, listBook := range ownedList.ListBooks {
		// Get primary author
		var author string
		if len(listBook.Book.Contributions) > 0 {
			author = listBook.Book.Contributions[0].Author.Name
		}

		// Prefer edition info if available, otherwise use book info
		title := listBook.Book.Title
		var imageURL string
		if listBook.Book.Image != nil {
			imageURL = listBook.Book.Image.URL
		}
		isbn10 := listBook.Book.ISBN10
		isbn13 := listBook.Book.ISBN13

		if listBook.Edition != nil {
			if listBook.Edition.Title != "" {
				title = listBook.Edition.Title
			}
			if listBook.Edition.Image != nil && listBook.Edition.Image.URL != "" {
				imageURL = listBook.Edition.Image.URL
			}
			if listBook.Edition.ISBN10 != "" {
				isbn10 = listBook.Edition.ISBN10
			}
			if listBook.Edition.ISBN13 != "" {
				isbn13 = listBook.Edition.ISBN13
			}
		}

		ownedBook := OwnedBook{
			ListBookID: listBook.ID,
			BookID:     listBook.BookID,
			EditionID:  listBook.EditionID,
			Title:      title,
			Author:     author,
			ImageURL:   imageURL,
			ISBN10:     isbn10,
			ISBN13:     isbn13,
			DateAdded:  listBook.DateAdded,
		}

		ownedBooks = append(ownedBooks, ownedBook)
	}

	debugLog("Retrieved %d owned books from Hardcover", len(ownedBooks))
	return ownedBooks, nil
}

// isBookOwnedDirect checks if a specific book is in the user's owned list
// Returns ownership status, list_book_id, and error
func isBookOwnedDirect(bookID int) (bool, int, error) {
	currentUser, err := getCurrentUser()
	if err != nil {
		return false, 0, fmt.Errorf("failed to get current user: %v", err)
	}

	query := `
	query CheckBookOwnership($username: citext!, $bookId: Int!) {
	  lists(
		where: {
		  user: { username: { _eq: $username } }
		  name: { _eq: "Owned" }
		  list_books: { book_id: { _eq: $bookId } }
		}
	  ) {
		id
		list_books(where: { book_id: { _eq: $bookId } }) {
		  id
		  book_id
		  edition_id
		}
	  }
	}`

	variables := map[string]interface{}{
		"username": currentUser,
		"bookId":   bookID,
	}

	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return false, 0, fmt.Errorf("failed to marshal query: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return false, 0, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, 0, fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return false, 0, fmt.Errorf("API error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			Lists []struct {
				ID        int `json:"id"`
				ListBooks []struct {
					ID        int  `json:"id"`
					BookID    int  `json:"book_id"`
					EditionID *int `json:"edition_id"`
				} `json:"list_books"`
			} `json:"lists"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, 0, fmt.Errorf("failed to read response: %v", err)
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return false, 0, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return false, 0, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	// If we have a list with list_books, the book is owned
	if len(result.Data.Lists) > 0 && len(result.Data.Lists[0].ListBooks) > 0 {
		listBookID := result.Data.Lists[0].ListBooks[0].ID
		return true, listBookID, nil
	}

	return false, 0, nil // Not owned
}

// getCurrentUser gets the current authenticated user's information
// This function caches the result to avoid repeated API calls during a sync run
func getCurrentUser() (string, error) {
	return getCurrentUserCached()
}

// getCurrentUserCached returns cached user or fetches and caches it
func getCurrentUserCached() (string, error) {
	// Return cached user if already fetched
	if cachedCurrentUser != "" {
		return cachedCurrentUser, nil
	}

	// Fetch user from API
	user, err := fetchCurrentUserFromAPI()
	if err != nil {
		return "", err
	}

	// Cache the result
	cachedCurrentUser = user
	debugLog("Cached current user: %s", user)
	return user, nil
}

// clearCurrentUserCache clears the cached current user (useful for testing or fresh sync runs)
func clearCurrentUserCache() {
	cachedCurrentUser = ""
	debugLog("Cleared current user cache")
}

// fetchCurrentUserFromAPI does the actual API call to get current user
func fetchCurrentUserFromAPI() (string, error) {
	query := `
	query GetCurrentUser {
	  me {
		username
	  }
	}`

	payload := map[string]interface{}{"query": query}
	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
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

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("hardcover me query error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			Me []struct {
				Username string `json:"username"`
			} `json:"me"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	debugLog("getCurrentUser response: %s", string(body))

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse getCurrentUser response: %v", err)
	}

	if len(result.Data.Me) == 0 {
		return "", fmt.Errorf("no user data returned from me query")
	}

	return result.Data.Me[0].Username, nil
}

// BookMetadataResponse represents detailed book information from Hardcover
type BookMetadataResponse struct {
	ID            json.Number        `json:"id"`
	Title         string             `json:"title"`
	Subtitle      string             `json:"subtitle"`
	ReleaseDate   string             `json:"release_date"`
	AudioSeconds  int                `json:"audio_seconds"`
	Image         *ImageInfo         `json:"image"`
	Contributions []ContributionInfo `json:"contributions"`
	Editions      []EditionInfo      `json:"editions"`
}

type ImageInfo struct {
	URL string `json:"url"`
}

type ContributionInfo struct {
	Role   string     `json:"contribution"`
	Author AuthorInfo `json:"author"`
}

type AuthorInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type EditionInfo struct {
	ID           json.Number   `json:"id"`
	Title        string        `json:"title"`
	Subtitle     string        `json:"subtitle"`
	ASIN         string        `json:"asin"`
	ISBN10       string        `json:"isbn_10"`
	ISBN13       string        `json:"isbn_13"`
	AudioSeconds int           `json:"audio_seconds"`
	ReleaseDate  string        `json:"release_date"`
	PublisherID  int           `json:"publisher_id"`
	Publisher    PublisherInfo `json:"publisher"`
	Image        *ImageInfo    `json:"image"`
}

type PublisherInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// getDetailedBookMetadata fetches comprehensive book metadata from Hardcover for edition creation prepopulation
func getDetailedBookMetadata(bookID string) (*BookMetadataResponse, error) {
	query := `
	query BookDetailedMetadata($bookId: Int!) {
	  books(where: { id: { _eq: $bookId } }, limit: 1) {
		id
		title
		subtitle
		release_date
		audio_seconds
		image {
		  url
		}
		contributions {
		  contribution
		  author {
			id
			name
		  }
		}
		editions {
		  id
		  title
		  subtitle
		  asin
		  isbn_10
		  isbn_13
		  audio_seconds
		  release_date
		  publisher_id
		  publisher {
			id
			name
		  }
		  image {
			url
		  }
		}
	  }
	}`

	bookIDInt := 0
	if _, err := fmt.Sscanf(bookID, "%d", &bookIDInt); err != nil {
		return nil, fmt.Errorf("invalid book ID format: %s", bookID)
	}

	variables := map[string]interface{}{
		"bookId": bookIDInt,
	}

	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hardcover detailed metadata query error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			Books []BookMetadataResponse `json:"books"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	debugLog("Detailed book metadata response: %s", string(body))

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %v", result.Errors)
	}

	if len(result.Data.Books) == 0 {
		return nil, fmt.Errorf("book with ID %s not found", bookID)
	}

	book := result.Data.Books[0]
	debugLog("Found book metadata: title='%s', subtitle='%s', editions=%d, contributions=%d",
		book.Title, book.Subtitle, len(book.Editions), len(book.Contributions))

	return &book, nil
}

// extractAuthorIDs extracts author IDs from contributions with role="author"
func extractAuthorIDs(contributions []ContributionInfo) []int {
	authorIDs := []int{}
	for _, contrib := range contributions {
		if strings.EqualFold(contrib.Role, "author") {
			authorIDs = append(authorIDs, contrib.Author.ID)
		}
	}
	return authorIDs
}

// extractNarratorIDs extracts narrator IDs from contributions with role="narrator"
func extractNarratorIDs(contributions []ContributionInfo) []int {
	narratorIDs := []int{}
	for _, contrib := range contributions {
		if strings.EqualFold(contrib.Role, "narrator") {
			narratorIDs = append(narratorIDs, contrib.Author.ID)
		}
	}
	return narratorIDs
}

// searchPersonIDs uses Hardcover's search API to find person IDs by name and type
// makeHardcoverRequest is a helper function to make GraphQL requests to Hardcover API
func makeHardcoverRequest(query string, variables map[string]interface{}) ([]byte, error) {
	// Rate limiting: Hardcover API is limited to 60 requests per minute
	// Add a delay between requests to avoid 429 errors
	time.Sleep(1200 * time.Millisecond) // 1.2 seconds = 50 requests/minute (safe margin)

	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Handle rate limiting with exponential backoff
	if resp.StatusCode == 429 {
		debugLog("Rate limited (429), waiting 60 seconds before retry...")
		time.Sleep(60 * time.Second)
		
		// Retry the request once
		resp2, err2 := httpClient.Do(req)
		if err2 != nil {
			return nil, fmt.Errorf("retry request failed: %v", err2)
		}
		defer resp2.Body.Close()
		resp = resp2
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GraphQL query error: %s - %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	debugLog("GraphQL response: %s", string(body))
	return body, nil
}

func searchPersonIDs(name, personType string, limit int) ([]int, error) {
	query := `
	query SearchPeople($query: String!, $queryType: String, $perPage: Int) {
		search(
			query: $query
			query_type: $queryType
			per_page: $perPage
		) {
			error
			ids
			query_type
		}
	}`

	variables := map[string]interface{}{
		"query":     name,
		"queryType": personType,
		"perPage":   limit,
	}

	resp, err := makeHardcoverRequest(query, variables)
	if err != nil {
		return nil, fmt.Errorf("search API request failed: %v", err)
	}

	var searchResp SearchAPIResponse
	if err := json.Unmarshal(resp, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %v", err)
	}

	if len(searchResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", searchResp.Errors)
	}

	if searchResp.Data.Search.Error != nil {
		return nil, fmt.Errorf("search API error: %s", *searchResp.Data.Search.Error)
	}

	// Convert json.Number to int
	var ids []int
	for _, jsonNum := range searchResp.Data.Search.IDs {
		id, err := jsonNum.Int64()
		if err != nil {
			return nil, fmt.Errorf("failed to convert ID to integer: %v", err)
		}
		ids = append(ids, int(id))
	}

	return ids, nil
}

// searchAuthors searches for authors by name using the search API
func searchAuthors(name string, limit int) ([]PersonSearchResult, error) {
	// First, use the search API to get author IDs
	ids, err := searchPersonIDs(name, "author", limit)
	if err != nil {
		return nil, fmt.Errorf("author search failed: %v", err)
	}

	if len(ids) == 0 {
		return []PersonSearchResult{}, nil
	}

	// Then fetch full details for those IDs
	query := `
	query GetAuthorsByIDs($ids: [Int!]!) {
		authors(
			where: {
				id: { _in: $ids }
			}
		) {
			id
			name
			books_count
			bio
			canonical_id
		}
	}`

	variables := map[string]interface{}{
		"ids": ids,
	}

	result, err := executePersonSearch(query, variables)
	if err != nil {
		return nil, fmt.Errorf("author details fetch failed: %v", err)
	}

	// Add canonical flag and sort to match the search result order
	for i := range result {
		result[i].IsCanonical = result[i].CanonicalID == nil
	}

	// Sort to match the search result order
	sortedResult := make([]PersonSearchResult, 0, len(result))
	for _, id := range ids {
		for _, person := range result {
			if person.ID == id {
				sortedResult = append(sortedResult, person)
				break
			}
		}
	}

	return sortedResult, nil
}

// searchNarrators searches for narrators by name using the search API
func searchNarrators(name string, limit int) ([]PersonSearchResult, error) {
	// First, use the search API to get narrator IDs
	ids, err := searchPersonIDs(name, "narrator", limit)
	if err != nil {
		return nil, fmt.Errorf("narrator search failed: %v", err)
	}

	if len(ids) == 0 {
		return []PersonSearchResult{}, nil
	}

	// Then fetch full details for those IDs, filtering for those with narrator contributions
	query := `
	query GetNarratorsByIDs($ids: [Int!]!) {
		authors(
			where: {
				id: { _in: $ids }
				contributions: {
					contribution: { _eq: "narrator" }
				}
			}
		) {
			id
			name
			books_count
			bio
			canonical_id
		}
	}`

	variables := map[string]interface{}{
		"ids": ids,
	}

	result, err := executePersonSearch(query, variables)
	if err != nil {
		return nil, fmt.Errorf("narrator details fetch failed: %v", err)
	}

	// Add canonical flag and sort to match the search result order
	for i := range result {
		result[i].IsCanonical = result[i].CanonicalID == nil
	}

	// Sort to match the search result order
	sortedResult := make([]PersonSearchResult, 0, len(result))
	for _, id := range ids {
		for _, person := range result {
			if person.ID == id {
				sortedResult = append(sortedResult, person)
				break
			}
		}
	}

	return sortedResult, nil
}

// searchPublishers searches for publishers by name using the search API
func searchPublishers(name string, limit int) ([]PublisherSearchResult, error) {
	// First, use the search API to get publisher IDs
	ids, err := searchPersonIDs(name, "publisher", limit)
	if err != nil {
		return nil, fmt.Errorf("publisher search failed: %v", err)
	}

	if len(ids) == 0 {
		return []PublisherSearchResult{}, nil
	}

	// Then fetch full details for those IDs
	query := `
	query GetPublishersByIDs($ids: [bigint!]!) {
		publishers(
			where: {
				id: { _in: $ids }
			}
		) {
			id
			name
			editions_count
			canonical_id
		}
	}`

	variables := map[string]interface{}{
		"ids": ids,
	}

	resp, err := makeHardcoverRequest(query, variables)
	if err != nil {
		return nil, fmt.Errorf("publisher details fetch failed: %v", err)
	}

	var result PublisherSearchResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	// Add canonical flag and sort to match the search result order
	for i := range result.Data.Publishers {
		result.Data.Publishers[i].IsCanonical = result.Data.Publishers[i].CanonicalID == nil
	}

	// Sort to match the search result order
	sortedResult := make([]PublisherSearchResult, 0, len(result.Data.Publishers))
	for _, id := range ids {
		for _, publisher := range result.Data.Publishers {
			if publisher.ID == id {
				sortedResult = append(sortedResult, publisher)
				break
			}
		}
	}

	return sortedResult, nil
}

// executePersonSearch is a helper function for author/narrator searches
func executePersonSearch(query string, variables map[string]interface{}) ([]PersonSearchResult, error) {
	resp, err := makeHardcoverRequest(query, variables)
	if err != nil {
		return nil, fmt.Errorf("person search request failed: %v", err)
	}

	var result PersonSearchResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	return result.Data.Authors, nil
}

// getPersonByID retrieves a specific person (author/narrator) by ID
func getPersonByID(id int) (*PersonSearchResult, error) {
	query := `
	query GetPersonByID($id: Int!) {
		authors(where: { id: { _eq: $id } }) {
			id
			name
			books_count
			bio
			canonical_id
		}
	}`

	variables := map[string]interface{}{
		"id": id,
	}

	result, err := executePersonSearch(query, variables)
	if err != nil {
		return nil, fmt.Errorf("person lookup failed: %v", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("person with ID %d not found", id)
	}

	person := result[0]
	person.IsCanonical = person.CanonicalID == nil
	return &person, nil
}

// getPublisherByID retrieves a specific publisher by ID
func getPublisherByID(id int) (*PublisherSearchResult, error) {
	query := `
	query GetPublisherByID($id: bigint!) {
		publishers(where: { id: { _eq: $id } }) {
			id
			name
			editions_count
			canonical_id
		}
	}`

	variables := map[string]interface{}{
		"id": id,
	}

	resp, err := makeHardcoverRequest(query, variables)
	if err != nil {
		return nil, fmt.Errorf("publisher lookup request failed: %v", err)
	}

	var result PublisherSearchResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if len(result.Data.Publishers) == 0 {
		return nil, fmt.Errorf("publisher with ID %d not found", id)
	}

	publisher := result.Data.Publishers[0]
	publisher.IsCanonical = publisher.CanonicalID == nil
	return &publisher, nil
}

// uploadImageToHardcover uploads an image from URL to Hardcover
func uploadImageToHardcover(imageURL string, bookID int) (int, error) {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would upload image URL: %s for book ID: %d", imageURL, bookID)
		return 999999, nil // Return a fake ID for dry run
	}

	mutation := `
	mutation InsertImage($image: ImageInput!) {
		insert_image(image: $image) {
			id
			image {
				id
				url
				imageable_id
				imageable_type
			}
		}
	}`

	variables := map[string]interface{}{
		"image": map[string]interface{}{
			"url":            imageURL,
			"imageable_type": "Book",
			"imageable_id":   bookID,
		},
	}

	resp, err := makeHardcoverRequest(mutation, variables)
	if err != nil {
		return 0, fmt.Errorf("image upload request failed: %v", err)
	}

	var result struct {
		Data struct {
			InsertImage struct {
				ID    int `json:"id"`
				Image struct {
					ID            int    `json:"id"`
					URL           string `json:"url"`
					ImageableID   int    `json:"imageable_id"`
					ImageableType string `json:"imageable_type"`
				} `json:"image"`
			} `json:"insert_image"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return 0, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if result.Data.InsertImage.Image.ID == 0 {
		return 0, fmt.Errorf("image upload failed: no ID returned")
	}

	return result.Data.InsertImage.Image.ID, nil
}

// isHardcoverAssetURL checks if the image URL is already from Hardcover's asset domain
// Returns: isHardcoverAsset (bool), skipUpload (bool), error
func isHardcoverAssetURL(imageURL string) (bool, bool, error) {
	if imageURL == "" {
		return false, false, nil
	}

	// Check if URL is from Hardcover's exact assets domain
	if strings.HasPrefix(imageURL, "https://assets.hardcover.app/") || strings.HasPrefix(imageURL, "http://assets.hardcover.app/") {
		debugLog("Image URL is already from Hardcover assets domain: %s", imageURL)
		return true, true, nil
	}

	debugLog("Image URL is not from Hardcover assets domain, will upload: %s", imageURL)
	return false, false, nil
}

// diagNoseNullEdition queries a user_book_read entry to understand why edition might be null
func diagnoseNullEdition(userBookReadID int) {
	if !debugMode {
		return
	}

	debugLog("=== Diagnosing NULL Edition Issue for user_book_read ID: %d ===", userBookReadID)

	query := `
	query DiagnoseNullEdition($id: Int!) {
	  user_book_reads(where: { id: { _eq: $id } }) {
		id
		progress_seconds
		started_at
		finished_at
		user_book_id
		user_book {
		  id
		  book_id
		  edition_id
		  status_id
		  owned
		  book {
			id
			title
		  }
		  edition {
			id
			title
			isbn_10
			isbn_13
			asin
		  }
		}
	  }
	}`

	variables := map[string]interface{}{
		"id": userBookReadID,
	}
	payload := map[string]interface{}{"query": query, "variables": variables}
	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		debugLog("Failed to create diagnostic request: %v", err)
		return
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		debugLog("Failed to execute diagnostic query: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		debugLog("Failed to read diagnostic response: %v", err)
		return
	}

	debugLog("Diagnostic query response: %s", string(body))

	var result struct {
		Data struct {
			UserBookReads []struct {
				ID             int `json:"id"`
				ProgressSeconds *int `json:"progress_seconds"`
				StartedAt      *string `json:"started_at"`
				FinishedAt     *string `json:"finished_at"`
				UserBookID     int `json:"user_book_id"`
				UserBook       struct {
					ID        int  `json:"id"`
					BookID    int  `json:"book_id"`
					EditionID *int `json:"edition_id"`
					StatusID  int  `json:"status_id"`
					Owned     bool `json:"owned"`
					Book      struct {
						ID    int    `json:"id"`
						Title string `json:"title"`
					} `json:"book"`
					Edition *struct {
						ID     int     `json:"id"`
						Title  string  `json:"title"`
						ISBN10 *string `json:"isbn_10"`
						ISBN13 *string `json:"isbn_13"`
						ASIN   *string `json:"asin"`
					} `json:"edition"`
				} `json:"user_book"`
			} `json:"user_book_reads"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		debugLog("Failed to parse diagnostic response: %v", err)
		return
	}

	if len(result.Data.UserBookReads) == 0 {
		debugLog("No user_book_read found with ID %d", userBookReadID)
		return
	}

	read := result.Data.UserBookReads[0]
	userBook := read.UserBook

	debugLog("=== Diagnostic Results ===")
	debugLog("user_book_read ID: %d", read.ID)
	debugLog("user_book ID: %d", read.UserBookID)
	debugLog("book_id: %d (%s)", userBook.BookID, userBook.Book.Title)
	
	if userBook.EditionID != nil {
		debugLog("edition_id: %d", *userBook.EditionID)
		if userBook.Edition != nil {
			debugLog("edition found: %s", userBook.Edition.Title)
			if userBook.Edition.ASIN != nil {
				debugLog("edition ASIN: %s", *userBook.Edition.ASIN)
			}
		} else {
			debugLog("WARNING: edition_id is set (%d) but edition is NULL - possible orphaned reference", *userBook.EditionID)
		}
	} else {
		debugLog("WARNING: edition_id is NULL - this causes the edition field to be null in queries")
	}
	
	debugLog("status_id: %d, owned: %t", userBook.StatusID, userBook.Owned)
	debugLog("=== End Diagnostic ===")
}
