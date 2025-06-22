// +build ignore

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// createEditionCommand handles the --create-edition command line flag
func createEditionCommand() error {
	fmt.Println("=== Hardcover Edition Creator ===")
	fmt.Println("This tool will help you create a new audiobook edition in Hardcover.")
	fmt.Println()

	// Safety warning about production API calls
	if !getDryRun() {
		fmt.Println("⚠️  WARNING: You are NOT in DRY_RUN mode!")
		fmt.Println("⚠️  This will make REAL API calls to Hardcover and create actual editions!")
		fmt.Println("⚠️  To test safely, set: export DRY_RUN=true")
		fmt.Print("⚠️  Are you sure you want to continue? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
			fmt.Println("Operation cancelled.")
			return nil
		}
	} else {
		fmt.Println("🧪 DRY_RUN mode enabled - no real API calls will be made")
	}
	fmt.Println()

	// Collect required information
	input := EditionCreatorInput{}

	// Book ID (required)
	fmt.Print("Enter Hardcover Book ID: ")
	var bookIDStr string
	fmt.Scanln(&bookIDStr)
	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		return fmt.Errorf("invalid book ID: %v", err)
	}
	input.BookID = bookID

	// Title
	fmt.Print("Enter edition title: ")
	fmt.Scanln(&input.Title)

	// Audible ASIN
	fmt.Print("Enter Audible ASIN: ")
	fmt.Scanln(&input.ASIN)

	// Audible image URL
	fmt.Print("Enter Audible cover image URL: ")
	fmt.Scanln(&input.ImageURL)

	// Release date
	fmt.Print("Enter release date (YYYY-MM-DD): ")
	fmt.Scanln(&input.ReleaseDate)

	// Audio duration
	fmt.Print("Enter audio duration (e.g., '14 hrs and 52 mins' or seconds): ")
	var durationInput string
	fmt.Scanln(&durationInput)

	// Try to parse as duration string first, then as seconds
	audioSeconds, err := ParseAudibleDuration(durationInput)
	if err != nil {
		// Try parsing as direct seconds
		audioSeconds, err = strconv.Atoi(durationInput)
		if err != nil {
			return fmt.Errorf("failed to parse audio duration: %v", err)
		}
	}
	input.AudioLength = audioSeconds

	// Authors (optional - can be looked up)
	fmt.Print("Enter author names (comma-separated): ")
	var authorsInput string
	fmt.Scanln(&authorsInput)
	if authorsInput != "" {
		authorNames := strings.Split(authorsInput, ",")
		for _, name := range authorNames {
			name = strings.TrimSpace(name)
			if name != "" {
				authorID, err := LookupHardcoverAuthor(name)
				if err != nil {
					fmt.Printf("Warning: Could not find author '%s': %v
", name, err)
				} else {
					input.AuthorIDs = append(input.AuthorIDs, authorID)
					fmt.Printf("Found author: %s (ID: %d)
", name, authorID)
				}
			}
		}
	}

	// Narrators (optional - can be looked up)
	fmt.Print("Enter narrator names (comma-separated): ")
	var narratorsInput string
	fmt.Scanln(&narratorsInput)
	if narratorsInput != "" {
		narratorNames := strings.Split(narratorsInput, ",")
		for _, name := range narratorNames {
			name = strings.TrimSpace(name)
			if name != "" {
				narratorID, err := LookupHardcoverAuthor(name) // Narrators are stored as authors
				if err != nil {
					fmt.Printf("Warning: Could not find narrator '%s': %v
", name, err)
				} else {
					input.NarratorIDs = append(input.NarratorIDs, narratorID)
					fmt.Printf("Found narrator: %s (ID: %d)
", name, narratorID)
				}
			}
		}
	}

	// Publisher (optional)
	fmt.Print("Enter publisher name (optional): ")
	var publisherInput string
	fmt.Scanln(&publisherInput)
	if publisherInput != "" {
		publisherID, err := LookupHardcoverPublisher(publisherInput)
		if err != nil {
			fmt.Printf("Warning: Could not find publisher '%s': %v
", publisherInput, err)
		} else {
			input.PublisherID = publisherID
			fmt.Printf("Found publisher: %s (ID: %d)
", publisherInput, publisherID)
		}
	}

	// Confirm before creating
	fmt.Println()
	fmt.Println("=== Edition Details ===")
	fmt.Printf("Book ID: %d
", input.BookID)
	fmt.Printf("Title: %s
", input.Title)
	fmt.Printf("ASIN: %s
", input.ASIN)
	fmt.Printf("Image URL: %s
", input.ImageURL)
	fmt.Printf("Release Date: %s
", input.ReleaseDate)
	fmt.Printf("Audio Length: %d seconds (%.1f hours)
", input.AudioLength, float64(input.AudioLength)/3600)
	fmt.Printf("Authors: %v
", input.AuthorIDs)
	fmt.Printf("Narrators: %v
", input.NarratorIDs)
	fmt.Printf("Publisher ID: %d
", input.PublisherID)
	fmt.Println()

	fmt.Print("Create this edition? (y/N): ")
	var confirm string
	fmt.Scanln(&confirm)
	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Edition creation cancelled.")
		return nil
	}

	// Create the edition
	fmt.Println("Creating edition...")
	result, err := CreateHardcoverEdition(input)
	if err != nil {
		return fmt.Errorf("failed to create edition: %v", err)
	}

	// Display results
	fmt.Println()
	fmt.Println("=== Creation Result ===")
	if result.Success {
		fmt.Printf("✅ Edition created successfully!
")
		fmt.Printf("Edition ID: %d
", result.EditionID)
		fmt.Printf("Image ID: %d
", result.ImageID)
		fmt.Printf("Hardcover URL: https://hardcover.app/books/[book-slug]/editions/%d
", result.EditionID)
	} else {
		fmt.Printf("❌ Edition creation failed: %s
", result.Error)
	}

	return nil
}

// createEditionFromJSON handles creating an edition from a JSON file
func createEditionFromJSON(filename string) error {
	// Safety warning about production API calls
	if !getDryRun() {
		fmt.Println("⚠️  WARNING: You are NOT in DRY_RUN mode!")
		fmt.Println("⚠️  This will make REAL API calls to Hardcover and create actual editions!")
		fmt.Println("⚠️  To test safely, set: export DRY_RUN=true")
		fmt.Print("⚠️  Are you sure you want to continue? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
			fmt.Println("Operation cancelled.")
			return nil
		}
	} else {
		fmt.Println("🧪 DRY_RUN mode enabled - no real API calls will be made")
	}
	fmt.Println()

	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %v", filename, err)
	}

	var input EditionCreatorInput
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("failed to parse JSON: %v", err)
	}

	fmt.Printf("Creating edition from %s...
", filename)
	fmt.Printf("Book ID: %d, Title: %s
", input.BookID, input.Title)
	if input.ImageURL != "" {
		fmt.Printf("Image URL: %s
", input.ImageURL)
		fmt.Println("Checking for existing image or uploading new one...")
	}

	result, err := CreateHardcoverEdition(input)
	if err != nil {
		return fmt.Errorf("failed to create edition: %v", err)
	}

	if result.Success {
		fmt.Printf("✅ Edition created successfully! Edition ID: %d
", result.EditionID)
		if result.ImageID > 0 {
			fmt.Printf("   Image ID: %d
", result.ImageID)
		}
	} else {
		fmt.Printf("❌ Edition creation failed: %s
", result.Error)
	}

	return nil
}

// Example JSON structure for batch processing
func printExampleJSON() {
	example := EditionCreatorInput{
		BookID:      362672,
		Title:       "Earth Awakens",
		ImageURL:    "https://m.media-amazon.com/images/I/51IW2IbweAL._SL500_.jpg",
		ASIN:        "B00J9QNNZU",
		AuthorIDs:   []int{134035, 234257},
		NarratorIDs: []int{250721, 259996, 254158, 250655, 270329, 256028},
		PublisherID: 242,
		ReleaseDate: "2014-06-10",
		AudioLength: 53357,
	}

	jsonData, _ := json.MarshalIndent(example, "", "  ")
	fmt.Println("Example JSON structure for batch processing:")
	fmt.Println(string(jsonData))
}

// generateExampleJSON creates an example JSON file for batch edition creation
func generateExampleJSON(filename string) error {
	example := EditionCreatorInput{
		BookID:        123456,
		Title:         "Example Audiobook",
		Subtitle:      "A Comprehensive Guide",
		ImageURL:      "https://m.media-amazon.com/images/I/51example.jpg",
		ASIN:          "B01EXAMPLE",
		ISBN10:        "1234567890",
		ISBN13:        "9781234567890",
		AuthorIDs:     []int{12345, 67890},
		NarratorIDs:   []int{54321, 98765},
		PublisherID:   999,
		ReleaseDate:   "2024-01-15",
		AudioLength:   53357, // ~14.8 hours
		EditionFormat: "Audible Audio",
		EditionInfo:   "Unabridged",
		LanguageID:    1, // English
		CountryID:     1, // USA
	}

	data, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal example JSON: %v", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write example file: %v", err)
	}

	fmt.Printf("✅ Example JSON file created: %s
", filename)
	fmt.Println("Edit this file with your book data, then run:")
	fmt.Printf("  ./main --create-edition-json %s
", filename)

	return nil
}

// createEditionWithPrepopulation handles the --create-edition-prepopulated command
func createEditionWithPrepopulation() error {
	fmt.Println("=== Hardcover Edition Creator (with Prepopulation) ===")
	fmt.Println("This tool will prepopulate edition data from existing Hardcover book metadata.")
	fmt.Println()

	// Get Book ID for prepopulation
	fmt.Print("Enter Hardcover Book ID for prepopulation: ")
	var bookIDStr string
	fmt.Scanln(&bookIDStr)

	// Fetch prepopulated data
	fmt.Println("Fetching book metadata from Hardcover...")
	prepopulated, err := prepopulateFromHardcoverBook(bookIDStr)
	if err != nil {
		return fmt.Errorf("failed to prepopulate data: %v", err)
	}

	// Validate prepopulated data
	if err := validatePrepopulatedData(prepopulated); err != nil {
		return fmt.Errorf("prepopulated data validation failed: %v", err)
	}

	// Display prepopulated information
	fmt.Println()
	fmt.Println("=== Prepopulated Data ===")
	fmt.Printf("Book ID: %d
", prepopulated.BookID)
	fmt.Printf("Title: %s
", prepopulated.Title)
	if prepopulated.Subtitle != "" {
		fmt.Printf("Subtitle: %s
", prepopulated.Subtitle)
	}
	fmt.Printf("Authors: %s (IDs: %v)
", strings.Join(prepopulated.AuthorNames, ", "), prepopulated.AuthorIDs)
	if len(prepopulated.NarratorNames) > 0 {
		fmt.Printf("Narrators: %s (IDs: %v)
", strings.Join(prepopulated.NarratorNames, ", "), prepopulated.NarratorIDs)
	}
	if prepopulated.PublisherName != "" {
		fmt.Printf("Publisher: %s (ID: %d)
", prepopulated.PublisherName, prepopulated.PublisherID)
	}
	if prepopulated.ReleaseDate != "" {
		fmt.Printf("Release Date: %s
", prepopulated.ReleaseDate)
	}
	if prepopulated.AudioSeconds > 0 {
		fmt.Printf("Audio Duration: %d seconds (%.1f hours)
", prepopulated.AudioSeconds, float64(prepopulated.AudioSeconds)/3600)
	}
	if prepopulated.ImageURL != "" {
		fmt.Printf("Image URL: %s
", prepopulated.ImageURL)
	}
	if len(prepopulated.ExistingEditions) > 0 {
		fmt.Printf("Existing Editions: %s
", strings.Join(prepopulated.ExistingEditions, ", "))
	}
	fmt.Printf("Prepopulation Source: %s
", prepopulated.PrepopulationSource)
	fmt.Println()

	// Allow user to override/add missing fields
	input := convertPrepopulatedToInput(prepopulated)

	// ASIN (most likely to be missing or need updating)
	if input.ASIN == "" {
		fmt.Print("Enter Audible ASIN (required for new edition): ")
		fmt.Scanln(&input.ASIN)
	} else {
		fmt.Printf("Current ASIN: %s - press Enter to keep, or enter new ASIN: ", input.ASIN)
		var newASIN string
		fmt.Scanln(&newASIN)
		if newASIN != "" {
			input.ASIN = newASIN
		}
	}

	// Image URL (might need updating)
	if input.ImageURL == "" {
		fmt.Print("Enter Audible cover image URL (required): ")
		fmt.Scanln(&input.ImageURL)
	} else {
		fmt.Printf("Current Image URL: %s
", input.ImageURL)
		fmt.Print("Press Enter to keep, or enter new image URL: ")
		var newImageURL string
		fmt.Scanln(&newImageURL)
		if newImageURL != "" {
			input.ImageURL = newImageURL
		}
	}

	// Title (allow override)
	fmt.Printf("Current Title: %s
", input.Title)
	fmt.Print("Press Enter to keep, or enter new title: ")
	var newTitle string
	fmt.Scanln(&newTitle)
	if newTitle != "" {
		input.Title = newTitle
	}

	// Audio Length (might need updating for audiobook edition)
	if input.AudioLength == 0 {
		fmt.Print("Enter audio duration (e.g., '14 hrs and 52 mins' or seconds): ")
		var durationInput string
		fmt.Scanln(&durationInput)

		audioSeconds, err := ParseAudibleDuration(durationInput)
		if err != nil {
			audioSeconds, err = strconv.Atoi(durationInput)
			if err != nil {
				return fmt.Errorf("failed to parse audio duration: %v", err)
			}
		}
		input.AudioLength = audioSeconds
	} else {
		fmt.Printf("Current Audio Duration: %d seconds (%.1f hours)
", input.AudioLength, float64(input.AudioLength)/3600)
		fmt.Print("Press Enter to keep, or enter new duration: ")
		var newDuration string
		fmt.Scanln(&newDuration)
		if newDuration != "" {
			audioSeconds, err := ParseAudibleDuration(newDuration)
			if err != nil {
				audioSeconds, err = strconv.Atoi(newDuration)
				if err != nil {
					return fmt.Errorf("failed to parse audio duration: %v", err)
				}
			}
			input.AudioLength = audioSeconds
		}
	}

	// Confirm before creating
	fmt.Println()
	fmt.Println("=== Final Edition Details ===")
	fmt.Printf("Book ID: %d
", input.BookID)
	fmt.Printf("Title: %s
", input.Title)
	fmt.Printf("ASIN: %s
", input.ASIN)
	fmt.Printf("Image URL: %s
", input.ImageURL)
	fmt.Printf("Release Date: %s
", input.ReleaseDate)
	fmt.Printf("Audio Length: %d seconds (%.1f hours)
", input.AudioLength, float64(input.AudioLength)/3600)
	fmt.Printf("Authors: %v
", input.AuthorIDs)
	fmt.Printf("Narrators: %v
", input.NarratorIDs)
	fmt.Printf("Publisher ID: %d
", input.PublisherID)
	fmt.Println()

	fmt.Print("Create this edition? (y/N): ")
	var confirm string
	fmt.Scanln(&confirm)
	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Edition creation cancelled.")
		return nil
	}

	// Create the edition
	fmt.Println("Creating edition...")
	result, err := CreateHardcoverEdition(input)
	if err != nil {
		return fmt.Errorf("failed to create edition: %v", err)
	}

	// Display results
	fmt.Println()
	fmt.Println("=== Creation Result ===")
	if result.Success {
		fmt.Printf("✅ Edition created successfully!
")
		fmt.Printf("Edition ID: %d
", result.EditionID)
		fmt.Printf("Image ID: %d
", result.ImageID)
		fmt.Printf("Hardcover URL: https://hardcover.app/books/[book-slug]/editions/%d
", result.EditionID)
	} else {
		fmt.Printf("❌ Edition creation failed: %s
", result.Error)
	}

	return nil
}

// generatePrepopulatedTemplate creates a JSON template with prepopulated data
func generatePrepopulatedTemplate(bookID, filename string) error {
	fmt.Printf("Generating prepopulated template for book ID %s...
", bookID)

	// Generate prepopulated JSON
	jsonData, err := generatePrepopulatedJSON(bookID)
	if err != nil {
		return fmt.Errorf("failed to generate prepopulated JSON: %v", err)
	}

	// Write to file
	if err := os.WriteFile(filename, []byte(jsonData), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %v", filename, err)
	}

	fmt.Printf("✅ Prepopulated template saved to: %s
", filename)
	fmt.Println()
	fmt.Println("=== Template Contents ===")
	fmt.Println(jsonData)
	fmt.Println()
	fmt.Printf("Edit %s with any additional data (ASIN, image URL, etc.) and then use:
", filename)
	fmt.Printf("  ./main --create-edition-json %s
", filename)

	return nil
}

// enhanceExistingTemplate enhances an existing JSON template with prepopulated data
func enhanceExistingTemplate(filename, bookID string) error {
	// Read existing template
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %v", filename, err)
	}

	var existing EditionCreatorInput
	if err := json.Unmarshal(data, &existing); err != nil {
		return fmt.Errorf("failed to parse existing JSON: %v", err)
	}

	// Get prepopulated data
	prepopulated, err := prepopulateFromHardcoverBook(bookID)
	if err != nil {
		return fmt.Errorf("failed to prepopulate data: %v", err)
	}

	// Merge data (keep existing values if present, add missing ones)
	if existing.Title == "" && prepopulated.Title != "" {
		existing.Title = prepopulated.Title
	}
	if len(existing.AuthorIDs) == 0 && len(prepopulated.AuthorIDs) > 0 {
		existing.AuthorIDs = prepopulated.AuthorIDs
	}
	if len(existing.NarratorIDs) == 0 && len(prepopulated.NarratorIDs) > 0 {
		existing.NarratorIDs = prepopulated.NarratorIDs
	}
	if existing.PublisherID == 0 && prepopulated.PublisherID > 0 {
		existing.PublisherID = prepopulated.PublisherID
	}
	if existing.ReleaseDate == "" && prepopulated.ReleaseDate != "" {
		existing.ReleaseDate = prepopulated.ReleaseDate
	}
	if existing.AudioLength == 0 && prepopulated.AudioSeconds > 0 {
		existing.AudioLength = prepopulated.AudioSeconds
	}
	if existing.ImageURL == "" && prepopulated.ImageURL != "" {
		existing.ImageURL = prepopulated.ImageURL
	}

	// Write enhanced template back to file
	enhancedJSON, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal enhanced JSON: %v", err)
	}

	if err := os.WriteFile(filename, enhancedJSON, 0644); err != nil {
		return fmt.Errorf("failed to write enhanced file: %v", err)
	}

	fmt.Printf("✅ Template %s enhanced with prepopulated data
", filename)
	fmt.Println()
	fmt.Println("=== Enhanced Template ===")
	fmt.Println(string(enhancedJSON))

	return nil
}

// lookupAuthorIDCommand provides interactive author ID lookup functionality
func lookupAuthorIDCommand() error {
	fmt.Println("🔍 Author ID Lookup Tool")
	fmt.Println("========================")

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter author name to search: ")
	name, err := reader.ReadString('
')
	if err != nil {
		return fmt.Errorf("failed to read author name: %v", err)
	}
	name = strings.TrimSpace(name)

	if name == "" {
		return fmt.Errorf("author name cannot be empty")
	}

	fmt.Printf("Searching for authors matching '%s'...

", name)

	authors, err := searchAuthorsCached(name, 10)
	if err != nil {
		return fmt.Errorf("author search failed: %v", err)
	}

	if len(authors) == 0 {
		fmt.Printf("❌ No authors found matching '%s'
", name)
		fmt.Println("
💡 Tips:")
		fmt.Println("  - Try using partial names (e.g., 'Stephen' instead of 'Stephen King')")
		fmt.Println("  - Check spelling and try different variations")
		fmt.Println("  - Search for the author's most common name form")
		return nil
	}

	fmt.Printf("✅ Found %d author(s):

", len(authors))

	for i, author := range authors {
		canonicalStatus := ""
		if !author.IsCanonical {
			canonicalStatus = " (alias)"
		}

		fmt.Printf("%d. ID: %d - %s%s
", i+1, author.ID, author.Name, canonicalStatus)
		fmt.Printf("   Books: %d", author.BooksCount)
		if author.Bio != "" {
			bio := author.Bio
			if len(bio) > 100 {
				bio = bio[:97] + "..."
			}
			fmt.Printf(" | Bio: %s", bio)
		}
		if author.CanonicalID != nil {
			fmt.Printf(" | Canonical ID: %d", *author.CanonicalID)
		}
		fmt.Println()
	}

	fmt.Println("
💡 Use the ID numbers above in your edition JSON files.")
	fmt.Println("💡 Prefer canonical authors (without 'alias' label) when possible.")

	return nil
}

// lookupNarratorIDCommand provides interactive narrator ID lookup functionality
func lookupNarratorIDCommand() error {
	fmt.Println("🔍 Narrator ID Lookup Tool")
	fmt.Println("==========================")

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter narrator name to search: ")
	name, err := reader.ReadString('
')
	if err != nil {
		return fmt.Errorf("failed to read narrator name: %v", err)
	}
	name = strings.TrimSpace(name)

	if name == "" {
		return fmt.Errorf("narrator name cannot be empty")
	}

	fmt.Printf("Searching for narrators matching '%s'...

", name)

	narrators, err := searchNarratorsCached(name, 10)
	if err != nil {
		return fmt.Errorf("narrator search failed: %v", err)
	}

	if len(narrators) == 0 {
		fmt.Printf("❌ No narrators found matching '%s'
", name)
		fmt.Println("
💡 Tips:")
		fmt.Println("  - Try using partial names (e.g., 'Jim' instead of 'James Dale')")
		fmt.Println("  - Check spelling and try different variations")
		fmt.Println("  - Some narrators might be listed as authors who also narrate")
		return nil
	}

	fmt.Printf("✅ Found %d narrator(s):

", len(narrators))

	for i, narrator := range narrators {
		canonicalStatus := ""
		if !narrator.IsCanonical {
			canonicalStatus = " (alias)"
		}

		fmt.Printf("%d. ID: %d - %s%s
", i+1, narrator.ID, narrator.Name, canonicalStatus)
		fmt.Printf("   Books: %d", narrator.BooksCount)
		if narrator.Bio != "" {
			bio := narrator.Bio
			if len(bio) > 100 {
				bio = bio[:97] + "..."
			}
			fmt.Printf(" | Bio: %s", bio)
		}
		if narrator.CanonicalID != nil {
			fmt.Printf(" | Canonical ID: %d", *narrator.CanonicalID)
		}
		fmt.Println()
	}

	fmt.Println("
💡 Use the ID numbers above in your edition JSON files.")
	fmt.Println("💡 Prefer canonical narrators (without 'alias' label) when possible.")

	return nil
}

// lookupPublisherIDCommand provides interactive publisher ID lookup functionality
func lookupPublisherIDCommand() error {
	fmt.Println("🔍 Publisher ID Lookup Tool")
	fmt.Println("===========================")

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter publisher name to search: ")
	name, err := reader.ReadString('
')
	if err != nil {
		return fmt.Errorf("failed to read publisher name: %v", err)
	}
	name = strings.TrimSpace(name)

	if name == "" {
		return fmt.Errorf("publisher name cannot be empty")
	}

	fmt.Printf("Searching for publishers matching '%s'...

", name)

	publishers, err := searchPublishersCached(name, 10)
	if err != nil {
		return fmt.Errorf("publisher search failed: %v", err)
	}

	if len(publishers) == 0 {
		fmt.Printf("❌ No publishers found matching '%s'
", name)
		fmt.Println("
💡 Tips:")
		fmt.Println("  - Try using partial names (e.g., 'Penguin' instead of 'Penguin Random House')")
		fmt.Println("  - Check spelling and try different variations")
		fmt.Println("  - Try common publisher abbreviations or alternate names")
		return nil
	}

	fmt.Printf("✅ Found %d publisher(s):

", len(publishers))

	for i, publisher := range publishers {
		canonicalStatus := ""
		if !publisher.IsCanonical {
			canonicalStatus = " (alias)"
		}

		fmt.Printf("%d. ID: %d - %s%s
", i+1, publisher.ID, publisher.Name, canonicalStatus)
		fmt.Printf("   Editions: %d", publisher.EditionsCount)
		if publisher.CanonicalID != nil {
			fmt.Printf(" | Canonical ID: %d", *publisher.CanonicalID)
		}
		fmt.Println()
	}

	fmt.Println("
💡 Use the ID numbers above in your edition JSON files.")
	fmt.Println("💡 Prefer canonical publishers (without 'alias' label) when possible.")

	return nil
}

// verifyIDCommand provides ID verification functionality
func verifyIDCommand(idType, idValue string) error {
	id, err := strconv.Atoi(idValue)
	if err != nil {
		return fmt.Errorf("invalid ID format '%s': must be a number", idValue)
	}

	switch strings.ToLower(idType) {
	case "author", "narrator":
		person, err := getPersonByID(id)
		if err != nil {
			return fmt.Errorf("failed to verify %s ID %d: %v", idType, id, err)
		}

		canonicalStatus := "✅ Canonical"
		if !person.IsCanonical {
			canonicalStatus = "⚠️  Alias"
		}

		fmt.Printf("🔍 %s ID Verification
", strings.Title(idType))
		fmt.Printf("========================
")
		fmt.Printf("ID: %d
", person.ID)
		fmt.Printf("Name: %s
", person.Name)
		fmt.Printf("Status: %s
", canonicalStatus)
		fmt.Printf("Books: %d
", person.BooksCount)
		if person.Bio != "" {
			fmt.Printf("Bio: %s
", person.Bio)
		}
		if person.CanonicalID != nil {
			fmt.Printf("Canonical ID: %d
", *person.CanonicalID)
			fmt.Printf("
💡 Consider using canonical ID %d instead for better data consistency.
", *person.CanonicalID)
		}

	case "publisher":
		publisher, err := getPublisherByID(id)
		if err != nil {
			return fmt.Errorf("failed to verify publisher ID %d: %v", id, err)
		}

		canonicalStatus := "✅ Canonical"
		if !publisher.IsCanonical {
			canonicalStatus = "⚠️  Alias"
		}

		fmt.Printf("🔍 Publisher ID Verification
")
		fmt.Printf("============================
")
		fmt.Printf("ID: %d
", publisher.ID)
		fmt.Printf("Name: %s
", publisher.Name)
		fmt.Printf("Status: %s
", canonicalStatus)
		fmt.Printf("Editions: %d
", publisher.EditionsCount)
		if publisher.CanonicalID != nil {
			fmt.Printf("Canonical ID: %d
", *publisher.CanonicalID)
			fmt.Printf("
💡 Consider using canonical ID %d instead for better data consistency.
", *publisher.CanonicalID)
		}

	default:
		return fmt.Errorf("invalid ID type '%s': must be 'author', 'narrator', or 'publisher'", idType)
	}

	return nil
}

// bulkLookupAuthorsCommand provides bulk author ID lookup functionality
func bulkLookupAuthorsCommand(authorNames string) error {
	fmt.Println("🔍 Bulk Author ID Lookup Tool")
	fmt.Println("=============================")

	names := strings.Split(authorNames, ",")
	if len(names) == 0 {
		return fmt.Errorf("no author names provided")
	}

	fmt.Printf("Looking up %d author(s)...

", len(names))

	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		fmt.Printf("--- Author %d: %s ---
", i+1, name)

		authors, err := searchAuthorsCached(name, 5)
		if err != nil {
			fmt.Printf("❌ Search failed for '%s': %v

", name, err)
			continue
		}

		if len(authors) == 0 {
			fmt.Printf("❌ No authors found matching '%s'

", name)
			continue
		}

		fmt.Printf("✅ Found %d result(s):
", len(authors))
		for j, author := range authors {
			canonicalStatus := ""
			if !author.IsCanonical {
				canonicalStatus = " (alias)"
			}

			fmt.Printf("  %d. ID: %d - %s%s (Books: %d)
", j+1, author.ID, author.Name, canonicalStatus, author.BooksCount)
		}
		fmt.Println()
	}

	fmt.Println("💡 Use the ID numbers above in your edition JSON files.")
	fmt.Println("💡 Prefer canonical authors (without 'alias' label) when possible.")

	return nil
}

// bulkLookupNarratorsCommand provides bulk narrator ID lookup functionality
func bulkLookupNarratorsCommand(narratorNames string) error {
	fmt.Println("🔍 Bulk Narrator ID Lookup Tool")
	fmt.Println("===============================")

	names := strings.Split(narratorNames, ",")
	if len(names) == 0 {
		return fmt.Errorf("no narrator names provided")
	}

	fmt.Printf("Looking up %d narrator(s)...

", len(names))

	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		fmt.Printf("--- Narrator %d: %s ---
", i+1, name)

		narrators, err := searchNarratorsCached(name, 5)
		if err != nil {
			fmt.Printf("❌ Search failed for '%s': %v

", name, err)
			continue
		}

		if len(narrators) == 0 {
			fmt.Printf("❌ No narrators found matching '%s'

", name)
			continue
		}

		fmt.Printf("✅ Found %d result(s):
", len(narrators))
		for j, narrator := range narrators {
			canonicalStatus := ""
			if !narrator.IsCanonical {
				canonicalStatus = " (alias)"
			}

			fmt.Printf("  %d. ID: %d - %s%s (Books: %d)
", j+1, narrator.ID, narrator.Name, canonicalStatus, narrator.BooksCount)
		}
		fmt.Println()
	}

	fmt.Println("💡 Use the ID numbers above in your edition JSON files.")
	fmt.Println("💡 Prefer canonical narrators (without 'alias' label) when possible.")

	return nil
}

// bulkLookupPublishersCommand provides bulk publisher ID lookup functionality
func bulkLookupPublishersCommand(publisherNames string) error {
	fmt.Println("🔍 Bulk Publisher ID Lookup Tool")
	fmt.Println("================================")

	names := strings.Split(publisherNames, ",")
	if len(names) == 0 {
		return fmt.Errorf("no publisher names provided")
	}

	fmt.Printf("Looking up %d publisher(s)...

", len(names))

	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		fmt.Printf("--- Publisher %d: %s ---
", i+1, name)

		publishers, err := searchPublishersCached(name, 5)
		if err != nil {
			fmt.Printf("❌ Search failed for '%s': %v

", name, err)
			continue
		}

		if len(publishers) == 0 {
			fmt.Printf("❌ No publishers found matching '%s'

", name)
			continue
		}

		fmt.Printf("✅ Found %d result(s):
", len(publishers))
		for j, publisher := range publishers {
			canonicalStatus := ""
			if !publisher.IsCanonical {
				canonicalStatus = " (alias)"
			}

			fmt.Printf("  %d. ID: %d - %s%s (Editions: %d)
", j+1, publisher.ID, publisher.Name, canonicalStatus, publisher.EditionsCount)
		}
		fmt.Println()
	}

	fmt.Println("💡 Use the ID numbers above in your edition JSON files.")
	fmt.Println("💡 Prefer canonical publishers (without 'alias' label) when possible.")

	return nil
}

// uploadImageCommand handles image upload from URL to Hardcover
func uploadImageCommand(uploadSpec string) error {
	fmt.Println("📷 Image Upload Tool")
	fmt.Println("===================")

	// Parse format: url:bookID:description
	parts := strings.Split(uploadSpec, ":")
	if len(parts) < 3 {
		return fmt.Errorf("invalid format. Expected: url:bookID:description (e.g., https://example.com/image.jpg:431810:Cover art)")
	}

	// Reconstruct URL (in case it had colons)
	imageURL := strings.Join(parts[:len(parts)-2], ":")
	bookIDStr := parts[len(parts)-2]
	description := parts[len(parts)-1]

	imageURL = strings.TrimSpace(imageURL)
	bookIDStr = strings.TrimSpace(bookIDStr)
	description = strings.TrimSpace(description)

	if imageURL == "" {
		return fmt.Errorf("image URL cannot be empty")
	}
	if bookIDStr == "" {
		return fmt.Errorf("book ID cannot be empty")
	}
	if description == "" {
		return fmt.Errorf("description cannot be empty")
	}

	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		return fmt.Errorf("invalid book ID '%s': %v", bookIDStr, err)
	}

	fmt.Printf("Image URL: %s
", imageURL)
	fmt.Printf("Book ID: %d
", bookID)
	fmt.Printf("Description: %s

", description)

	// Upload the image using GraphQL mutation
	fmt.Println("Uploading image to Hardcover...")

	imageID, err := uploadImageToHardcover(imageURL, bookID)
	if err != nil {
		return fmt.Errorf("image upload failed: %v", err)
	}

	fmt.Printf("✅ Image uploaded successfully!
")
	fmt.Printf("Image ID: %d
", imageID)
	fmt.Println("
💡 You can now use this image ID in your edition JSON files.")

	return nil
}
