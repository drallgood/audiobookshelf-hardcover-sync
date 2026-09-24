package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// Keep the full source read and region discovery below the server's 30 second
// write timeout, leaving time to serialize the response.
const defaultEditionDraftRequestTimeout = 25 * time.Second

type editionDraftAudnexDiscoverer interface {
	DiscoverBookByASIN(ctx context.Context, asin, preferredRegion string) (*audnex.Book, string, error)
}

type editionDraftResponse struct {
	ABSItemID                  string                      `json:"abs_item_id"`
	ReadingFormat              string                      `json:"reading_format"`
	DryRun                     bool                        `json:"dry_run"`
	Eligible                   bool                        `json:"eligible"`
	IneligibleReason           string                      `json:"ineligible_reason,omitempty"`
	SourceIdentifiers          sourceIdentifiers           `json:"source_identifiers"`
	RegionStatus               string                      `json:"region_status,omitempty"`
	ConfirmedRegion            string                      `json:"confirmed_region,omitempty"`
	AudibleIdentifierCandidate *audibleIdentifierCandidate `json:"audible_identifier_candidate,omitempty"`
	MetadataPreview            *audiobookMetadataPreview   `json:"metadata_preview,omitempty"`
	EbookCandidate             *ebookEditionCandidate      `json:"ebook_candidate,omitempty"`
	Warnings                   []editionDraftWarning       `json:"warnings,omitempty"`
}

type sourceIdentifiers struct {
	ASIN string `json:"asin,omitempty"`
	ISBN string `json:"isbn,omitempty"`
}

type audibleIdentifierCandidate struct {
	ASIN              string `json:"asin"`
	Region            string `json:"region"`
	CorrectionAllowed bool   `json:"correction_allowed"`
}

type editionDraftISBNFields struct {
	ISBN10      string `json:"isbn_10,omitempty"`
	ISBN13      string `json:"isbn_13,omitempty"`
	ISBN10Valid *bool  `json:"isbn_10_valid,omitempty"`
	ISBN13Valid *bool  `json:"isbn_13_valid,omitempty"`
}

type audiobookMetadataPreview struct {
	Title              string `json:"title"`
	Subtitle           string `json:"subtitle,omitempty"`
	Author             string `json:"author,omitempty"`
	Narrator           string `json:"narrator,omitempty"`
	Publisher          string `json:"publisher,omitempty"`
	Description        string `json:"description,omitempty"`
	Language           string `json:"language,omitempty"`
	ReleaseDate        string `json:"release_date,omitempty"`
	EditionFormat      string `json:"edition_format,omitempty"`
	EditionInformation string `json:"edition_information"`
	AudioSeconds       int    `json:"audio_seconds"`
	editionDraftISBNFields
}

type ebookEditionCandidate struct {
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle,omitempty"`
	Author        string `json:"author,omitempty"`
	ASIN          string `json:"asin,omitempty"`
	ReleaseDate   string `json:"release_date,omitempty"`
	EditionFormat string `json:"edition_format"`
	CorrectedISBN string `json:"corrected_isbn"`
	editionDraftISBNFields
}

type editionDraftWarning struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

