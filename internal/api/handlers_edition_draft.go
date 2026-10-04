package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// Keep the full source read and region discovery below the server's 30 second
// write timeout, leaving time to serialize the response.
const defaultEditionDraftRequestTimeout = 25 * time.Second

type editionDraftAudnexDiscoverer interface {
	GetBookByASIN(ctx context.Context, asin, region string) (*audnex.Book, error)
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
	SourceMetadataPreview      *audiobookMetadataPreview   `json:"source_metadata_preview,omitempty"`
	AudnexusDetails            *audnexusDetails            `json:"audnexus_details,omitempty"`
	AudnexusRecord             *edition.AudnexusRecord     `json:"audnexus_record,omitempty"`
	AudnexusComparison         *edition.AudnexusComparison `json:"audnexus_comparison,omitempty"`
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
	Series             string `json:"series,omitempty"`
	SeriesPosition     string `json:"series_position,omitempty"`
	CoverURL           string `json:"cover_url,omitempty"`
	editionDraftISBNFields
}

type audnexusDetails struct {
	Title       string `json:"title"`
	Author      string `json:"author,omitempty"`
	Narrator    string `json:"narrator,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`
	FormatType  string `json:"format_type,omitempty"`
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
// It reads only Audiobookshelf and Audnexus; Hardcover is not constructed or queried.
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
	select {
	case h.editionDraftSlots <- struct{}{}:
		defer func() { <-h.editionDraftSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		h.writeErrorResponse(w, http.StatusTooManyRequests, "Edition draft service is busy; retry shortly")
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

	absClient, err := audiobookshelf.NewClientWithNetworkTrust(
		profile.AudiobookshelfURL, profile.AudiobookshelfToken, h.multiUserService.AudiobookshelfNetworkTrust(),
	)
	if err != nil {
		h.log.Error("Invalid Audiobookshelf client configuration for edition source draft: " + err.Error())
		h.writeErrorResponse(w, http.StatusConflict, "Saved Audiobookshelf URL is not permitted by the current network trust policy; update the profile URL")
		return
	}
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
	mediaType := strings.ToLower(strings.TrimSpace(book.MediaType))
	if mediaType != "book" && mediaType != "ebook" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Audiobookshelf item must be a book")
		return
	}

	draft := buildEditionSourceDraft(book, profile.SyncConfig.DryRun)
	correctedIdentifier := strings.TrimSpace(r.URL.Query().Get("audible_identifier"))
	if book.IsEbook() && correctedIdentifier != "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "audible_identifier is only valid for an audiobook")
		return
	}
	if !book.IsEbook() {
		if correctedIdentifier != "" {
			asin, region, parseErr := parseSubmittedAudibleIdentifier(correctedIdentifier)
			if parseErr != nil {
				h.writeErrorResponse(w, http.StatusBadRequest, parseErr.Error())
				return
			}
			draft.AudibleIdentifierCandidate.ASIN = asin
			draft.AudibleIdentifierCandidate.Region = region
			draft.lookupAudnexusRecord(r.Context(), parentCtx, h, editionDraftAudnexClient(h), book, asin, region, true)
		} else if lookupASIN, validASIN := audnex.CanonicalASIN(draft.SourceIdentifiers.ASIN); validASIN {
			preference := h.multiUserService.ProfileAudnexusRegion(profile.SyncConfig)
			preferredRegion, supported := supportedAudnexPreference(preference)
			if strings.TrimSpace(preference) != "" && !supported {
				draft.addWarning("unsupported_audnex_region", "The saved Audnexus region is unsupported; region discovery is using US.", false)
			}
			draft.lookupAudnexusRecord(r.Context(), parentCtx, h, editionDraftAudnexClient(h), book, lookupASIN, preferredRegion, false)
		} else {
			draft.RegionStatus = "not_applicable"
		}
	}
	if parentCtx.Err() != nil {
		return
	}
	if !book.IsEbook() && draft.MetadataPreview != nil {
		coverURL := audiobookshelfDraftCoverURL(profile.AudiobookshelfURL, book.ID)
		draft.MetadataPreview.CoverURL = coverURL
		if draft.SourceMetadataPreview != nil {
			draft.SourceMetadataPreview.CoverURL = coverURL
		}
	}

	h.writeSuccessResponse(w, draft)
}

func editionDraftAudnexClient(h *Handler) editionDraftAudnexDiscoverer {
	if h.editionDraftAudnexClientFactory != nil {
		return h.editionDraftAudnexClientFactory()
	}
	return audnex.NewClient(&h.log)
}

func audiobookshelfDraftCoverURL(baseURL, itemID string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || itemID == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/items/" + url.PathEscape(itemID) + "/cover"
	parsed.RawPath = ""
	return parsed.String()
}

