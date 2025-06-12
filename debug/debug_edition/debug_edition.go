package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func getHardcoverToken() string {
	return os.Getenv("HARDCOVER_TOKEN")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run debug_edition.go <user_book_read_id>")
		fmt.Println("   or: go run debug_edition.go userbook <user_book_id>")
		os.Exit(1)
	}

	if os.Args[1] == "userbook" && len(os.Args) >= 3 {
		userBookID, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Printf("Invalid user_book_id: %v\n", err)
			os.Exit(1)
		}
		
		fmt.Printf("Checking edition_id for user_book %d:\n", userBookID)
		actualEditionID := verifyUserBookEdition(userBookID)
		fmt.Printf("user_book %d has edition_id: %d\n", userBookID, actualEditionID)
		return
	}

	userBookReadID, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Printf("Invalid user_book_read_id: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Diagnosing user_book_read entry %d:\n", userBookReadID)
	diagnoseUserBookReadEdition(userBookReadID)
}

// diagnoseUserBookReadEdition analyzes a specific user_book_read entry to understand why edition_id is null
func diagnoseUserBookReadEdition(userBookReadID int) {
	fmt.Printf("=== DIAGNOSING USER_BOOK_READ ID %d ===\n", userBookReadID)
	
	query := `
	query DiagnoseUserBookRead($id: Int!) {
	  user_book_reads(where: {id: {_eq: $id}}) {
		id
		progress_seconds
		edition_id
		user_book_id
		user_book {
		  id
		  book_id
		  edition_id
		  edition {
			id
			title
			asin
		  }
		}
	  }
	}`
	
	variables := map[string]interface{}{
		"id": userBookReadID,
	}
	
	payload := map[string]interface{}{
		"query": query,
		"variables": variables,
	}
	
	payloadBytes, _ := json.Marshal(payload)
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		fmt.Printf("Failed to create diagnosis request: %v\n", err)
		return
	}
	
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
	
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to execute diagnosis request: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Failed to read diagnosis response: %v\n", err)
		return
	}
	
	fmt.Printf("Diagnosis response: %s\n", string(body))
	
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
					Edition   *struct {
						ID    int    `json:"id"`
						Title string `json:"title"`
						ASIN  string `json:"asin"`
					} `json:"edition"`
				} `json:"user_book"`
			} `json:"user_book_reads"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Failed to parse diagnosis response: %v\n", err)
		return
	}

	if len(result.Data.UserBookReads) == 0 {
		fmt.Printf("No user_book_read found with ID %d\n", userBookReadID)
		return
	}

	ubr := result.Data.UserBookReads[0]
	fmt.Printf("=== DIAGNOSIS RESULTS ===\n")
	fmt.Printf("user_book_read ID: %d\n", ubr.ID)
	fmt.Printf("user_book_read.edition_id: %v\n", ubr.EditionID)
	fmt.Printf("user_book_read.user_book_id: %d\n", ubr.UserBookID)
	fmt.Printf("user_book.id: %d\n", ubr.UserBook.ID)
	fmt.Printf("user_book.book_id: %d\n", ubr.UserBook.BookID)
	fmt.Printf("user_book.edition_id: %v\n", ubr.UserBook.EditionID)

	if ubr.UserBook.Edition != nil {
		fmt.Printf("user_book.edition.id: %d\n", ubr.UserBook.Edition.ID)
		fmt.Printf("user_book.edition.title: %s\n", ubr.UserBook.Edition.Title)
		fmt.Printf("user_book.edition.asin: %s\n", ubr.UserBook.Edition.ASIN)
	} else {
		fmt.Printf("user_book.edition: null\n")
	}

	// Analyze the issue
	if ubr.EditionID == nil && ubr.UserBook.EditionID != nil {
		fmt.Printf("❌ PROBLEM IDENTIFIED: user_book_read.edition_id is NULL but user_book.edition_id is %d\n", *ubr.UserBook.EditionID)
		fmt.Printf("🔧 SOLUTION: The user_book_read needs to be updated with edition_id=%d\n", *ubr.UserBook.EditionID)
	} else if ubr.EditionID != nil && ubr.UserBook.EditionID != nil && *ubr.EditionID != *ubr.UserBook.EditionID {
		fmt.Printf("❌ MISMATCH: user_book_read.edition_id=%d but user_book.edition_id=%d\n", *ubr.EditionID, *ubr.UserBook.EditionID)
	} else if ubr.EditionID == nil && ubr.UserBook.EditionID == nil {
		fmt.Printf("⚠️  BOTH NULL: Both user_book_read.edition_id and user_book.edition_id are null\n")
	} else {
		fmt.Printf("✅ OK: edition_id values are consistent\n")
	}
	
	fmt.Printf("=== END DIAGNOSIS ===\n")
}

// verifyUserBookEdition checks what edition_id a user_book has
func verifyUserBookEdition(userBookID int) int {
	if userBookID == 0 {
		fmt.Printf("Cannot verify user_book edition - userBookID is 0\n")
		return 0
	}
	
	fmt.Printf("=== VERIFYING USER_BOOK %d EDITION ===\n", userBookID)
	
	query := `
	query VerifyUserBook($id: Int!) {
	  user_books(where: {id: {_eq: $id}}) {
		id
		book_id
		edition_id
		edition {
		  id
		  title
		  asin
		}
	  }
	}`
	
	variables := map[string]interface{}{
		"id": userBookID,
	}
	
	payload := map[string]interface{}{
		"query": query,
		"variables": variables,
	}
	
	payloadBytes, _ := json.Marshal(payload)
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		fmt.Printf("Failed to create user_book verification request: %v\n", err)
		return 0
	}
	
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
	
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to execute user_book verification request: %v\n", err)
		return 0
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Failed to read user_book verification response: %v\n", err)
		return 0
	}
	
	var result struct {
		Data struct {
			UserBooks []struct {
				ID        int  `json:"id"`
				BookID    int  `json:"book_id"`
				EditionID *int `json:"edition_id"`
				Edition   *struct {
					ID    int    `json:"id"`
					Title string `json:"title"`
					ASIN  string `json:"asin"`
				} `json:"edition"`
			} `json:"user_books"`
		} `json:"data"`
	}
	
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Failed to parse user_book verification response: %v\n", err)
		return 0
	}
	
	if len(result.Data.UserBooks) == 0 {
		fmt.Printf("No user_book found with ID %d\n", userBookID)
		return 0
	}
	
	ub := result.Data.UserBooks[0]
	fmt.Printf("user_book ID: %d\n", ub.ID)
	fmt.Printf("user_book.book_id: %d\n", ub.BookID)
	fmt.Printf("user_book.edition_id: %v\n", ub.EditionID)
	
	if ub.Edition != nil {
		fmt.Printf("user_book.edition.id: %d\n", ub.Edition.ID)
		fmt.Printf("user_book.edition.title: %s\n", ub.Edition.Title)
		fmt.Printf("user_book.edition.asin: %s\n", ub.Edition.ASIN)
	} else {
		fmt.Printf("user_book.edition: null\n")
	}
	
	if ub.EditionID != nil {
		fmt.Printf("✅ user_book has edition_id: %d\n", *ub.EditionID)
		return *ub.EditionID
	} else {
		fmt.Printf("❌ user_book.edition_id is null\n")
		return 0
	}
}
