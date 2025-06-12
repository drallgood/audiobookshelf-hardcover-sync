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

// Entry represents a NULL edition_id entry that needs fixing
type Entry struct {
	UserBookReadID int  `json:"user_book_read_id"`
	UserBookID     int  `json:"user_book_id"`
	EditionID      *int `json:"edition_id"`
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

func findNullEditionEntries() ([]Entry, error) {
	currentUser, err := getCurrentUser()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %v", err)
	}

	// Find all user_book_read entries where edition_id is null but user_book.edition_id is not null
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
				ID       int `json:"id"`
				UserBookID int `json:"user_book_id"`
				UserBook struct {
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
				UserBookReadID: ubr.ID,
				UserBookID:     ubr.UserBookID,
				EditionID:      ubr.UserBook.EditionID,
			})
		}
	}

	return entries, nil
}

func fixNullEditionEntry(entry Entry) error {
	mutation := `
	mutation UpdateUserBookRead($id: Int!, $object: DatesReadInput!) {
	  update_user_book_read(id: $id, object: $object) {
		id
		error
		user_book_read {
		  id
		  edition_id
		}
	  }
	}`

	variables := map[string]interface{}{
		"id": entry.UserBookReadID,
		"object": map[string]interface{}{
			"edition_id": *entry.EditionID,
		},
	}

	payload := map[string]interface{}{
		"query":     mutation,
		"variables": variables,
	}

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
					ID        int  `json:"id"`
					EditionID *int `json:"edition_id"`
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

	// Verify the fix worked
	if result.Data.UpdateUserBookRead.UserBookRead.EditionID == nil {
		return fmt.Errorf("failed to set edition_id - still null after update")
	}

	fmt.Printf("✅ Fixed user_book_read ID %d: set edition_id to %d\n", 
		entry.UserBookReadID, *result.Data.UpdateUserBookRead.UserBookRead.EditionID)
	
	return nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("Usage: go run debug/fix_all_null_editions.go [--dry-run]")
		fmt.Println("Fixes all user_book_read entries with NULL edition_id where user_book.edition_id is not null")
		fmt.Println("Options:")
		fmt.Println("  --dry-run  Show what would be fixed without making changes")
		os.Exit(0)
	}

	dryRun := len(os.Args) > 1 && os.Args[1] == "--dry-run"

	fmt.Println("🔍 Finding user_book_read entries with NULL edition_id...")
	entries, err := findNullEditionEntries()
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
		fmt.Printf("  %d. user_book_read ID %d (user_book_id: %d) -> edition_id: %d\n", 
			i+1, entry.UserBookReadID, entry.UserBookID, *entry.EditionID)
	}

	if dryRun {
		fmt.Println("\n🔍 DRY RUN MODE: No changes will be made")
		fmt.Printf("To actually fix these entries, run: go run debug/fix_all_null_editions.go\n")
		os.Exit(0)
	}

	fmt.Printf("\n🔧 Fixing %d entries...\n", len(entries))
	
	fixed := 0
	failed := 0
	
	for _, entry := range entries {
		if err := fixNullEditionEntry(entry); err != nil {
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
		fmt.Printf("\n🎉 All NULL edition_id entries have been successfully fixed!\n")
	}
}
