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
					return book.ID.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("no matching book found for '%s' by '%s'", title, author)
}

// checkExistingUserBook checks if the user already has this book in their Hardcover library
// Returns: userBookId (0 if not found), currentStatusId, currentProgressSeconds, error
func checkExistingUserBook(bookId string) (int, int, int, error) {
	// Get current user to ensure we only query their data
	currentUser, err := getCurrentUser()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to get current user: %v", err)
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
		return 0, 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, 0, fmt.Errorf("hardcover user_books query error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			UserBooks []struct {
				ID            int `json:"id"`
				StatusID      int `json:"status_id"`
				BookID        int `json:"book_id"`
				UserBookReads []struct {
					ProgressSeconds *int    `json:"progress_seconds"`
					FinishedAt      *string `json:"finished_at"`
				} `json:"user_book_reads"`
			} `json:"user_books"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0, err
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, 0, err
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
			return 0, 0, 0, err
		}
		req2.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

		resp2, err := httpClient.Do(req2)
		if err != nil {
			return 0, 0, 0, err
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != 200 {
			body2, _ := io.ReadAll(resp2.Body)
			return 0, 0, 0, fmt.Errorf("hardcover user_books fallback query error: %s - %s", resp2.Status, string(body2))
		}

		body2, err := io.ReadAll(resp2.Body)
		if err != nil {
			return 0, 0, 0, err
		}

		if err := json.Unmarshal(body2, &result); err != nil {
			return 0, 0, 0, err
		}
	}

	// If no user book found, return 0s to indicate we need to create it
	if len(result.Data.UserBooks) == 0 {
		debugLog("No existing user book found for bookId=%s", bookId)
		return 0, 0, 0, nil
	}

	userBook := result.Data.UserBooks[0]
	userBookId := userBook.ID
	currentStatusId := userBook.StatusID
	currentProgressSeconds := 0

	// Get the most recent progress if available
	if len(userBook.UserBookReads) > 0 && userBook.UserBookReads[0].ProgressSeconds != nil {
		currentProgressSeconds = *userBook.UserBookReads[0].ProgressSeconds
	}

	debugLog("Found existing user book: userBookId=%d, statusId=%d, progressSeconds=%d",
		userBookId, currentStatusId, currentProgressSeconds)

	return userBookId, currentStatusId, currentProgressSeconds, nil
}

// checkExistingUserBookRead checks if a user_book_read already exists for the given user_book_id that isn't finished
// Returns: existingReadId (0 if not found), existingProgressSeconds, error
func checkExistingUserBookRead(userBookID int, targetDate string) (int, int, error) {
	// Get current user to ensure we only query their data
	currentUser, err := getCurrentUser()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get current user: %v", err)
	}

	query := `
	query CheckUserBookRead($userBookId: Int!, $username: citext!) {
	  user_book_reads(where: { 
		user_book_id: { _eq: $userBookId },
		finished_at: { _is_null: true },
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
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("hardcover user_book_reads query error: %s - %s", resp.Status, string(body))
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
		return 0, 0, err
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, err
	}

	// If no user book read found for this book, return 0s to indicate we need to create it
	if len(result.Data.UserBookReads) == 0 {
		debugLog("No existing unfinished user_book_read found for userBookId=%d", userBookID)
		return 0, 0, nil
	}

	userBookRead := result.Data.UserBookReads[0]
	existingReadId := userBookRead.ID
	existingProgressSeconds := 0

	if userBookRead.ProgressSeconds != nil {
		existingProgressSeconds = *userBookRead.ProgressSeconds
	}

	debugLog("Found existing unfinished user_book_read: id=%d, progressSeconds=%d, startedAt=%s",
		existingReadId, existingProgressSeconds, *userBookRead.StartedAt)

	return existingReadId, existingProgressSeconds, nil
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

	debugLog("Using insert_user_book_read with variables: %+v", variables)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal insert_user_book_read payload: %v", err)
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

// getCurrentUser gets the current authenticated user's information
func getCurrentUser() (string, error) {
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
