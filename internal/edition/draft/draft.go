// Package draft builds an edition preview from an Audiobookshelf item.
//
// It lives beside, not inside, package edition because it reuses the mismatch
// pipeline (mismatch imports the Hardcover client, which imports edition).
//
// The preview uses only Audiobookshelf metadata and optional Audnex release
// metadata. Hardcover resolution happens after the user confirms creation.
package draft

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// draftReason labels the throwaway mismatch record used to build a draft.
const draftReason = "Edition draft requested from Sync Status"

const draftEnrichmentReserve = time.Second

// Draft is a previewable, editable Hardcover edition built from an
// Audiobookshelf item. The JSON tags are the API contract for the
// edition-draft endpoint.
type Draft struct {
	HardcoverBookID int    `json:"hardcover_book_id"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	ASIN            string `json:"asin"`
	ISBN10          string `json:"isbn_10"`
	ISBN13          string `json:"isbn_13"`
	// ISBN10Valid and ISBN13Valid report whether the ISBN's own check digit is
	// correct, like Hardcover's isbn_10_valid and isbn_13_valid. Each is omitted
	// when that ISBN is empty. A bad checksum is a warning, not a blocker:
	// Hardcover accepts and stores a checksum-invalid ISBN.
	ISBN10Valid        *bool    `json:"isbn_10_valid,omitempty"`
	ISBN13Valid        *bool    `json:"isbn_13_valid,omitempty"`
	ReleaseDate        string   `json:"release_date"`
	EditionInformation string   `json:"edition_information"`
	EditionFormat      string   `json:"edition_format"`
	ReadingFormat      string   `json:"reading_format"`
	AudioSeconds       int      `json:"audio_seconds"`
	LanguageID         int      `json:"language_id"`
	CountryID          int      `json:"country_id"`
	AuthorNames        string   `json:"author_names"`
	NarratorNames      string   `json:"narrator_names"`
	PublisherName      string   `json:"publisher_name"`
	DryRun             bool     `json:"dry_run"`
	Warnings           []string `json:"warnings"`
}

// New builds a draft for absBook using the existing mismatch export pipeline.
// hardcoverBookID is copied from the needs-review run record and identifies
// the target for a later create request; it does not trigger a Hardcover call.
func New(ctx context.Context, absBook models.AudiobookshelfBook, hardcoverBookID int, audnexRegion string) (*Draft, error) {
	if hardcoverBookID <= 0 {
		return nil, errors.New("hardcover book id is required")
	}
	if absBook.ID == "" {
		return nil, errors.New("audiobookshelf item id is required")
	}

	meta := absBook.Media.Metadata
	asin := strings.TrimSpace(meta.ASIN)
	readingFormat := absBook.ReadingFormat()
	ebook := readingFormat == models.ReadingFormatEbook
	var authorNames []string
	for _, author := range meta.Authors {
		if name := strings.TrimSpace(author.Name); name != "" {
			authorNames = append(authorNames, name)
		}
	}
	var narratorNames []string
	for _, narrator := range meta.Narrators {
		if name := strings.TrimSpace(narrator); name != "" {
			narratorNames = append(narratorNames, name)
		}
	}
	authorName := meta.AuthorName
	if len(authorNames) > 0 {
		authorName = strings.Join(authorNames, ", ")
	}
	narratorName := meta.NarratorName
	if len(narratorNames) > 0 {
		narratorName = strings.Join(narratorNames, ", ")
	}
	if ebook {
		// Narrators and audio length only apply to audiobooks.
		narratorName = ""
	}
	// Keep the publisher name as source metadata. Passing a nil Hardcover client
	// to the shared mismatch pipeline prevents draft-time Hardcover lookups.
	publisherName := meta.Publisher

	// Optional Audnex enrichment uses a deadline one second earlier than the
	// overall draft budget. It still inherits caller cancellation, while ctx
	// remains available to classify a genuine overall timeout below.
	enrichmentCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		boundedCtx, cancel := context.WithDeadline(ctx, deadline.Add(-draftEnrichmentReserve))
		defer cancel()
		enrichmentCtx = boundedCtx
	}

	record := mismatch.NewCollector().AddWithMetadataContext(
		enrichmentCtx,
		mismatch.MediaMetadata{
			Title:         meta.Title,
			Subtitle:      meta.Subtitle,
			AuthorName:    authorName,
			NarratorName:  narratorName,
			Publisher:     "",
			PublishedYear: meta.PublishedYear,
			PublishedDate: meta.PublishedDate,
			ISBN:          meta.ISBN,
			ASIN:          asin,
			Duration:      absBook.Media.Duration,
			LibraryID:     absBook.LibraryID,
			ReadingFormat: readingFormat,
			Abridged:      meta.Abridged,
		},
		absBook.ID,
		"",
		draftReason,
		absBook.Media.Duration,
		absBook.ID,
		nil,
		audnexRegion,
	)
	record.Publisher = publisherName
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record.HardcoverBookID = strconv.Itoa(hardcoverBookID)

	if logger.FromContext(ctx) == nil {
		ctx = logger.WithLogger(ctx, logger.Get())
	}
	// Retain the established export mapping without Hardcover enrichment.
	export := record.ToEditionExport(ctx, nil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if export == nil {
		return nil, errors.New("edition export was not produced")
	}

	d := &Draft{
		HardcoverBookID:    hardcoverBookID,
		Title:              export.Title,
		Subtitle:           export.Subtitle,
		ASIN:               export.ASIN,
		ISBN10:             export.ISBN10,
		ISBN13:             export.ISBN13,
		ISBN10Valid:        export.ISBN10Valid,
		ISBN13Valid:        export.ISBN13Valid,
		ReleaseDate:        export.ReleaseDate,
		EditionInformation: export.EditionInfo,
		EditionFormat:      export.EditionFormat,
		ReadingFormat:      readingFormat,
		AudioSeconds:       export.AudioSeconds,
		LanguageID:         export.LanguageID,
		CountryID:          export.CountryID,
	}
	if export.Info != nil {
		d.AuthorNames = export.Info.AuthorName
		d.NarratorNames = export.Info.NarratorName
		d.PublisherName = export.Info.PublisherName
	}
	d.fillCounterpartISBN()
	d.Warnings = d.buildWarnings(meta.Language)
	return d, nil
}

// fillCounterpartISBN sets the missing ISBN form when the other can be derived
// from it, so the draft carries both and Hardcover can match either. The user
// can edit both before creating the edition.
func (d *Draft) fillCounterpartISBN() {
	valid := true
	switch {
	case d.ISBN13 != "" && d.ISBN10 == "":
		if parsed, ok := isbn.Parse(d.ISBN13); ok {
			if d.ISBN10 = parsed.ISBN10(); d.ISBN10 != "" {
				d.ISBN10Valid = &valid
			}
		}
	case d.ISBN10 != "" && d.ISBN13 == "":
		if parsed, ok := isbn.Parse(d.ISBN10); ok {
			if d.ISBN13 = parsed.ISBN13(); d.ISBN13 != "" {
				d.ISBN13Valid = &valid
			}
		}
	}
}

// buildWarnings lists source metadata conditions the user should know about
// before creating the edition. It does not check for Hardcover matches.
// absLanguage is the Audiobookshelf item's free-text metadata.language field.
func (d *Draft) buildWarnings(absLanguage string) []string {
	warnings := []string{}
	if d.AuthorNames == "" {
		warnings = append(warnings, "The Audiobookshelf item has no author, and an author is required to create an edition.")
	}
	if d.ReleaseDate == "" {
		warnings = append(warnings, "No release date is available, so the edition will have none.")
	}
	if d.NarratorNames == "" && d.ReadingFormat != models.ReadingFormatEbook {
		warnings = append(warnings, "The Audiobookshelf item lists no narrator, so the edition will have none.")
	}
	if d.ISBN10Valid != nil && !*d.ISBN10Valid {
		warnings = append(warnings, fmt.Sprintf("ISBN-10 %q has an incorrect check digit; Hardcover will still store it as given.", d.ISBN10))
	}
	if d.ISBN13Valid != nil && !*d.ISBN13Valid {
		warnings = append(warnings, fmt.Sprintf("ISBN-13 %q has an incorrect check digit; Hardcover will still store it as given.", d.ISBN13))
	}
	if lang := strings.TrimSpace(absLanguage); lang != "" && !isEnglishLabel(lang) {
		warnings = append(warnings, fmt.Sprintf("The Audiobookshelf item is tagged %q, but the draft defaults to language 1 (English) and country 1 (United States).", lang))
	}
	return warnings
}

func isEnglishLabel(language string) bool {
	label := strings.ToLower(strings.TrimSpace(language))
	switch label {
	case "english", "en",
		"english (us)", "english (united states)",
		"english (uk)", "english (gb)", "english (united kingdom)",
		"english (ca)", "english (canada)",
		"english (au)", "english (australia)",
		"english (nz)", "english (new zealand)":
		return true
	}
	return strings.HasPrefix(label, "en-") || strings.HasPrefix(label, "en_")
}
