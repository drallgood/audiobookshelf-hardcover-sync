package mismatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

var (
	mismatches   []BookMismatch
	mismatchLock sync.Mutex
)

// Add adds a new book mismatch to the collection
func Add(book BookMismatch) {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Set timestamp if not already set
	if book.Timestamp == 0 {
		book.Timestamp = time.Now().Unix()
	}
	if book.Attempts == 0 {
		book.Attempts = 1
	}
	if book.CreatedAt.IsZero() {
		book.CreatedAt = time.Now()
	}

	mismatches = append(mismatches, book)

	// Log the mismatch
	log := logger.Get()
	if log != nil {
		log.Info("Mismatch recorded", map[string]interface{}{
			"title":  book.Title,
			"reason": book.Reason,
		})
	}
}

// RecordMismatch records a new book mismatch
func RecordMismatch(book *BookMismatch) error {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Check if we already have this mismatch
	key := book.BookID
	for i, existing := range mismatches {
		if existing.BookID == key {
			existing.Attempts++
			existing.Timestamp = time.Now().Unix()
			existing.Reason = book.Reason
			mismatches[i] = existing
			return nil
		}
	}

	// Add timestamp and initialize attempts
	book.Timestamp = time.Now().Unix()
	book.CreatedAt = time.Now()
	book.Attempts = 1

	mismatches = append(mismatches, *book)
	return nil
}