func (draft *editionDraftResponse) lookupAudnexusRecord(ctx, parentCtx context.Context, h *Handler, client editionDraftAudnexDiscoverer, abs *models.AudiobookshelfBook, asin, region string, exactRegion bool) {
	var found *audnex.Book
	confirmedRegion := region
	var err error
	if exactRegion {
		found, err = client.GetBookByASIN(ctx, asin, region)
	} else {
		found, confirmedRegion, err = client.DiscoverBookByASIN(ctx, asin, region)
	}
	if parentCtx.Err() != nil {
		return
	}
	returnedASIN, validReturnedASIN := "", false
	if found != nil {
		returnedASIN, validReturnedASIN = audnex.CanonicalASIN(found.ASIN)
	}
	switch {
	case errors.Is(err, audnex.ErrNotFound):
		draft.RegionStatus = "unknown"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, audnex.ErrRateLimited), errors.Is(err, audnex.ErrTransient):
		draft.RegionStatus = "temporarily_unavailable"
		draft.addWarning("audnex_temporarily_unavailable", "Audnexus lookup is temporarily unavailable. Retry to check the regional Audible identifier.", true)
	case err != nil:
		h.log.Error("Failed to retrieve Audnexus record for edition source draft: " + err.Error())
		draft.RegionStatus = "unknown"
		draft.addWarning("audnex_lookup_failed", "Audnexus did not confirm this regional Audible identifier.", false)
	case validReturnedASIN && returnedASIN == asin && audnexregion.IsRegion(strings.ToLower(strings.TrimSpace(confirmedRegion))):
		confirmedRegion = strings.ToLower(strings.TrimSpace(confirmedRegion))
		draft.RegionStatus = "confirmed"
		draft.ConfirmedRegion = confirmedRegion
		if draft.AudibleIdentifierCandidate != nil {
			if exactRegion {
				draft.AudibleIdentifierCandidate.ASIN = asin
			}
			draft.AudibleIdentifierCandidate.Region = confirmedRegion
		}
		draft.AudnexusDetails = buildAudnexusDetails(found)
		record := edition.BuildAudnexusRecord(found)
		if strings.TrimSpace(found.ReleaseDate) != "" {
			if draft.AudnexusDetails.ReleaseDate != "" {
				draft.MetadataPreview.ReleaseDate = draft.AudnexusDetails.ReleaseDate
			} else {
				draft.addWarning("audnex_date_unrecognized", "Audnexus returned a release date that could not be normalized; the Audiobookshelf date is shown instead.", false)
			}
		}
		draft.AudnexusRecord = &record
		comparison := edition.CompareAudnexus(abs, record)
		draft.AudnexusComparison = &comparison
	default:
		draft.RegionStatus = "unknown"
	}
}

func buildAudnexusDetails(book *audnex.Book) *audnexusDetails {
	details := &audnexusDetails{
		Title:      strings.TrimSpace(book.Title),
		Author:     joinAudnexNames(book.GetAuthorsAsStrings()),
		Narrator:   joinAudnexNames(book.GetNarratorsAsStrings()),
		FormatType: strings.TrimSpace(book.FormatType),
	}
	if releaseDate, ok := edition.NormalizeAudnexusDate(book.ReleaseDate); ok {
		details.ReleaseDate = releaseDate
	}
	return details
}

func joinAudnexNames(values []string) string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		if name := strings.TrimSpace(value); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
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
	series, position := "", ""
	if !book.IsEbook() {
		series, position = edition.AudiobookshelfSeriesFields(metadata)
		draft.AudibleIdentifierCandidate = &audibleIdentifierCandidate{
			ASIN: asin, CorrectionAllowed: true,
		}
	}
	if asin != "" && !usableASIN {
		message := "Audiobookshelf source ASIN is malformed; it is not usable as an identifier."
		if !book.IsEbook() {
			message = "Audiobookshelf source ASIN is malformed; it is not usable as an identifier and Audnexus region discovery is skipped."
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
		} else if _, ok := edition.NormalizeAudnexusDate(metadata.PublishedDate); !ok {
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
	if usableASIN {
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
		Series: series, SeriesPosition: position,
	}
	sourceMetadataPreview := *draft.MetadataPreview
	sourceMetadataPreview.ReleaseDate = sourceDraftDate(metadata.PublishedDate, metadata.PublishedYear)
	draft.SourceMetadataPreview = &sourceMetadataPreview
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
	if date, ok := edition.NormalizeAudnexusDate(publishedDate); ok {
		return date, true
	}
	return edition.NormalizeAudnexusDate(publishedYear)
}

func sourceDraftDate(publishedDate, publishedYear string) string {
	if value := strings.TrimSpace(publishedDate); value != "" {
		return value
	}
	return strings.TrimSpace(publishedYear)
}

func isAmbiguousSlashDate(raw string) bool {
	value := strings.TrimSpace(raw)
	if !strings.Contains(value, "/") {
		return false
	}
	monthFirst, monthFirstErr := time.Parse("01/02/2006", value)
	dayFirst, dayFirstErr := time.Parse("02/01/2006", value)
	return monthFirstErr == nil && dayFirstErr == nil && !monthFirst.Equal(dayFirst)
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
