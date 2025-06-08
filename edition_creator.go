package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// EditionCreatorInput represents the input data for creating a new edition
type EditionCreatorInput struct {
	BookID        int    `json:"book_id"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`            // Edition subtitle
	ImageURL      string `json:"image_url"`           // Audible cover image URL
	ASIN          string `json:"asin"`                // Audible ASIN
	ISBN10        string `json:"isbn_10"`             // ISBN-10 identifier
	ISBN13        string `json:"isbn_13"`             // ISBN-13 identifier
	AuthorIDs     []int  `json:"author_ids"`          // Hardcover author IDs
	NarratorIDs   []int  `json:"narrator_ids"`        // Hardcover narrator IDs (contributor IDs)
	PublisherID   int    `json:"publisher_id"`        // Hardcover publisher ID
	ReleaseDate   string `json:"release_date"`        // Format: YYYY-MM-DD
	AudioLength   int    `json:"audio_seconds"`       // Audio duration in seconds
	EditionFormat string `json:"edition_format"`      // Edition format (e.g., "Audible Audio")
	EditionInfo   string `json:"edition_information"` // Additional edition information
	LanguageID    int    `json:"language_id"`         // Primary language ID (defaults to 1 for English)
	CountryID     int    `json:"country_id"`          // Country ID (defaults to 1 for USA)
}

// EditionCreatorResult represents the result of edition creation
type EditionCreatorResult struct {
	Success   bool   `json:"success"`
	EditionID int    `json:"edition_id"`
	ImageID   int    `json:"image_id"`
	Error     string `json:"error,omitempty"`
}

// CreateHardcoverEdition automates the two-step process of creating a new audiobook edition in Hardcover
// 1. Upload image from Audible URL (or use existing if already uploaded)
// 2. Create the edition with all metadata
func CreateHardcoverEdition(input EditionCreatorInput) (*EditionCreatorResult, error) {
	result := &EditionCreatorResult{}

	// Step 1: Check if image already exists, upload if needed
	var imageID int
	var err error

	if input.ImageURL != "" {
		isHardcoverAsset, skipUpload, err := isHardcoverAssetURL(input.ImageURL)
		if err != nil {
			debugLog("Warning: Failed to check image URL: %v", err)
			// Continue with upload attempt
		}

		if isHardcoverAsset && skipUpload {
			debugLog("Skipping image upload for book %d - URL is already from Hardcover assets: %s", input.BookID, input.ImageURL)
			// Don't set imageID since we're not uploading - let the edition be created without an image
			imageID = 0
		} else {
			imageID, err = uploadImageFromURL(input.BookID, input.ImageURL)
			if err != nil {
				result.Error = fmt.Sprintf("Failed to upload image: %v", err)
				return result, err
			}
			debugLog("Successfully uploaded new image for book %d, image_id: %d", input.BookID, imageID)
		}
	}

	result.ImageID = imageID

	// Step 2: Create the edition
	editionID, err := createEditionWithMetadata(input, imageID)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to create edition: %v", err)
		return result, err
	}

	result.Success = true
	result.EditionID = editionID
	debugLog("Successfully created edition %d for book %d", editionID, input.BookID)

	return result, nil
}

// uploadImageFromURL uploads an image from URL to Hardcover using the working GraphQL approach
func uploadImageFromURL(bookID int, imageURL string) (int, error) {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would upload image from URL: %s for book ID: %d", imageURL, bookID)
		return 888888, nil // Return a fake ID for dry run
	}

	// Use the working uploadImageToHardcover function from hardcover.go
	// This bypasses the webhook issue by using direct GraphQL mutation
	return uploadImageToHardcover(imageURL, bookID)
}

// insertImageRecord creates an image record in Hardcover's database
func insertImageRecord(bookID int, imageURL string) (int, error) {
	mutation := `
	mutation InsertImage($image: ImageInput!) {
	  insertResponse: insert_image(image: $image) {
	    image {
	      id
	      url
	      color
	      height
	      width
	      imageableId: imageable_id
	      imageableType: imageable_type
	    }
	  }
	}`

	variables := map[string]interface{}{
		"image": map[string]interface{}{
			"imageable_type": "Book",
			"imageable_id":   bookID,
			"url":            imageURL,
		},
	}

	payload := map[string]interface{}{
		"operationName": "InsertImage",
		"query":         mutation,
		"variables":     variables,
	}

	return executeImageMutation(payload)
}