// GetEditionSourceDraft handles GET /api/profiles/{id}/edition-drafts/source/{itemID}.
// It reads only Audiobookshelf and Audnex; Hardcover is not constructed or queried.
func (h *Handler) GetEditionSourceDraft(w http.ResponseWriter, r *http.Request) {
	parentCtx := r.Context()
	requestTimeout := h.editionDraftRequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultEditionDraftRequestTimeout
	}
	draftCtx, cancel := context.WithTimeout(parentCtx, requestTimeout)
	defer cancel()
	r = r.WithContext(draftCtx)

	profileID := profileIDFromRequest(r)
	itemID := strings.TrimSpace(r.PathValue("itemID"))
	if profileID == "" || itemID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID and Audiobookshelf item ID are required")
		return
	}
	if _, authorized := h.authorizeProfileMetadata(w, r, profileID, false); !authorized {
		return
	}
	profile, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.log.Error("Failed to retrieve profile for edition source draft")
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profile")
		return
	}
	if profile == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		return
	}

	absClient := audiobookshelf.NewClient(profile.AudiobookshelfURL, profile.AudiobookshelfToken)
	book, err := absClient.GetLibraryItemByID(r.Context(), itemID)
	if err != nil {
		if parentCtx.Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			h.writeErrorResponse(w, http.StatusGatewayTimeout, "Timed out retrieving Audiobookshelf item")
			return
		}
		h.log.Error("Failed to retrieve Audiobookshelf item for edition source draft: " + err.Error())
		h.writeErrorResponse(w, http.StatusBadGateway, "Failed to retrieve Audiobookshelf item")
		return
	}
	if parentCtx.Err() != nil {
		return
	}

	draft := buildEditionSourceDraft(book, profile.SyncConfig.DryRun)
	if !book.IsEbook() && draft.SourceIdentifiers.ASIN != "" {
		lookupASIN, validASIN := audnex.CanonicalASIN(draft.SourceIdentifiers.ASIN)
		if !validASIN {
			draft.RegionStatus = "unknown"
			draft.addWarning("invalid_source_asin", "Audiobookshelf source ASIN is malformed; Audnex region discovery was skipped.", false)
		} else {
			preferredRegion, supported := supportedAudnexPreference(profile.SyncConfig.AudnexusRegion)
			if strings.TrimSpace(profile.SyncConfig.AudnexusRegion) != "" && !supported {
				draft.addWarning("unsupported_audnex_region", "The saved Audnex region is unsupported; region discovery is using US.", false)
			}
			var discovery editionDraftAudnexDiscoverer = audnex.NewClient(&h.log)
			if h.editionDraftAudnexClientFactory != nil {
				discovery = h.editionDraftAudnexClientFactory()
			}
			found, region, discoverErr := discovery.DiscoverBookByASIN(r.Context(), lookupASIN, preferredRegion)
			if parentCtx.Err() != nil {
				return
			}
			returnedASIN, validReturnedASIN := "", false
			if found != nil {
				returnedASIN, validReturnedASIN = audnex.CanonicalASIN(found.ASIN)
			}
			switch {
			case errors.Is(discoverErr, context.DeadlineExceeded), errors.Is(discoverErr, audnex.ErrRateLimited), errors.Is(discoverErr, audnex.ErrTransient):
				draft.RegionStatus = "temporarily_unavailable"
				draft.addWarning("audnex_temporarily_unavailable", "Audnex region discovery is temporarily unavailable. Retry to check the source ASIN.", true)
			case discoverErr != nil:
				h.log.Error("Failed to discover Audnex region for edition source draft: " + discoverErr.Error())
				h.writeErrorResponse(w, http.StatusBadGateway, "Failed to retrieve Audnex source metadata")
				return
			case validReturnedASIN && returnedASIN == lookupASIN && audnexregion.IsRegion(region):
				draft.RegionStatus = "confirmed"
				draft.ConfirmedRegion = region
				draft.AudibleIdentifierCandidate.Region = region
				if found.ReleaseDate != "" {
					if date, ok := normalizeDraftDate(found.ReleaseDate); ok {
						draft.MetadataPreview.ReleaseDate = date
					} else {
						draft.addWarning("audnex_date_unrecognized", "Audnex returned a release date that could not be normalized; the Audiobookshelf date is shown instead.", false)
					}
				}
			default:
				draft.RegionStatus = "unknown"
			}
		}
	} else if !book.IsEbook() {
		draft.RegionStatus = "not_applicable"
	}

	h.writeSuccessResponse(w, draft)
}

func buildEditionSourceDraft(book *models.AudiobookshelfBook, dryRun bool) *editionDraftResponse {
	metadata := book.Media.Metadata
	readingFormat := book.ReadingFormat()
	asin := strings.TrimSpace(metadata.ASIN)
	rawISBN := strings.TrimSpace(metadata.ISBN)
	identifiers := sourceIdentifiers{ASIN: asin, ISBN: rawISBN}
	date, dateOK := fallbackDraftDate(metadata.PublishedDate, metadata.PublishedYear)
	isbnFields, isbnOK := draftISBNFields(rawISBN)
	author := strings.TrimSpace(metadata.AuthorName)
	_, usableASIN := audnex.CanonicalASIN(asin)
	draft := &editionDraftResponse{
		ABSItemID:         book.ID,
		ReadingFormat:     readingFormat,
		DryRun:            dryRun,
		Eligible:          usableASIN || isbnOK,
		SourceIdentifiers: identifiers,
	}
	if !book.IsEbook() {
		draft.AudibleIdentifierCandidate = &audibleIdentifierCandidate{
			ASIN: asin, CorrectionAllowed: true,
		}
	}
	if asin != "" && !usableASIN {
		message := "Audiobookshelf source ASIN is malformed; it is not usable as an identifier."
		if !book.IsEbook() {
			message = "Audiobookshelf source ASIN is malformed; it is not usable as an identifier and Audnex region discovery is skipped."
		}
		draft.addWarning("invalid_source_asin", message, false)
	}
	if !draft.Eligible {
		draft.IneligibleReason = "The Audiobookshelf item needs a valid ASIN or ISBN before an edition can be added."
		draft.addWarning("missing_identifier", draft.IneligibleReason, false)
	}
	if rawISBN != "" && !isbnOK {
		draft.addWarning("invalid_isbn", "The source ISBN is not a 10- or 13-character ISBN; correct it before adding an edition if no ASIN is present.", false)
	} else if rawISBN != "" && hasInvalidISBNChecksum(isbnFields) {
		draft.addWarning("invalid_isbn", "The source ISBN has an invalid check digit. It remains a candidate, but verify or correct it before adding an edition.", false)
	}
	if !dateOK {
		draft.addWarning("date_unavailable", "Audiobookshelf has no usable publication date or year.", false)
	}
	if strings.TrimSpace(metadata.PublishedDate) != "" {
		if isAmbiguousSlashDate(metadata.PublishedDate) {
			draft.addWarning("published_date_ambiguous", "Audiobookshelf publication date is ambiguous; its published year is the fallback when available.", false)
		} else if _, ok := normalizeDraftDate(metadata.PublishedDate); !ok {
			draft.addWarning("published_date_unrecognized", "Audiobookshelf publication date could not be normalized; its published year is the fallback when available.", false)
		}
	}
	if author == "" {
		draft.addWarning("missing_author", "Audiobookshelf has no author name for this item.", false)
	}
	if !isEnglish(metadata.Language) {
		message := "Audiobookshelf language is shown for reference; the edition draft uses English."
		if strings.TrimSpace(metadata.Language) == "" {
			message = "Audiobookshelf does not specify a language; the edition draft uses English."
		}
		draft.addWarning("language_defaults_to_english", message, false)
	}

	if book.IsEbook() {
		draft.EbookCandidate = &ebookEditionCandidate{
			Title: strings.TrimSpace(metadata.Title), Subtitle: strings.TrimSpace(metadata.Subtitle),
			Author: author, ASIN: asin, ReleaseDate: date, EditionFormat: "Ebook",
			CorrectedISBN: "", editionDraftISBNFields: isbnFields,
		}
		return draft
	}

	editionInformation := "Unabridged"
	if metadata.Abridged {
		editionInformation = "Abridged"
	}
	seconds := int(book.Media.Duration + 0.5)
	editionFormat := ""
	if asin != "" {
		editionFormat = "Audible Audio"
	} else if strings.Contains(strings.ToLower(metadata.Publisher), "libro") {
		editionFormat = "libro.fm"
	}
	draft.MetadataPreview = &audiobookMetadataPreview{
		Title: strings.TrimSpace(metadata.Title), Subtitle: strings.TrimSpace(metadata.Subtitle),
		Author: author, Narrator: strings.TrimSpace(metadata.NarratorName),
		Publisher: strings.TrimSpace(metadata.Publisher), Description: strings.TrimSpace(metadata.Description),
		Language: strings.TrimSpace(metadata.Language), ReleaseDate: date,
		EditionFormat: editionFormat, EditionInformation: editionInformation,
		AudioSeconds: seconds, editionDraftISBNFields: isbnFields,
	}
	return draft
}

