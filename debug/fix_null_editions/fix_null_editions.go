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

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func getHardcoverToken() string {
	return os.Getenv("HARDCOVER_TOKEN")
}

func main() {
	fmt.Println("=== EDITION ID FIX SCRIPT ===")
	fmt.Println("Finding and fixing user_book_read entries with null edition_id...")
	
	// First, find all problematic entries
	entries, err := findProblematicEntries()
	if err != nil {
		fmt.Printf("Error finding entries: %v\n", err)
		return
	}
	
	fmt.Printf("Found %d user_book_read entries with null edition_id that need fixing\n", len(entries))
	
	if len(entries) == 0 {
		fmt.Println("No entries need fixing!")
		return
	}
	
	// Ask for confirmation
	fmt.Print("Do you want to fix these entries? (y/N): ")
	var response string
	fmt.Scanln(&response)
	
	if response != "y" && response != "Y" {
		fmt.Println("Aborted by user")
		return
	}
	
	// Fix each entry
	fixed := 0
	for _, entry := range entries {
		fmt.Printf("Fixing user_book_read ID %d (%s) - setting edition_id to %d\n", 
			entry.ID, entry.BookTitle, entry.CorrectEditionID)
		
		if err := fixUserBookRead(entry.ID, entry.CorrectEditionID); err != nil {
			fmt.Printf("  ❌ Error fixing ID %d: %v\n", entry.ID, err)
		} else {
			fmt.Printf("  ✅ Fixed!\n")
			fixed++
		}
		
		// Add delay to avoid rate limiting
		time.Sleep(1 * time.Second)
	}
	
	fmt.Printf("\n=== SUMMARY ===\n")
	fmt.Printf("Total entries found: %d\n", len(entries))
	fmt.Printf("Successfully fixed: %d\n", fixed)
	fmt.Printf("Failed: %d\n", len(entries)-fixed)
}

type ProblematicEntry struct {
	ID               int    `json:"id"`
	BookTitle        string `json:"book_title"`
	UserBookID       int    `json:"user_book_id"`
	CorrectEditionID int    `json:"correct_edition_id"`
}

func findProblematicEntries() ([]ProblematicEntry, error) {
	query := `
	query FindNullEditions {
	  user_book_reads(
		where: {
		  edition_id: {_is_null: true}
		  user_book: {
			user: {username: {_eq: "patricezoeb"}}
			edition_id: {_is_null: false}
		  }
		}
		order_by: {id: desc}
	  ) {
		id
		user_book_id
		user_book {
		  edition_id
		  book {
			title
		  }
		}
	  }
	}`
	
	payload := map[string]interface{}{
		"query": query,
	}
	
	payloadBytes, _ := json.Marshal(payload)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		return nil, fmt.Errorf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}
	
	var result struct {
		Data struct {
			UserBookReads []struct {
				ID         int `json:"id"`
				UserBookID int `json:"user_book_id"`
				UserBook   struct {
					EditionID *int `json:"edition_id"`
					Book      struct {
						Title string `json:"title"`
					} `json:"book"`
				} `json:"user_book"`
			} `json:"user_book_reads"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}
	
	var entries []ProblematicEntry
	for _, ubr := range result.Data.UserBookReads {
		if ubr.UserBook.EditionID != nil {
			entries = append(entries, ProblematicEntry{
				ID:               ubr.ID,
				BookTitle:        ubr.UserBook.Book.Title,
				UserBookID:       ubr.UserBookID,
				CorrectEditionID: *ubr.UserBook.EditionID,
			})
		}
	}
	
	return entries, nil
}

func fixUserBookRead(userBookReadID, editionID int) error {
	mutation := `
	mutation FixEditionID($id: Int!, $object: DatesReadInput!) {
	  update_user_book_read(id: $id, object: $object) {
		id
		error
		user_book_read {
		  id
		  edition_id
		}
	  }
	}`
	
	updateObject := map[string]interface{}{
		"edition_id": editionID,
	}
	
	variables := map[string]interface{}{
		"id":     userBookReadID,
		"object": updateObject,
	}
	
	payload := map[string]interface{}{
		"query":     mutation,
		"variables": variables,
	}
	
	payloadBytes, _ := json.Marshal(payload)
	
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
		return fmt.Errorf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}
	
	var result struct {
		Data struct {
			UpdateUserBookRead struct {
				ID           int     `json:"id"`
				Error        *string `json:"error"`
				UserBookRead *struct {
					ID        int  `json:"id"`
					EditionID *int `json:"edition_id"`
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
		return fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if result.Data.UpdateUserBookRead.Error != nil {
		return fmt.Errorf("update error: %s", *result.Data.UpdateUserBookRead.Error)
	}
	
	return nil
}