// AddWithMetadata creates and adds a new book mismatch with enhanced metadata
// If hc is provided, it will be used to look up publisher and other metadata
func AddWithMetadata(metadata MediaMetadata, bookID, editionID, reason string, duration float64, audiobookShelfID string, hc hardcover.HardcoverClientInterface) {
	// Create a logger
	log := logger.Get()

	// Extract book ID from reason if it's in the format "... for book 12345"
	extractBookIDFromReason := func() string {
		re := regexp.MustCompile(`(?:for book(?: ID)?\s+)(\d+)`)
		matches := re.FindStringSubmatch(reason)
		if len(matches) > 1 {
			return matches[1]
		}
		return ""
	}

	// If we don't have a bookID but can extract one from the reason, use it
	if bookID == "" || bookID == "0" {
		if extractedID := extractBookIDFromReason(); extractedID != "" {
			log.Debug("Extracted book ID from reason", map[string]interface{}{
				"original_book_id": bookID,
				"extracted_id":     extractedID,
				"reason":           reason,
			})
			bookID = extractedID
		}
	}
	// Log the mismatch being added
	log.Debug("Adding mismatch with metadata", map[string]interface{}{
		"book_id":           bookID,
		"edition_id":        editionID,
		"title":             metadata.Title,
		"author":            metadata.AuthorName,
		"reason":            reason,
		"has_cover":         metadata.CoverURL != "",
		"has_hardcover_api": hc != nil,
	})

	// Enhanced release date lookup - try to get accurate date from Audnex API for ASIN
	releaseDate := ""
	audnexReleaseDate := ""
	
	// If we have an ASIN, try to look up the book details from Audnex API
	if metadata.ASIN != "" {
		log.Info("Attempting Audnex enrichment for mismatch with ASIN", map[string]interface{}{
			"asin":     metadata.ASIN,
			"title":    metadata.Title,
			"book_id":  bookID,
			"mismatch": true,
		})
		
		// Create a context with timeout for the ASIN lookup
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		
		// Create Audnex client and log the creation
		audnexClient := audnex.NewClient(logger.Get())
		log.Debug("Created Audnex client for ASIN lookup", map[string]interface{}{
			"asin":       metadata.ASIN,
			"client_nil": audnexClient == nil,
		})
		
		// Log details just before the API call
		log.Info("Calling Audnex API for book details by ASIN", map[string]interface{}{
			"asin":    metadata.ASIN,
			"title":   metadata.Title,
			"context": "mismatch_enrichment",
		})
		
		// Try to get book details from Audnex API
		book, err := audnexClient.GetBookByASIN(ctx, metadata.ASIN, "")
		
		// Enhanced logging based on response
		if err == nil && book != nil {
			// Successfully retrieved book details from Audnex
			log.Info("Audnex API lookup succeeded", map[string]interface{}{
				"asin":           metadata.ASIN,
				"audnex_title":   book.Title,
				"has_release":    book.ReleaseDate != "",
				"authors_type":   fmt.Sprintf("%T", book.Authors),
				"narrators_type": fmt.Sprintf("%T", book.Narrators),
			})
			
			// Get authors and narrators as strings for logging
			authors := book.GetAuthorsAsStrings()
			narrators := book.GetNarratorsAsStrings()
			
			log.Debug("Audnex parsed author/narrator data", map[string]interface{}{
				"asin":                metadata.ASIN,
				"authors_count":       len(authors),
				"narrators_count":     len(narrators),
				"authors_as_strings":  strings.Join(authors, ", "),
				"narrators_as_string": strings.Join(narrators, ", "),
			})
			
			if book.ReleaseDate != "" {
				audnexReleaseDate = book.ReleaseDate
				log.Info("Using release date from Audnex API for mismatch enrichment", map[string]interface{}{
					"asin":         metadata.ASIN,
					"release_date": audnexReleaseDate,
					"title":        book.Title,
				})
			}
		} else if err != nil {
			log.Warn("Failed to lookup book by ASIN from Audnex API", map[string]interface{}{
				"asin":  metadata.ASIN,
				"error": err.Error(),
			})
		} else {
			log.Warn("Audnex API returned nil book without error", map[string]interface{}{
				"asin": metadata.ASIN,
			})
		}
	} else {
		log.Debug("Skipping Audnex enrichment, no ASIN available", nil)
	}
	
	// Format release date - prefer Audnex API date, then publishedDate, fallback to publishedYear
	// Always ensure the date is in YYYY-MM-DD format without time component
	formatDateToYYYYMMDD := func(dateStr string) string {
		// If empty, return empty
		if dateStr == "" {
			return ""
		}
		
		// Check if it's already in YYYY-MM-DD format
		if len(dateStr) == 10 && dateStr[4] == '-' && dateStr[7] == '-' {
			return dateStr
		}
		
		// Try to parse ISO-8601 format (including with time component)
		// RFC3339 ("2006-01-02T15:04:05Z07:00") and variations
		formats := []string{
			"2006-01-02T15:04:05Z07:00", // RFC3339
			"2006-01-02T15:04:05.000Z07:00", // RFC3339 with milliseconds
			"2006-01-02", // Just the date part
			"2006-01-02 15:04:05", // MySQL datetime format
			"2006/01/02", // Slash format
			"01/02/2006", // US format
			"02/01/2006", // EU format
			"Jan 2, 2006", // Month name format
			"2 Jan 2006", // Day first format
		}
		
		for _, format := range formats {
			if parsed, err := time.Parse(format, dateStr); err == nil {
				return parsed.Format("2006-01-02") // Return in YYYY-MM-DD format
			}
		}
		
		// If it's just a year, append -01-01
		if len(dateStr) == 4 && regexp.MustCompile(`^\d{4}$`).MatchString(dateStr) {
			return dateStr + "-01-01" // Use Jan 1st if only year is known
		}
		
		// Could not parse, return as is
		log.Warn("Could not parse date format", map[string]interface{}{
			"date": dateStr,
		})
		return dateStr
	}
	
	// Process and format the release date
	if audnexReleaseDate != "" {
		releaseDate = formatDateToYYYYMMDD(audnexReleaseDate)
		log.Debug("Formatted Audnex release date", map[string]interface{}{
			"original": audnexReleaseDate,
			"formatted": releaseDate,
		})
	} else if metadata.PublishedDate != "" {
		releaseDate = formatDateToYYYYMMDD(metadata.PublishedDate)
	} else if metadata.PublishedYear != "" {
		releaseDate = formatDateToYYYYMMDD(metadata.PublishedYear)
	}

	// Extract ISBN10 and ISBN13 from metadata.ISBN if it's set
	isbn10, isbn13 := "", ""
	if metadata.ISBN != "" {
		// Simple heuristic: ISBN10 is 10 chars, ISBN13 is 13 chars
		if len(metadata.ISBN) == 10 {
			isbn10 = metadata.ISBN
		} else if len(metadata.ISBN) == 13 {
			isbn13 = metadata.ISBN
		}
	}

	// Default publisher values
	publisherID := 1 // Default publisher ID
	publisherName := metadata.Publisher

	// If we have a Hardcover client and a publisher name, try to look up the publisher ID
	if hc != nil && publisherName != "" {
		// Create a context with timeout for the publisher lookup
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Look up the publisher ID
		if id, err := LookupPublisherID(ctx, hc, publisherName); err == nil && id > 0 {
			publisherID = id
			logger.Get().Debug("Found publisher ID", map[string]interface{}{
				"name": publisherName,
				"id":   publisherID,
			})
		} else if err != nil {
			logger.Get().Warn("Failed to look up publisher", map[string]interface{}{
				"name":  publisherName,
				"error": err.Error(),
			})
		} else {
			logger.Get().Debug("Publisher not found, using default ID", map[string]interface{}{
				"name": publisherName,
			})
		}
	}

	// Ensure we have a valid book ID - if not, use the audiobookShelfID as a fallback
	if bookID == "" || bookID == "0" {
		bookID = audiobookShelfID
		log.Debug("Using audiobookShelfID as book ID", map[string]interface{}{
			"audiobook_shelf_id": audiobookShelfID,
		})
	}

	// Note: We no longer automatically use ASIN as bookID to prevent incorrect book identification
	// ASIN is stored in its dedicated field below

	// Create the mismatch with all available metadata
	mismatch := BookMismatch{
		// Core book information
		BookID:          bookID,
		Title:           metadata.Title,
		Subtitle:        metadata.Subtitle,
		Author:          metadata.AuthorName,
		Narrator:        metadata.NarratorName,
		PublishedYear:   metadata.PublishedYear,
		ReleaseDate:     releaseDate,
		DurationSeconds: int(duration + 0.5), // Round to nearest second

		// Identifiers
		ISBN:   metadata.ISBN, // Keep original ISBN for backward compatibility
		ISBN10: isbn10,
		ISBN13: isbn13,
		ASIN:   metadata.ASIN,

		// Media URLs
		CoverURL: metadata.CoverURL,
		ImageURL: metadata.CoverURL, // Use CoverURL as ImageURL by default

		// Edition information
		EditionFormat: "Audiobook",
		EditionInfo:   "Audiobookshelf", // Only include platform info, no debug/error details
		LanguageID:    1, // Default to English
		CountryID:     1, // Default to US

		// Publisher information
		PublisherID: publisherID, // Use looked up or default publisher ID
		Publisher:   publisherName,


		// Audiobookshelf-specific context
		LibraryID: metadata.LibraryID,
		FolderID:  metadata.FolderID,

		// Tracking information
		Reason:    reason,
		Timestamp: time.Now().Unix(),
		CreatedAt: time.Now(),
	}

	// If we have a Hardcover client, try to enrich with Hardcover-side details
	if hc != nil {
		log.Debug("Attempting Hardcover enrichment for mismatch", map[string]interface{}{
			"book_id":    bookID,
			"edition_id": editionID,
			"asin":       metadata.ASIN,
			"isbn":       metadata.ISBN,
			"title":      metadata.Title,
			"author":     metadata.AuthorName,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Helper to apply Hardcover book details to mismatch
		applyHC := func(hcBook *models.HardcoverBook) {
			if hcBook == nil {
				return
			}
			// Prefer setting HC fields only if we actually have them
			if mismatch.HardcoverBookID == "" && hcBook.ID != "" {
				mismatch.HardcoverBookID = hcBook.ID
			}
			if mismatch.HardcoverTitle == "" && hcBook.Title != "" {
				mismatch.HardcoverTitle = hcBook.Title
			}
			if mismatch.HardcoverSlug == "" && hcBook.Slug != "" {
				mismatch.HardcoverSlug = hcBook.Slug
				log.Debug("Applied Hardcover slug to mismatch", map[string]interface{}{
					"slug": hcBook.Slug,
				})
			} else if mismatch.HardcoverSlug != "" {
				log.Debug("Skipped slug apply: mismatch already has slug", map[string]interface{}{
					"existing_slug": mismatch.HardcoverSlug,
				})
			} else if hcBook.Slug == "" {
				log.Debug("Skipped slug apply: Hardcover book has empty slug", nil)
			}
			if mismatch.HardcoverCoverURL == "" && hcBook.CoverImageURL != "" {
				mismatch.HardcoverCoverURL = hcBook.CoverImageURL
			}
            // Only apply publisher when we have a confirmed edition match via identifiers (ASIN/ISBN)
            if mismatch.HardcoverPublisher == "" && hcBook.Publisher != "" {
                asinMatch := hcBook.EditionASIN != "" && mismatch.ASIN != "" && strings.EqualFold(hcBook.EditionASIN, mismatch.ASIN)
                isbnMatch := (hcBook.EditionISBN13 != "" && mismatch.ISBN13 != "" && strings.EqualFold(hcBook.EditionISBN13, mismatch.ISBN13)) ||
                    (hcBook.EditionISBN10 != "" && mismatch.ISBN10 != "" && strings.EqualFold(hcBook.EditionISBN10, mismatch.ISBN10)) ||
                    (hcBook.EditionISBN13 != "" && mismatch.ISBN != "" && strings.EqualFold(hcBook.EditionISBN13, mismatch.ISBN)) ||
                    (hcBook.EditionISBN10 != "" && mismatch.ISBN != "" && strings.EqualFold(hcBook.EditionISBN10, mismatch.ISBN))
                if asinMatch || isbnMatch {
                    mismatch.HardcoverPublisher = hcBook.Publisher
                } else {
                    log.Debug("Skipped publisher apply: no confirmed edition identifier match", map[string]interface{}{
                        "hc_edition_asin": hcBook.EditionASIN,
                        "hc_isbn13":       hcBook.EditionISBN13,
                        "hc_isbn10":       hcBook.EditionISBN10,
                        "abs_asin":        mismatch.ASIN,
                        "abs_isbn":        mismatch.ISBN,
                        "abs_isbn13":      mismatch.ISBN13,
                        "abs_isbn10":      mismatch.ISBN10,
                    })
                }
            }
			// Only set Hardcover identifiers when they match ABS identifiers to avoid random IDs
			if mismatch.HardcoverASIN == "" && mismatch.ASIN != "" {
				if hcBook.EditionASIN != "" && strings.EqualFold(hcBook.EditionASIN, mismatch.ASIN) {
					mismatch.HardcoverASIN = hcBook.EditionASIN
				} else if hcBook.ASIN != "" && strings.EqualFold(hcBook.ASIN, mismatch.ASIN) {
					mismatch.HardcoverASIN = hcBook.ASIN
				}
			}
			if mismatch.HardcoverISBN == "" && (mismatch.ISBN != "" || mismatch.ISBN13 != "" || mismatch.ISBN10 != "") {
				if hcBook.EditionISBN13 != "" && (strings.EqualFold(hcBook.EditionISBN13, mismatch.ISBN13) || strings.EqualFold(hcBook.EditionISBN13, mismatch.ISBN)) {
					mismatch.HardcoverISBN = hcBook.EditionISBN13
				} else if hcBook.EditionISBN10 != "" && (strings.EqualFold(hcBook.EditionISBN10, mismatch.ISBN10) || strings.EqualFold(hcBook.EditionISBN10, mismatch.ISBN)) {
					mismatch.HardcoverISBN = hcBook.EditionISBN10
				} else if hcBook.ISBN != "" && (strings.EqualFold(hcBook.ISBN, mismatch.ISBN) || strings.EqualFold(hcBook.ISBN, mismatch.ISBN13) || strings.EqualFold(hcBook.ISBN, mismatch.ISBN10)) {
					mismatch.HardcoverISBN = hcBook.ISBN
				}
			}
			// Authors
			if mismatch.HardcoverAuthor == "" && len(hcBook.Authors) > 0 {
				authors := make([]string, 0, len(hcBook.Authors))
				for _, a := range hcBook.Authors {
					name := strings.TrimSpace(a.Name)
					if name != "" {
						authors = append(authors, name)
					}
				}
				if len(authors) > 0 {
					mismatch.HardcoverAuthor = strings.Join(authors, ", ")
				}
			}
			// Published year from release date (YYYY or YYYY-MM-DD)
			if mismatch.HardcoverPublishedYear == "" && hcBook.ReleaseDate != "" {
				year := hcBook.ReleaseDate
				if len(year) >= 4 {
					year = year[:4]
				}
				if regexp.MustCompile(`^\d{4}$`).MatchString(year) {
					mismatch.HardcoverPublishedYear = year
				}
			}
		}

		// If we already know a HardcoverBookID, we keep it for linking in the UI.
		// The interface does not support fetching by book ID directly.

		        // Try to use editionID first to discover book context and identifiers
        if editionID != "" {
            if ed, err := hc.GetEdition(ctx, editionID); err == nil && ed != nil {
                if ed.BookID != "" && mismatch.HardcoverBookID == "" {
                    mismatch.HardcoverBookID = ed.BookID
                }
                // If we now know the HardcoverBookID, fetch richer book metadata
                if mismatch.HardcoverBookID != "" {
                    if b, err := hc.GetBookByID(ctx, mismatch.HardcoverBookID); err == nil && b != nil {
                        applyHC(b)
                    } else if err != nil {
                        log.Debug("GetBookByID failed during edition enrichment", map[string]interface{}{
                            "book_id": mismatch.HardcoverBookID,
                            "error":   err.Error(),
                        })
                    }
                }
                // If edition provides identifiers, try to resolve full book to get authors
                if ed.ISBN13 != "" {
                    if b, err := hc.SearchBookByISBN13(ctx, ed.ISBN13); err == nil && b != nil {
                        applyHC(b)
                    }
				} else if ed.ISBN10 != "" {
					if b, err := hc.SearchBookByISBN10(ctx, ed.ISBN10); err == nil && b != nil {
						applyHC(b)
					}
				} else if ed.ASIN != "" {
					if b, err := hc.SearchBookByASIN(ctx, ed.ASIN); err == nil && b != nil {
						applyHC(b)
					}
				}
			}
		}

		        // If we were given a HardcoverBookID directly, prefer fetching it now
        if mismatch.HardcoverBookID != "" {
            if b, err := hc.GetBookByID(ctx, mismatch.HardcoverBookID); err == nil && b != nil {
                applyHC(b)
            } else if err != nil {
                log.Debug("GetBookByID failed using provided HardcoverBookID", map[string]interface{}{
                    "book_id": mismatch.HardcoverBookID,
                    "error":   err.Error(),
                })
            }
        }

        // Fall back to provided identifiers
        if mismatch.HardcoverAuthor == "" {
            if isbn13 != "" {
                if b, err := hc.SearchBookByISBN13(ctx, isbn13); err == nil && b != nil {
                    applyHC(b)
                }
			} else if isbn10 != "" {
				if b, err := hc.SearchBookByISBN10(ctx, isbn10); err == nil && b != nil {
					applyHC(b)
				}
			} else if metadata.ASIN != "" {
				if b, err := hc.SearchBookByASIN(ctx, metadata.ASIN); err == nil && b != nil {
					applyHC(b)
				}
			}
		}

		// Final fallback: search by title/author with best-match selection
        if mismatch.HardcoverAuthor == "" && metadata.Title != "" {
            if results, err := hc.SearchBooks(ctx, metadata.Title, metadata.AuthorName); err == nil && len(results) > 0 {
                // Normalize helper: lower-case, trim, remove punctuation-like chars and collapse spaces
                normalize := func(s string) string {
                    s = strings.ToLower(strings.TrimSpace(s))
                    replacer := strings.NewReplacer(
                        ",", " ", ".", " ", ":", " ", ";", " ", "-", " ", "_", " ",
                        "(" , " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ",
                        "'", " ", "\"", " ", "&", " and ", "/", " ", "\\", " ",
                    )
                    s = replacer.Replace(s)
                    for strings.Contains(s, "  ") { s = strings.ReplaceAll(s, "  ", " ") }
                    return strings.TrimSpace(s)
                }

                nt := normalize(metadata.Title)
                na := normalize(metadata.AuthorName)

                bestIdx := 0
                bestScore := -1
                // Cap detailed lookups to first 5 to respect rate limits
                maxCheck := len(results)
                if maxCheck > 5 { maxCheck = 5 }
                for i := 0; i < maxCheck; i++ {
                    rt := normalize(results[i].Title)
                    score := 0
                    if rt == nt { score += 3 }
                    if rt != nt && (strings.Contains(rt, nt) || strings.Contains(nt, rt)) { score += 1 }

                    // If author provided, fetch detailed book to compare authors
                    if na != "" {
                        if detailed, derr := hc.GetBookByID(ctx, results[i].ID); derr == nil && detailed != nil {
                            // Combine author names
                            names := make([]string, 0, len(detailed.Authors))
                            for _, a := range detailed.Authors { names = append(names, a.Name) }
                            ra := normalize(strings.Join(names, " "))
                            if ra == na { score += 3 }
                            if ra != na && (strings.Contains(ra, na) || strings.Contains(na, ra)) { score += 1 }
                            // Prefer candidates that have a cover image
                            if detailed.CoverImageURL != "" { score += 1 }
                            // Replace lightweight result with detailed one for final apply if chosen best
                            if score > bestScore {
                                bestScore = score
                                bestIdx = i
                                // Update results[i] with more complete info
                                results[i] = *detailed
                            }
                            continue
                        }
                    }

                    if results[i].CoverImageURL != "" { score += 1 }
                    if score > bestScore { bestScore = score; bestIdx = i }
                }
                applyHC(&results[bestIdx])
            }
        }

		// Final note: we cannot fetch by HardcoverBookID with current client interface; rely on
		// edition lookups and searches above to fill details.

		        log.Debug("Hardcover enrichment result", map[string]interface{}{
            "hc_book_id":    mismatch.HardcoverBookID,
            "hc_author":     mismatch.HardcoverAuthor,
            "hc_title":      mismatch.HardcoverTitle,
            "hc_slug":       mismatch.HardcoverSlug,
            "hc_publisher":  mismatch.HardcoverPublisher,
            "hc_asin":       mismatch.HardcoverASIN,
            "hc_isbn":       mismatch.HardcoverISBN,
            "hc_cover":      mismatch.HardcoverCoverURL != "",
            "hc_year":       mismatch.HardcoverPublishedYear,
        })
	}

	Add(mismatch)
}

// GetAll returns a copy of all collected mismatches
func GetAll() []BookMismatch {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Return a copy to avoid race conditions
	result := make([]BookMismatch, len(mismatches))
	copy(result, mismatches)
	return result
}

// Clear removes all collected mismatches
func Clear() {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()
	mismatches = []BookMismatch{}
}

// ExportJSON returns all mismatches as a JSON string
func ExportJSON() (string, error) {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Create a struct that matches the expected JSON structure
	type exportStruct struct {
		Mismatches []BookMismatch `json:"mismatches"`
		Count      int            `json:"count"`
		Timestamp  int64          `json:"timestamp"`
	}

	exportData := exportStruct{
		Mismatches: mismatches,
		Count:      len(mismatches),
		Timestamp:  time.Now().Unix(),
	}

	jsonData, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal mismatches to JSON: %w", err)
	}

	return string(jsonData), nil
}

// SaveToFile saves all mismatches as individual JSON files in the specified directory
// in a format compatible with the edition import tool. If outputDir is empty, it will
// use the directory from the provided config.
// Note: This function should be called with a context that has a Hardcover client available
// for proper author/narrator lookups.
func SaveToFile(ctx context.Context, hc hardcover.HardcoverClientInterface, outputDir string, cfg *config.Config) error {
	// Get logger instance
	log := logger.Get()

	// Determine the output directory
	if outputDir == "" {
		if cfg == nil || cfg.Paths.MismatchOutputDir == "" {
			err := fmt.Errorf("no output directory specified and no default in config")
			log.Error("Failed to determine output directory in mismatch.SaveToFile", map[string]interface{}{
				"error": err.Error(),
			})
			return err
		}
		outputDir = cfg.Paths.MismatchOutputDir
	}

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		err := fmt.Errorf("failed to create output directory: %w", err)
		log.Error("Failed to create output directory in mismatch.SaveToFile", map[string]interface{}{
			"directory": outputDir,
			"error":     err.Error(),
		})
		return err
	}

	// Clean up old files first
	if err := cleanupOldFiles(outputDir); err != nil {
		log.Warn("Failed to clean up old mismatch files", map[string]interface{}{
			"directory": outputDir,
			"error":     err.Error(),
		})
		// Continue anyway, this isn't a fatal error
	}

	// Get all mismatches
	mismatches := GetAll()
	if len(mismatches) == 0 {
		log.Info("No mismatches to save")
		return nil
	}

	// Track errors
	var saveErrors []error
	successCount := 0

	// Save each mismatch to a separate file
	for i, mismatch := range mismatches {
		// Generate a filename based on the book title
		safeTitle := SanitizeFilename(mismatch.Title)
		if safeTitle == "" {
			safeTitle = fmt.Sprintf("untitled_%d", i+1)
		}

		// Create a filename with a sequence number and the book title
		filename := fmt.Sprintf("edition_%03d_%s.json", i+1, safeTitle)
		filePath := filepath.Join(outputDir, filename)

		// Convert to export format
		// Use the provided context and Hardcover client for author/narrator lookups
		export := mismatch.ToEditionExport(ctx, hc)

		// Set edition information if not already set - only include platform info, not debug/error details
		if export.EditionInfo == "" {
			export.EditionInfo = "Audiobookshelf"
		}

		// Convert to JSON with indentation for readability
		jsonData, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			err = fmt.Errorf("failed to marshal edition export for '%s': %w", mismatch.Title, err)
			log.Error("Failed to marshal edition export to JSON", map[string]interface{}{
				"error": err.Error(),
				"title": mismatch.Title,
			})
			saveErrors = append(saveErrors, err)
			continue
		}

		// Add trailing newline for better file handling
		jsonData = append(jsonData, '\n')

		// Write to file
		if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
			err = fmt.Errorf("failed to write file '%s': %w", filePath, err)
			log.Error("Failed to write mismatch file in mismatch.SaveToFile", map[string]interface{}{
				"error":    err.Error(),
				"filePath": filePath,
			})
			saveErrors = append(saveErrors, err)
			continue
		}

		successCount++
	}

	// Log results
	if len(saveErrors) > 0 {
		log.Warn("Some mismatch files failed to save in mismatch.SaveToFile", map[string]interface{}{
			"successful": successCount,
			"failed":     len(saveErrors),
		})
	} else {
		log.Info("Successfully saved all mismatch files in mismatch.SaveToFile", map[string]interface{}{
			"count": successCount,
		})
	}

	return nil
}