func (d *editionDraftResponse) addWarning(code, message string, retryable bool) {
	d.Warnings = append(d.Warnings, editionDraftWarning{Code: code, Message: message, Retryable: retryable})
}

func draftISBNFields(raw string) (editionDraftISBNFields, bool) {
	parsed, ok := isbn.Parse(raw)
	if !ok {
		return editionDraftISBNFields{}, false
	}
	fields := editionDraftISBNFields{ISBN10: parsed.ISBN10(), ISBN13: parsed.ISBN13()}
	if fields.ISBN10 != "" {
		valid := parsed.Valid
		if parsed.Is13 {
			valid = true // A counterpart exists only when the source ISBN-13 checksum is valid.
		}
		fields.ISBN10Valid = &valid
	}
	if fields.ISBN13 != "" {
		valid := parsed.Valid
		if !parsed.Is13 {
			valid = true // A counterpart exists only when the source ISBN-10 checksum is valid.
		}
		fields.ISBN13Valid = &valid
	}
	return fields, true
}

func hasInvalidISBNChecksum(fields editionDraftISBNFields) bool {
	return fields.ISBN10Valid != nil && !*fields.ISBN10Valid ||
		fields.ISBN13Valid != nil && !*fields.ISBN13Valid
}

func fallbackDraftDate(publishedDate, publishedYear string) (string, bool) {
	if date, ok := normalizeDraftDate(publishedDate); ok {
		return date, true
	}
	return normalizeDraftDate(publishedYear)
}

func normalizeDraftDate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	if isAmbiguousSlashDate(value) {
		return "", false
	}
	if len(value) == 4 {
		if _, err := time.Parse("2006", value); err == nil {
			return value + "-01-01", true
		}
	}
	layouts := []string{
		"2006-01-02", time.RFC3339, "2006-01-02T15:04:05.000Z07:00",
		"2006-01-02 15:04:05", "2006/01/02", "01/02/2006", "02/01/2006",
		"Jan 2, 2006", "January 2, 2006", "2 Jan 2006", "2 January 2006",
		"2006-01", "January 2006", "Jan 2006",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("2006-01-02"), true
		}
	}
	return "", false
}

func isAmbiguousSlashDate(raw string) bool {
	value := strings.TrimSpace(raw)
	if !strings.Contains(value, "/") {
		return false
	}
	_, monthFirstErr := time.Parse("01/02/2006", value)
	_, dayFirstErr := time.Parse("02/01/2006", value)
	return monthFirstErr == nil && dayFirstErr == nil
}

func supportedAudnexPreference(raw string) (string, bool) {
	region := strings.ToLower(strings.TrimSpace(raw))
	if region == "" {
		return "us", true
	}
	if audnexregion.IsRegion(region) {
		return region, true
	}
	return "us", false
}

func isEnglish(raw string) bool {
	language := strings.ToLower(strings.TrimSpace(raw))
	return language == "en" || language == "eng" || language == "english" ||
		strings.HasPrefix(language, "en-") || strings.HasPrefix(language, "en_")
}
