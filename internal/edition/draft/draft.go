// Package draft builds a Hardcover edition draft from an Audiobookshelf item.
//
// It lives beside, not inside, package edition because it reuses the mismatch
// pipeline (mismatch imports the Hardcover client, which imports edition).
//
// The draft retains exact, case-sensitive Hardcover matching for people and
// publishers.
// Names that differ by punctuation or spelling will not match; buildWarnings
// surfaces missing matches until looser lookup behavior can be verified
// against the hosted API.
package draft

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// draftReason labels the throwaway mismatch record used to build a draft.
const draftReason = "Edition draft requested from Sync Status"

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
	AuthorIDs          []int    `json:"author_ids"`
	NarratorIDs        []int    `json:"narrator_ids"`
	PublisherID        int      `json:"publisher_id"`
	AuthorNames        string   `json:"author_names"`
	NarratorNames      string   `json:"narrator_names"`
	PublisherName      string   `json:"publisher_name"`
	DryRun             bool     `json:"dry_run"`
	Warnings           []string `json:"warnings"`
}

// UpstreamError reports a Hardcover lookup failure while preparing a draft.
// The multi-user service translates it to its sanitized upstream response;
// mismatch exports intentionally keep their historical best-effort behavior.
type UpstreamError struct {
	Err error
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("hardcover draft lookup failed: %v", e.Err)
}
func (e *UpstreamError) Unwrap() error { return e.Err }

