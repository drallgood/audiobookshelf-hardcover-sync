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
	// Find user_book_read entries with null edition_id
	query := `
	query FindNullEditions {
	  user_book_reads(
where: {
  edition_id: {_is_null: true}
  user_book: {user: {username: {_eq: "patricezoeb"}}}
}
limit: 5
order_by: {id: desc}
  ) {
		id
		progress_seconds
		edition_id
		user_book_id
		user_book {
		  id
		  book_id
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
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		return
	}
	
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
	
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to execute request: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Failed to read response: %v\n", err)
		return
	}
	
	fmt.Printf("Response: %s\n", string(body))
	
	var result struct {
		Data struct {
			UserBookReads []struct {
				ID            int  `json:"id"`
				ProgressSeconds int `json:"progress_seconds"`
				EditionID     *int `json:"edition_id"`
				UserBookID    int  `json:"user_book_id"`
				UserBook      struct {
					ID        int  `json:"id"`
					BookID    int  `json:"book_id"`
					EditionID *int `json:"edition_id"`
					Book      struct {
						Title string `json:"title"`
					} `json:"book"`
				} `json:"user_book"`
			} `json:"user_book_reads"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Failed to parse response: %v\n", err)
		return
	}

	fmt.Printf("\n=== FOUND %d USER_BOOK_READ ENTRIES WITH NULL EDITION_ID ===\n", len(result.Data.UserBookReads))
	
	for _, ubr := range result.Data.UserBookReads {
		fmt.Printf("\nuser_book_read ID: %d\n", ubr.ID)
		fmt.Printf("  Book: %s\n", ubr.UserBook.Book.Title)
		fmt.Printf("  user_book_read.edition_id: %v\n", ubr.EditionID)
		fmt.Printf("  user_book.edition_id: %v\n", ubr.UserBook.EditionID)
		
		if ubr.EditionID == nil && ubr.UserBook.EditionID != nil {
			fmt.Printf("  ❌ ISSUE: user_book_read.edition_id is NULL but user_book.edition_id is %d\n", *ubr.UserBook.EditionID)
		} else if ubr.EditionID == nil && ubr.UserBook.EditionID == nil {
			fmt.Printf("  ⚠️  BOTH NULL: Both edition_ids are null\n")
		}
	}
}
