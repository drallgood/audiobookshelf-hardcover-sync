// Package draft builds a Hardcover edition draft from an Audiobookshelf item.
//
// It lives beside, not inside, package edition because it reuses the mismatch
// pipeline (mismatch imports the Hardcover client, which imports edition).
//
// Two crosswalk findings are deliberately left open in this step rather than
// implemented, because closing them touches the shared mismatch/export
// pipeline (finding 2) or needs a live-API check this step cannot make
// (finding 9):
//   - Finding 2 (exact author/narrator arrays): the Audiobookshelf item's
//     expanded response carries authors[]/narrators[] with exact names, but
//     this draft still goes through mismatch.AddWithMetadata's joined-string
//     splitting (Author/Narrator, comma-separated), matching the mismatch
//     export's existing behavior. Using the exact arrays instead would need
//     new MediaMetadata fields threaded through the shared export pipeline,
//     which step 1 pins as otherwise unchanged; left for a future step.
//   - Finding 9 (exact, case-sensitive Hardcover person/publisher matching):
//     an author/narrator/publisher name that differs by punctuation or
//     spelling from Hardcover's stored name will not match. The draft's
//     warnings (buildWarnings) are the minimum mitigation this step commits
//     to; a looser lookup needs verification against the hosted API before
//     it can be adopted (see AGENTS.md), and a UI remedy is step 7's decision.
package draft

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	CoverURL           string   `json:"cover_url"`
	DryRun             bool     `json:"dry_run"`
	Warnings           []string `json:"warnings"`
}

// CoverURL returns the server-controlled Audiobookshelf cover URL for an item,
// or "" when the item has no cover or no usable base URL. It is the only image
// URL a draft may carry, because the edition creator can attach the
// Audiobookshelf token to the image download.
func CoverURL(absBaseURL string, absBook models.AudiobookshelfBook) string {
	if absBook.ID == "" || absBook.Media.CoverPath == "" {
		return ""
	}
	base, err := url.Parse(strings.TrimSpace(absBaseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return ""
	}
	// Never carry credentials, query data, or a fragment into the image URL.
	base.User = nil
	base.RawQuery = ""
	base.Fragment = ""
	return fmt.Sprintf("%s/api/items/%s/cover", strings.TrimRight(base.String(), "/"), url.PathEscape(absBook.ID))
}

// New builds a draft for absBook using the existing mismatch export pipeline.
// hardcoverBookID is the book the edition will attach to; it always overrides
// whatever Hardcover candidate enrichment guessed. Author, narrator, and
// publisher IDs are resolved through hc, so this issues Hardcover read queries.
func New(ctx context.Context, absBook models.AudiobookshelfBook, hardcoverBookID int, absBaseURL string, hc hardcover.HardcoverClientInterface, audnexRegion string) (*Draft, error) {
	if hc == nil {
		return nil, errors.New("hardcover client is required")
	}
	if hardcoverBookID <= 0 {
		return nil, errors.New("hardcover book id is required")
	}
	if absBook.ID == "" {
		return nil, errors.New("audiobookshelf item id is required")
	}

	coverURL := CoverURL(absBaseURL, absBook)
	meta := absBook.Media.Metadata
	readingFormat := absBook.ReadingFormat()
	ebook := readingFormat == models.ReadingFormatEbook
	narrator := meta.NarratorName
	if ebook {
		// Narrators and audio length only apply to audiobooks.
		narrator = ""
	}

	record := mismatch.NewCollector().AddWithMetadata(
		mismatch.MediaMetadata{
			Title:         meta.Title,
			Subtitle:      meta.Subtitle,
			AuthorName:    meta.AuthorName,
			NarratorName:  narrator,
			Publisher:     meta.Publisher,
			PublishedYear: meta.PublishedYear,
			PublishedDate: meta.PublishedDate,
			ISBN:          meta.ISBN,
			ASIN:          meta.ASIN,
			CoverURL:      coverURL,
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
	record.HardcoverBookID = strconv.Itoa(hardcoverBookID)

	if logger.FromContext(ctx) == nil {
		ctx = logger.WithLogger(ctx, logger.Get())
	}
	export := record.ToEditionExport(ctx, hc)
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
		CoverURL:           coverURL,
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
	if lang := strings.TrimSpace(absLanguage); lang != "" && !strings.Contains(strings.ToLower(lang), "english") {
		warnings = append(warnings, fmt.Sprintf("The Audiobookshelf item is tagged %q, but the draft defaults to language 1 (English) and country 1 (United States); set language_id and country_id explicitly when creating the edition if that is wrong.", lang))
	}
	return warnings
}

func nonNilInts(ids []int) []int {
	if ids == nil {
		return []int{}
	}
	return ids
}