// createEditionWithMetadata creates a new edition with all the provided metadata
func createEditionWithMetadata(input EditionCreatorInput, imageID int) (int, error) {
	// Build contributions array (authors + narrators)
	var contributions []map[string]interface{}

	// Add authors (no contribution specified)
	for _, authorID := range input.AuthorIDs {
		contributions = append(contributions, map[string]interface{}{
			"contribution": nil,
			"author_id":    authorID,
		})
	}

	// Add narrators
	for _, narratorID := range input.NarratorIDs {
		contributions = append(contributions, map[string]interface{}{
			"contribution": "Narrator",
			"author_id":    narratorID,
		})
	}

	// Build edition DTO
	editionDTO := map[string]interface{}{
		"image_id":          imageID,
		"title":             input.Title,
		"contributions":     contributions,
		"reading_format_id": 2, // Audiobook format
		"audio_seconds":     input.AudioLength,
		"release_date":      input.ReleaseDate,
		"asin":              input.ASIN,
	}

	// Add subtitle if provided
	if input.Subtitle != "" {
		editionDTO["subtitle"] = input.Subtitle
	}

	// Add ISBN identifiers if provided
	if input.ISBN10 != "" {
		editionDTO["isbn_10"] = input.ISBN10
	}
	if input.ISBN13 != "" {
		editionDTO["isbn_13"] = input.ISBN13
	}

	// Set edition format (use provided value or default)
	if input.EditionFormat != "" {
		editionDTO["edition_format"] = input.EditionFormat
	} else {
		editionDTO["edition_format"] = "Audible Audio"
	}

	// Add edition information if provided
	if input.EditionInfo != "" {
		editionDTO["edition_information"] = input.EditionInfo
	}

	// Set language ID (use provided value or default to English)
	if input.LanguageID > 0 {
		editionDTO["language_id"] = input.LanguageID
	} else {
		editionDTO["language_id"] = 1 // English (default)
	}

	// Set country ID (use provided value or default to USA)
	if input.CountryID > 0 {
		editionDTO["country_id"] = input.CountryID
	} else {
		editionDTO["country_id"] = 1 // USA (default)
	}

	// Add publisher if specified
	if input.PublisherID > 0 {
		editionDTO["publisher_id"] = input.PublisherID
	}

	mutation := `
	mutation CreateEdition($bookId: Int!, $edition: EditionInput!) {
	  insert_edition(book_id: $bookId, edition: $edition) {
	    errors
	    id
	    edition {
	      id
	      title
	    }
	  }
	}`

	variables := map[string]interface{}{
		"bookId": input.BookID,
		"edition": map[string]interface{}{
			"book_id": input.BookID,
			"dto":     editionDTO,
		},
	}

	payload := map[string]interface{}{
		"operationName": "CreateEdition",
		"query":         mutation,
		"variables":     variables,
	}

	return executeEditionMutation(payload)
}