// New builds a draft for absBook using the existing mismatch export pipeline.
// hardcoverBookID is the book the edition will attach to; it always overrides
// whatever Hardcover candidate enrichment guessed. Author, narrator, and
// publisher IDs are resolved through hc, so this issues Hardcover read queries.
func New(ctx context.Context, absBook models.AudiobookshelfBook, hardcoverBookID int, hc hardcover.HardcoverClientInterface, audnexRegion string) (*Draft, error) {
	if hc == nil {
		return nil, errors.New("hardcover client is required")
	}
	if hardcoverBookID <= 0 {
		return nil, errors.New("hardcover book id is required")
	}
	if absBook.ID == "" {
		return nil, errors.New("audiobookshelf item id is required")
	}

	meta := absBook.Media.Metadata
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
		narratorNames = nil
	}
	// AddWithMetadataContext performs a best-effort publisher lookup for
	// ordinary mismatch exports. Drafts resolve publisher IDs strictly below,
	// so leave this field blank during collection to guarantee exactly one
	// authoritative publisher request.
	publisherName := meta.Publisher

	record := mismatch.NewCollector().AddWithMetadataContext(
		ctx,
		mismatch.MediaMetadata{
			Title:         meta.Title,
			Subtitle:      meta.Subtitle,
			AuthorName:    authorName,
			NarratorName:  narratorName,
			Publisher:     "",
			PublishedYear: meta.PublishedYear,
			PublishedDate: meta.PublishedDate,
			ISBN:          meta.ISBN,
			ASIN:          meta.ASIN,
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
		hc,
		audnexRegion,
	)
	record.Publisher = publisherName
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := resolveMetadataIDs(ctx, &record, hc, authorNames, narratorNames); err != nil {
		return nil, &UpstreamError{Err: err}
	}
	record.HardcoverBookID = strconv.Itoa(hardcoverBookID)

	if logger.FromContext(ctx) == nil {
		ctx = logger.WithLogger(ctx, logger.Get())
	}
	// Metadata IDs have already been resolved above. Passing a nil client keeps
	// the shared exporter from repeating those lookups (and suppressing errors)
	// while retaining its established field mapping.
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
		AuthorIDs:          nonNilInts(export.AuthorIDs),
		NarratorIDs:        nonNilInts(export.NarratorIDs),
		PublisherID:        export.PublisherID,
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

// resolveMetadataIDs performs the three direct metadata lookups needed by an
// edition draft. A successful empty result means that Hardcover has no match
// and is deliberately not an error; transport, GraphQL, and timeout failures
// are returned so the caller can report an upstream failure instead.
func resolveMetadataIDs(ctx context.Context, record *mismatch.BookMismatch, hc hardcover.HardcoverClientInterface, authorNames, narratorNames []string) error {
	if hc == nil {
		return errors.New("hardcover client is required")
	}

	if len(record.AuthorIDs) == 0 && record.Author != "" {
		ids, err := lookupAuthorIDs(ctx, hc, record.Author, authorNames)
		if err != nil {
			return fmt.Errorf("look up author %q: %w", record.Author, err)
		}
		record.AuthorIDs = ids
	}
	if len(record.NarratorIDs) == 0 && record.Narrator != "" {
		ids, err := lookupNarratorIDs(ctx, hc, record.Narrator, narratorNames)
		if err != nil {
			return fmt.Errorf("look up narrator %q: %w", record.Narrator, err)
		}
		record.NarratorIDs = ids
	}
	if record.PublisherID == 0 && record.Publisher != "" {
		id, err := mismatch.LookupPublisherID(ctx, hc, record.Publisher)
		if err != nil {
			return fmt.Errorf("look up publisher %q: %w", record.Publisher, err)
		}
		record.PublisherID = id
	}
	return nil
}

func lookupAuthorIDs(ctx context.Context, hc hardcover.HardcoverClientInterface, legacyName string, exactNames []string) ([]int, error) {
	if len(exactNames) > 0 {
		return mismatch.LookupAuthorIDsExactStrict(ctx, hc, exactNames...)
	}
	return mismatch.LookupAuthorIDsStrict(ctx, hc, legacyName)
}

func lookupNarratorIDs(ctx context.Context, hc hardcover.HardcoverClientInterface, legacyName string, exactNames []string) ([]int, error) {
	if len(exactNames) > 0 {
		return mismatch.LookupNarratorIDsExactStrict(ctx, hc, exactNames...)
	}
	return mismatch.LookupNarratorIDsStrict(ctx, hc, legacyName)
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

// buildWarnings lists conditions the user should know about before creating
// the edition. The first one (no author) makes creation fail validation.
// absLanguage is the Audiobookshelf item's free-text metadata.language field.
func (d *Draft) buildWarnings(absLanguage string) []string {
	warnings := []string{}
	if len(d.AuthorIDs) == 0 {
		if d.AuthorNames == "" {
			warnings = append(warnings, "The Audiobookshelf item has no author, and an author is required to create an edition.")
		} else {
			warnings = append(warnings, fmt.Sprintf("No Hardcover author matched %q, and an author is required to create an edition.", d.AuthorNames))
		}
	}
	if d.ReleaseDate == "" {
		warnings = append(warnings, "No release date is available, so the edition will have none.")
	}
	if d.PublisherID == 0 && d.PublisherName != "" {
		warnings = append(warnings, fmt.Sprintf("Publisher %q was not found on Hardcover, so the edition will have no publisher.", d.PublisherName))
	}
	if len(d.NarratorIDs) == 0 && d.ReadingFormat != models.ReadingFormatEbook {
		if d.NarratorNames == "" {
			warnings = append(warnings, "The Audiobookshelf item lists no narrator, so the edition will have none.")
		} else {
			warnings = append(warnings, fmt.Sprintf("No Hardcover narrator matched %q, so the edition will have none.", d.NarratorNames))
		}
	}
	if d.ISBN10Valid != nil && !*d.ISBN10Valid {
		warnings = append(warnings, fmt.Sprintf("ISBN-10 %q has an incorrect check digit; Hardcover will still store it as given.", d.ISBN10))
	}
	if d.ISBN13Valid != nil && !*d.ISBN13Valid {
		warnings = append(warnings, fmt.Sprintf("ISBN-13 %q has an incorrect check digit; Hardcover will still store it as given.", d.ISBN13))
	}
	if lang := strings.TrimSpace(absLanguage); lang != "" && !isEnglishLabel(lang) {
		warnings = append(warnings, fmt.Sprintf("The Audiobookshelf item is tagged %q, but the draft defaults to language 1 (English) and country 1 (United States); set language_id and country_id explicitly when creating the edition if that is wrong.", lang))
	}
	return warnings
}

func isEnglishLabel(language string) bool {
	label := strings.ToLower(strings.TrimSpace(language))
	if label == "english" || label == "en" {
		return true
	}
	if strings.HasPrefix(label, "en-") || strings.HasPrefix(label, "en_") {
		return true
	}
	return strings.HasPrefix(label, "english (") && strings.HasSuffix(label, ")")
}

func nonNilInts(ids []int) []int {
	if ids == nil {
		return []int{}
	}
	return ids
}
