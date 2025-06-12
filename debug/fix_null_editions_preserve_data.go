package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Entry represents a user_book_read entry with all its current data
type Entry struct {
	UserBookReadID  int     `json:"user_book_read_id"`
	UserBookID      int     `json:"user_book_id"`
	EditionID       *int    `json:"edition_id"`
	ProgressSeconds *int    `json:"progress_seconds"`
	StartedAt       *string `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
}

func getHardcoverToken() string {
	token := os.Getenv("HARDCOVER_TOKEN")
	if token == "" {
		fmt.Println("Error: HARDCOVER_TOKEN environment variable not set")
		os.Exit(1)
	}
	return token
}

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

	resp, err := http.DefaultClient.Do(req)
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

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse getCurrentUser response: %v", err)
	}

	if len(result.Data.Me) == 0 {
		return "", fmt.Errorf("no user data returned from me query")
	}

	return result.Data.Me[0].Username, nil
}

func findNullEditionEntriesWithData() ([]Entry, error) {
	currentUser, err := getCurrentUser()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %v", err)
	}

	// Find all user_book_read entries where edition_id is null but user_book.edition_id is not null
	// Include all current data to preserve it during update
	query := `
	query FindNullEditionEntries($username: citext!) {
	  user_book_reads(
		where: {
		  edition_id: { _is_null: true }
		  user_book: { 
			edition_id: { _is_null: false }
			user: { username: { _eq: $username } }
		  }
		}
		order_by: { id: asc }
	  ) {
		id
		user_book_id
		progress_seconds
		started_at
		finished_at
		user_book {
		  edition_id
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := http.DefaultClient.Do(req)
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
			UserBookReads []struct {
				ID              int     `json:"id"`
				UserBookID      int     `json:"user_book_id"`
				ProgressSeconds *int    `json:"progress_seconds"`
				StartedAt       *string `json:"started_at"`
				FinishedAt      *string `json:"finished_at"`
				UserBook        struct {
					EditionID *int `json:"edition_id"`
				} `json:"user_book"`
			} `json:"user_book_reads"`
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

	var entries []Entry
	for _, ubr := range result.Data.UserBookReads {
		if ubr.UserBook.EditionID != nil {
			entries = append(entries, Entry{
				UserBookReadID:  ubr.ID,
				UserBookID:      ubr.UserBookID,
				EditionID:       ubr.UserBook.EditionID,
				ProgressSeconds: ubr.ProgressSeconds,
				StartedAt:       ubr.StartedAt,
				FinishedAt:      ubr.FinishedAt,
			})
		}
	}

	return entries, nil
}

func fixNullEditionEntryPreservingData(entry Entry) error {
	mutation := `
	mutation UpdateUserBookRead($id: Int!, $object: DatesReadInput!) {
	  update_user_book_read(id: $id, object: $object) {
		id
		error
		user_book_read {
		  id
		  edition_id
		  progress_seconds
		  started_at
		  finished_at
		}
	  }
	}`

	// Build the update object with all existing data plus the corrected edition_id
	updateObject := map[string]interface{}{
		"edition_id": *entry.EditionID,
	}

	// Preserve existing progress_seconds if it exists
	if entry.ProgressSeconds != nil {
		updateObject["progress_seconds"] = *entry.ProgressSeconds
	}

	// Preserve existing started_at if it exists
	if entry.StartedAt != nil {
		updateObject["started_at"] = *entry.StartedAt
	}

	// Preserve existing finished_at if it exists
	if entry.FinishedAt != nil {
		updateObject["finished_at"] = *entry.FinishedAt
	}

	variables := map[string]interface{}{
		"id":     entry.UserBookReadID,
		"object": updateObject,
	}

	payload := map[string]interface{}{
		"query":     mutation,
		"variables": variables,
	}

	fmt.Printf("🔧 Fixing user_book_read ID %d: preserving progress_seconds=%v, started_at=%v, finished_at=%v, setting edition_id=%d\n",
		entry.UserBookReadID, entry.ProgressSeconds, entry.StartedAt, entry.FinishedAt, *entry.EditionID)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal mutation: %v", err)
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			UpdateUserBookRead struct {
				ID    int     `json:"id"`
				Error *string `json:"error"`
				UserBookRead struct {
					ID              int     `json:"id"`
					EditionID       *int    `json:"edition_id"`
					ProgressSeconds *int    `json:"progress_seconds"`
					StartedAt       *string `json:"started_at"`
					FinishedAt      *string `json:"finished_at"`
				} `json:"user_book_read"`
			} `json:"update_user_book_read"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if result.Data.UpdateUserBookRead.Error != nil {
		return fmt.Errorf("update error: %s", *result.Data.UpdateUserBookRead.Error)
	}

	// Verify the fix worked and data was preserved
	updated := result.Data.UpdateUserBookRead.UserBookRead
	if updated.EditionID == nil {
		return fmt.Errorf("failed to set edition_id - still null after update")
	}

	fmt.Printf("✅ Fixed user_book_read ID %d: edition_id=%d, progress_seconds=%v, started_at=%v, finished_at=%v\n",
		updated.ID, *updated.EditionID, updated.ProgressSeconds, updated.StartedAt, updated.FinishedAt)

	return nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("Usage: go run debug/fix_null_editions_preserve_data.go [--dry-run]")
		fmt.Println("Fixes all user_book_read entries with NULL edition_id while preserving existing data")
		fmt.Println("Options:")
		fmt.Println("  --dry-run  Show what would be fixed without making changes")
		os.Exit(0)
	}

	dryRun := len(os.Args) > 1 && os.Args[1] == "--dry-run"

	fmt.Println("🔍 Finding user_book_read entries with NULL edition_id...")
	entries, err := findNullEditionEntriesWithData()
	if err != nil {
		fmt.Printf("Error finding entries: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Println("✅ No NULL edition_id entries found - all entries are already fixed!")
		os.Exit(0)
	}

	fmt.Printf("📋 Found %d entries with NULL edition_id that need fixing:\n", len(entries))
	for i, entry := range entries {
		progressStr := "nil"
		if entry.ProgressSeconds != nil {
			progressStr = fmt.Sprintf("%d", *entry.ProgressSeconds)
		}
		
		startedStr := "nil"
		if entry.StartedAt != nil {
			startedStr = *entry.StartedAt
		}
		
		finishedStr := "nil"  
		if entry.FinishedAt != nil {
			finishedStr = *entry.FinishedAt
		}
		
		fmt.Printf("  %d. user_book_read ID %d -> edition_id: %d (current: progress_seconds=%s, started_at=%s, finished_at=%s)\n",
			i+1, entry.UserBookReadID, *entry.EditionID, progressStr, startedStr, finishedStr)
	}

	if dryRun {
		fmt.Println("\n🔍 DRY RUN MODE: No changes will be made")
		fmt.Printf("To actually fix these entries, run: go run debug/fix_null_editions_preserve_data.go\n")
		os.Exit(0)
	}

	fmt.Printf("\n🔧 Fixing %d entries while preserving existing data...\n", len(entries))

	fixed := 0
	failed := 0

	for _, entry := range entries {
		if err := fixNullEditionEntryPreservingData(entry); err != nil {
			fmt.Printf("❌ Failed to fix user_book_read ID %d: %v\n", entry.UserBookReadID, err)
			failed++
		} else {
			fixed++
		}

		// Rate limiting - wait 1 second between requests
		time.Sleep(1 * time.Second)
	}

	fmt.Printf("\n📊 Results:\n")
	fmt.Printf("  ✅ Fixed: %d entries\n", fixed)
	fmt.Printf("  ❌ Failed: %d entries\n", failed)
	fmt.Printf("  📝 Total: %d entries\n", len(entries))

	if failed > 0 {
		fmt.Printf("\n⚠️  Some entries failed to update. You may need to run this script again.\n")
		os.Exit(1)
	} else {
		fmt.Printf("\n🎉 All NULL edition_id entries have been successfully fixed with data preserved!\n")
	}
}