// executeImageMutation executes the image insertion mutation
func executeImageMutation(payload map[string]interface{}) (int, error) {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would execute image mutation with payload: %+v", payload)
		return 888888, nil // Return a fake ID for dry run
	}

	debugLog("=== GraphQL Mutation: insert_image ===")
	debugLog("Image mutation payload: %+v", payload)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal image payload: %v", err)
	}

	debugLog("Image mutation request body: %s", string(payloadBytes))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to create image request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("image mutation request failed: %v", err)
	}
	defer resp.Body.Close()

	debugLog("Image mutation response status: %d %s", resp.StatusCode, resp.Status)
	debugLog("Image mutation response headers: %+v", resp.Header)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read image response: %v", err)
	}

	debugLog("Image mutation raw response: %s", string(body))

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("image mutation failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			InsertResponse struct {
				Image struct {
					ID int `json:"id"`
				} `json:"image"`
			} `json:"insertResponse"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse image response: %v", err)
	}

	debugLog("Image mutation result data: %+v", result.Data)

	if len(result.Errors) > 0 {
		debugLog("GraphQL errors in image creation: %+v", result.Errors)
		return 0, fmt.Errorf("GraphQL errors in image creation: %v", result.Errors)
	}

	imageID := result.Data.InsertResponse.Image.ID
	debugLog("Successfully created image with ID: %d", imageID)
	return imageID, nil
}

// executeEditionMutation executes the edition creation mutation
func executeEditionMutation(payload map[string]interface{}) (int, error) {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would execute edition mutation with payload: %+v", payload)
		return 777777, nil // Return a fake ID for dry run
	}

	debugLog("=== GraphQL Mutation: insert_edition ===")
	debugLog("Edition mutation payload: %+v", payload)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal edition payload: %v", err)
	}

	debugLog("Edition mutation request body: %s", string(payloadBytes))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to create edition request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("edition mutation request failed: %v", err)
	}
	defer resp.Body.Close()

	debugLog("Edition mutation response status: %d %s", resp.StatusCode, resp.Status)
	debugLog("Edition mutation response headers: %+v", resp.Header)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read edition response: %v", err)
	}

	debugLog("Edition mutation raw response: %s", string(body))

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("edition mutation failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			InsertEdition struct {
				Errors  []string `json:"errors"`
				ID      int      `json:"id"`
				Edition struct {
					ID int `json:"id"`
				} `json:"edition"`
			} `json:"insert_edition"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse edition response: %v", err)
	}

	debugLog("Edition mutation result data: %+v", result.Data)

	if len(result.Errors) > 0 {
		debugLog("GraphQL errors in edition creation: %+v", result.Errors)
		return 0, fmt.Errorf("GraphQL errors in edition creation: %v", result.Errors)
	}

	if len(result.Data.InsertEdition.Errors) > 0 {
		debugLog("Edition creation specific errors: %+v", result.Data.InsertEdition.Errors)
		return 0, fmt.Errorf("edition creation errors: %v", result.Data.InsertEdition.Errors)
	}

	editionID := result.Data.InsertEdition.Edition.ID
	if editionID == 0 {
		editionID = result.Data.InsertEdition.ID // Fallback to top-level ID
		debugLog("Using fallback edition ID: %d", editionID)
	} else {
		debugLog("Successfully created edition with ID: %d", editionID)
	}

	return editionID, nil
}

// Helper functions for author/publisher lookups

// LookupHardcoverAuthor finds an author by name and returns their Hardcover ID
func LookupHardcoverAuthor(name string) (int, error) {
	query := `
	query AuthorByName($name: String!) {
	  authors(where: {name: {_eq: $name}}, limit: 1) {
	    id
	    name
	  }
	}`

	variables := map[string]interface{}{
		"name": name,
	}

	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	result, err := executeGraphQLQuery(payload)
	if err != nil {
		return 0, err
	}

	var response struct {
		Data struct {
			Authors []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"authors"`
		} `json:"data"`
	}

	if err := json.Unmarshal(result, &response); err != nil {
		return 0, fmt.Errorf("failed to parse author response: %v", err)
	}

	if len(response.Data.Authors) == 0 {
		return 0, fmt.Errorf("author not found: %s", name)
	}

	return response.Data.Authors[0].ID, nil
}

// LookupHardcoverPublisher finds a publisher by name and returns their Hardcover ID
func LookupHardcoverPublisher(name string) (int, error) {
	query := `
	query PublisherByName($name: String!) {
	  publishers(where: {name: {_eq: $name}}, limit: 1) {
	    id
	    name
	  }
	}`

	variables := map[string]interface{}{
		"name": name,
	}

	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	result, err := executeGraphQLQuery(payload)
	if err != nil {
		return 0, err
	}

	var response struct {
		Data struct {
			Publishers []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"publishers"`
		} `json:"data"`
	}

	if err := json.Unmarshal(result, &response); err != nil {
		return 0, fmt.Errorf("failed to parse publisher response: %v", err)
	}

	if len(response.Data.Publishers) == 0 {
		return 0, fmt.Errorf("publisher not found: %s", name)
	}

	return response.Data.Publishers[0].ID, nil
}