// cleanupOldFiles removes old JSON files from the output directory
func cleanupOldFiles(dirPath string) error {
	log := logger.Get()

	// List all files in the directory
	files, err := os.ReadDir(dirPath)
	if err != nil {
		if log != nil {
			log.Error("Failed to read directory in mismatch.cleanupOldFiles", map[string]interface{}{
				"error":     err.Error(),
				"directory": dirPath,
			})
		}
		return fmt.Errorf("failed to read directory: %w", err)
	}

	// Delete all .json files
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
			filePath := filepath.Join(dirPath, file.Name())
			if err := os.Remove(filePath); err != nil {
				if log != nil {
					log.Error("Failed to remove file in mismatch.cleanupOldFiles", map[string]interface{}{
						"error": err.Error(),
						"file":  filePath,
					})
				}
				return fmt.Errorf("failed to remove file %s: %w", filePath, err)
			}
		}
	}

	return nil
}

// SanitizeFilename removes or replaces characters that are invalid in filenames
func SanitizeFilename(s string) string {
	// Replace invalid characters with spaces
	replacer := strings.NewReplacer(
		"<", "", ">", "", ":", " ", "\"", "", "/", " ", "\\", " ", "|", " ",
		"?", "", "*", "", "'", "", "&", "and", "%", "", "#", "",
		"@", "", "!", "", "$", "", "+", "", "`", "", "=", "", "~", "",
	)
	result := replacer.Replace(s)

	// Trim any leading/trailing spaces or dots
	result = strings.TrimSpace(result)
	result = strings.Trim(result, ".")

	// Replace multiple spaces with a single space
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}

	// Limit length to avoid filesystem limits
	if len(result) > 100 {
		result = result[:100]
	}

	return result
}

// MediaMetadata contains metadata about an audiobook
// that can be used to enhance mismatch reporting
type MediaMetadata struct {
	Title         string
	Subtitle      string
	AuthorName    string
	NarratorName  string
	Publisher     string
	PublishedYear string
	PublishedDate string // Full publication date in YYYY-MM-DD format
	ISBN          string
	ASIN          string
	CoverURL      string  // URL to the book cover image
	Duration      float64 `json:"duration,omitempty"`
	LibraryID     string  // Audiobookshelf library ID
	FolderID      string  // Source folder ID (if available)
}