// executeGraphQLQuery executes a GraphQL query and returns raw response
func executeGraphQLQuery(payload map[string]interface{}) ([]byte, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query payload: %v", err)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// ParseAudibleDuration converts Audible duration string to seconds
// Examples: "14 hrs and 52 mins" -> 53520, "2 hrs" -> 7200
func ParseAudibleDuration(duration string) (int, error) {
	duration = strings.ToLower(strings.TrimSpace(duration))

	if duration == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	var hours, minutes int
	var err error
	var foundValidUnit bool

	// Parse hours - support both "hr", "hrs", "hour", "hours"
	if strings.Contains(duration, "hr") || strings.Contains(duration, "hour") {
		parts := strings.Fields(duration)
		for i, part := range parts {
			if strings.Contains(part, "hr") || strings.Contains(part, "hour") {
				if i > 0 {
					hoursStr := parts[i-1]
					hours, err = strconv.Atoi(hoursStr)
					if err != nil {
						return 0, fmt.Errorf("failed to parse hours: %v", err)
					}
					foundValidUnit = true
				}
				break
			}
		}
	}

	// Parse minutes - support both "min", "mins", "minute", "minutes"
	if strings.Contains(duration, "min") {
		parts := strings.Fields(duration)
		for i, part := range parts {
			if strings.Contains(part, "min") && i > 0 {
				minutesStr := parts[i-1]
				minutes, err = strconv.Atoi(minutesStr)
				if err != nil {
					return 0, fmt.Errorf("failed to parse minutes: %v", err)
				}
				foundValidUnit = true
				break
			}
		}
	}

	if !foundValidUnit {
		return 0, fmt.Errorf("no valid time units found in duration string")
	}

	return (hours * 3600) + (minutes * 60), nil
}

// PrepopulatedEditionInput represents prepopulated data from Hardcover book metadata
type PrepopulatedEditionInput struct {
	BookID              int      `json:"book_id"`
	Title               string   `json:"title"`
	Subtitle            string   `json:"subtitle,omitempty"`
	ImageURL            string   `json:"image_url,omitempty"`
	ASIN                string   `json:"asin,omitempty"`
	ISBN10              string   `json:"isbn_10,omitempty"` // ISBN-10 identifier
	ISBN13              string   `json:"isbn_13,omitempty"` // ISBN-13 identifier
	AuthorIDs           []int    `json:"author_ids"`
	AuthorNames         []string `json:"author_names,omitempty"` // For display purposes
	NarratorIDs         []int    `json:"narrator_ids"`
	NarratorNames       []string `json:"narrator_names,omitempty"` // For display purposes
	PublisherID         int      `json:"publisher_id,omitempty"`
	PublisherName       string   `json:"publisher_name,omitempty"`       // For display purposes
	ReleaseDate         string   `json:"release_date,omitempty"`         // Format: YYYY-MM-DD
	AudioSeconds        int      `json:"audio_seconds,omitempty"`        // Audio duration in seconds
	EditionFormat       string   `json:"edition_format,omitempty"`       // Edition format description
	EditionInfo         string   `json:"edition_information,omitempty"`  // Additional edition information
	LanguageID          int      `json:"language_id,omitempty"`          // Language ID
	CountryID           int      `json:"country_id,omitempty"`           // Country ID
	SourceBookTitle     string   `json:"source_book_title,omitempty"`    // Original book title for reference
	ExistingEditions    []string `json:"existing_editions,omitempty"`    // List of existing editions for reference
	PrepopulationSource string   `json:"prepopulation_source,omitempty"` // "hardcover" or "external"
}

// prepopulateFromHardcoverBook creates a prepopulated edition input from existing Hardcover book data
func prepopulateFromHardcoverBook(bookID string) (*PrepopulatedEditionInput, error) {
	debugLog("Prepopulating edition data from Hardcover book ID: %s", bookID)

	// Fetch detailed book metadata from Hardcover
	metadata, err := getDetailedBookMetadata(bookID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch book metadata: %v", err)
	}

	// Convert book ID to int
	bookIDInt := 0
	if _, err := fmt.Sscanf(bookID, "%d", &bookIDInt); err != nil {
		return nil, fmt.Errorf("invalid book ID format: %s", bookID)
	}

	// Create prepopulated input
	prepopulated := &PrepopulatedEditionInput{
		BookID:              bookIDInt,
		Title:               metadata.Title,
		Subtitle:            metadata.Subtitle,
		ImageURL:            getImageURL(metadata.Image),
		ReleaseDate:         metadata.ReleaseDate,
		AudioSeconds:        metadata.AudioSeconds,
		SourceBookTitle:     metadata.Title,
		PrepopulationSource: "hardcover",
	}

	// Extract author information
	authorIDs := extractAuthorIDs(metadata.Contributions)
	var authorNames []string
	for _, contrib := range metadata.Contributions {
		if strings.EqualFold(contrib.Role, "author") {
			authorNames = append(authorNames, contrib.Author.Name)
		}
	}
	prepopulated.AuthorIDs = authorIDs
	prepopulated.AuthorNames = authorNames

	// Extract narrator information
	narratorIDs := extractNarratorIDs(metadata.Contributions)
	var narratorNames []string
	for _, contrib := range metadata.Contributions {
		if strings.EqualFold(contrib.Role, "narrator") {
			narratorNames = append(narratorNames, contrib.Author.Name)
		}
	}
	prepopulated.NarratorIDs = narratorIDs
	prepopulated.NarratorNames = narratorNames

	// Set publisher information from first available edition
	var publisherID int
	var publisherName string
	if len(metadata.Editions) > 0 {
		for _, edition := range metadata.Editions {
			if edition.PublisherID > 0 {
				publisherID = edition.PublisherID
				publisherName = edition.Publisher.Name
				break
			}
		}
	}
	prepopulated.PublisherID = publisherID
	prepopulated.PublisherName = publisherName

	// Extract ISBN, edition format, and other fields from existing editions
	// Use the first edition that has the required data, or aggregate from multiple editions
	if len(metadata.Editions) > 0 {
		// Try to find an audiobook edition first (reading_format_id = 2), then fallback to any edition
		var sourceEdition *EditionInfo
		for _, edition := range metadata.Editions {
			// Check if this is an audiobook edition (we don't have reading_format_id in the query, but we can infer from audio_seconds)
			if edition.AudioSeconds > 0 {
				sourceEdition = &edition
				break
			}
		}
		// If no audiobook edition found, use the first edition with some data
		if sourceEdition == nil && len(metadata.Editions) > 0 {
			sourceEdition = &metadata.Editions[0]
		}

		if sourceEdition != nil {
			// Extract ISBN identifiers - always set these fields even if empty
			prepopulated.ISBN10 = sourceEdition.ISBN10
			prepopulated.ISBN13 = sourceEdition.ISBN13

			// Set edition format - default to "Audible Audio" for audiobook editions
			if sourceEdition.AudioSeconds > 0 {
				prepopulated.EditionFormat = "Audible Audio"
			} else {
				prepopulated.EditionFormat = "Book" // Generic format for non-audiobook editions
			}

			// Set edition information - for now, leave empty as it's not available in the API
			// Users can manually add this information (e.g., "Unabridged", "Abridged", etc.)
			prepopulated.EditionInfo = ""

			// TODO: Extract language and country information when available in API
			// For now, we'll use defaults (English=1, USA=1) but this could be enhanced
			// when Hardcover exposes language_id and country_id in their API
			prepopulated.LanguageID = 1 // Default to English
			prepopulated.CountryID = 1  // Default to USA

			debugLog("Extracted edition data: ISBN10='%s', ISBN13='%s', format='%s'",
				prepopulated.ISBN10, prepopulated.ISBN13, prepopulated.EditionFormat)
		}
	}

	// Collect existing editions for reference
	var existingEditions []string
	for _, edition := range metadata.Editions {
		editionDesc := fmt.Sprintf("Edition %s", edition.ID.String())
		if edition.ASIN != "" {
			editionDesc += fmt.Sprintf(" (ASIN: %s)", edition.ASIN)
		}
		if edition.AudioSeconds > 0 {
			durationHours := float64(edition.AudioSeconds) / 3600.0
			editionDesc += fmt.Sprintf(" [%.1fh]", durationHours)
		}
		existingEditions = append(existingEditions, editionDesc)
	}
	prepopulated.ExistingEditions = existingEditions

	debugLog("Prepopulated edition data: title='%s', authors=%d, narrators=%d, existing_editions=%d",
		prepopulated.Title, len(prepopulated.AuthorIDs), len(prepopulated.NarratorIDs), len(prepopulated.ExistingEditions))

	return prepopulated, nil
}

// generatePrepopulatedJSON generates a JSON template with prepopulated data from Hardcover
func generatePrepopulatedJSON(bookID string) (string, error) {
	prepopulated, err := prepopulateFromHardcoverBook(bookID)
	if err != nil {
		return "", err
	}

	// Convert to EditionCreatorInput format for JSON generation using the conversion function
	editionInput := convertPrepopulatedToInput(prepopulated)

	jsonBytes, err := json.MarshalIndent(editionInput, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %v", err)
	}

	return string(jsonBytes), nil
}

// enhanceWithExternalData attempts to enhance prepopulated data with external sources (e.g., Audible)
func enhanceWithExternalData(prepopulated *PrepopulatedEditionInput, asin string) error {
	if asin == "" {
		debugLog("No ASIN provided for external data enhancement")
		return nil
	}

	debugLog("Attempting to enhance edition data with external data for ASIN: %s", asin)

	// TODO: Implement external API integration (e.g., Audible API)
	// This is a placeholder for future enhancement with external data sources
	// For now, we just update the ASIN if it wasn't already populated
	if prepopulated.ASIN == "" {
		prepopulated.ASIN = asin
		prepopulated.PrepopulationSource = "hardcover+external"
	}

	return nil
}

// validatePrepopulatedData performs validation on prepopulated edition data
func validatePrepopulatedData(prepopulated *PrepopulatedEditionInput) error {
	if prepopulated.BookID <= 0 {
		return fmt.Errorf("invalid book ID: %d", prepopulated.BookID)
	}

	if strings.TrimSpace(prepopulated.Title) == "" {
		return fmt.Errorf("title is required")
	}

	if len(prepopulated.AuthorIDs) == 0 {
		return fmt.Errorf("at least one author is required")
	}

	// Validate release date format if provided
	if prepopulated.ReleaseDate != "" {
		if _, err := time.Parse("2006-01-02", prepopulated.ReleaseDate); err != nil {
			return fmt.Errorf("invalid release date format (expected YYYY-MM-DD): %s", prepopulated.ReleaseDate)
		}
	}

	// Validate audio seconds if provided
	if prepopulated.AudioSeconds < 0 {
		return fmt.Errorf("invalid audio duration: %d seconds", prepopulated.AudioSeconds)
	}

	debugLog("Prepopulated data validation passed: book_id=%d, title='%s', authors=%d",
		prepopulated.BookID, prepopulated.Title, len(prepopulated.AuthorIDs))

	return nil
}

// convertPrepopulatedToInput converts prepopulated data to EditionCreatorInput format
func convertPrepopulatedToInput(prepopulated *PrepopulatedEditionInput) EditionCreatorInput {
	return EditionCreatorInput{
		BookID:        prepopulated.BookID,
		Title:         prepopulated.Title,
		Subtitle:      prepopulated.Subtitle,
		ImageURL:      prepopulated.ImageURL,
		ASIN:          prepopulated.ASIN,
		ISBN10:        prepopulated.ISBN10,
		ISBN13:        prepopulated.ISBN13,
		AuthorIDs:     prepopulated.AuthorIDs,
		NarratorIDs:   prepopulated.NarratorIDs,
		PublisherID:   prepopulated.PublisherID,
		ReleaseDate:   prepopulated.ReleaseDate,
		AudioLength:   prepopulated.AudioSeconds,
		EditionFormat: prepopulated.EditionFormat,
		EditionInfo:   prepopulated.EditionInfo,
		LanguageID:    prepopulated.LanguageID,
		CountryID:     prepopulated.CountryID,
	}
}

// getImageURL safely extracts URL from ImageInfo struct, returns empty string if nil
func getImageURL(image *ImageInfo) string {
	if image == nil {
		return ""
	}
	return image.URL
}
