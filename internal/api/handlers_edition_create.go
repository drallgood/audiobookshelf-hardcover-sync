package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

const editionCreateRequestTimeout = 25 * time.Second

type editionCreateABSClient interface {
	GetLibraryItemByID(context.Context, string) (*models.AudiobookshelfBook, error)
}

type editionCreateRegionalAudiobookImporter interface {
	ImportRegionalAudiobook(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error)
}

type editionCreateHardcoverClient interface {
	editionCreateRegionalAudiobookImporter
	GetBookByID(context.Context, string) (*models.HardcoverBook, error)
	GetEditionUncached(context.Context, string) (*models.Edition, error)
	SearchAuthors(context.Context, string, int) ([]models.Author, error)
	SearchPublishers(context.Context, string, int) ([]models.Publisher, error)
	CreateEbook(context.Context, *edition.EditionInput) (*edition.EditionResult, error)
}

type editionCreateAudnexDiscoverer interface {
	GetBookByASIN(context.Context, string, string) (*audnex.Book, error)
	DiscoverBookByASIN(context.Context, string, string) (*audnex.Book, string, error)
}

type editionCreateRequest struct {
	RunID             string  `json:"run_id"`
	ABSItemID         string  `json:"abs_item_id"`
	AudibleIdentifier string  `json:"audible_identifier,omitempty"`
	Title             *string `json:"title,omitempty"`
	Subtitle          *string `json:"subtitle,omitempty"`
	ASIN              *string `json:"asin,omitempty"`
	ISBN10            *string `json:"isbn_10,omitempty"`
	ISBN13            *string `json:"isbn_13,omitempty"`
	ReleaseDate       *string `json:"release_date,omitempty"`
	EditionFormat     *string `json:"edition_format,omitempty"`
}

type editionCreateResponse struct {
	ABSItemID          string                    `json:"abs_item_id"`
	ReadingFormat      string                    `json:"reading_format"`
	Status             string                    `json:"status"`
	HardcoverBookID    string                    `json:"hardcover_book_id"`
	HardcoverEditionID string                    `json:"hardcover_edition_id"`
	RegionalExternalID string                    `json:"regional_external_id,omitempty"`
	MetadataPreview    *audiobookMetadataPreview `json:"metadata_preview,omitempty"`
}

type editionCreateOutcome struct {
	response editionCreateResponse
}

type editionCreateHardcoverAdapter struct {
	client *hardcover.Client
	log    *logger.Logger
}

func (c editionCreateHardcoverAdapter) ImportRegionalAudiobook(ctx context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
	return c.client.ImportRegionalAudiobook(ctx, input)
}

func (c editionCreateHardcoverAdapter) GetBookByID(ctx context.Context, id string) (*models.HardcoverBook, error) {
	return c.client.GetBookByID(ctx, id)
}

func (c editionCreateHardcoverAdapter) GetEditionUncached(ctx context.Context, id string) (*models.Edition, error) {
	return c.client.GetEditionUncached(ctx, id)
}

func (c editionCreateHardcoverAdapter) SearchAuthors(ctx context.Context, name string, limit int) ([]models.Author, error) {
	return c.client.SearchAuthors(ctx, name, limit)
}

func (c editionCreateHardcoverAdapter) SearchPublishers(ctx context.Context, name string, limit int) ([]models.Publisher, error) {
	return c.client.SearchPublishers(ctx, name, limit)
}

func (c editionCreateHardcoverAdapter) CreateEbook(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
	creator := edition.NewCreator(c.client, c.log, false, "")
	return creator.CreateEdition(ctx, input)
}

// CreateEditionFromDraft handles POST /api/profiles/{id}/edition-drafts/create.
// The run and item IDs identify the exact completed source snapshot; the book
// ID always comes from that verified snapshot, never from the request.
func (h *Handler) CreateEditionFromDraft(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), editionCreateRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, true) {
		return
	}

	select {
	case h.editionDraftSlots <- struct{}{}:
		defer func() { <-h.editionDraftSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		h.writeErrorResponse(w, http.StatusTooManyRequests, "Edition creation service is busy; retry shortly")
		return
	}

	request, err := decodeEditionCreateRequest(w, r)
	if err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	request.RunID = strings.TrimSpace(request.RunID)
	request.ABSItemID = strings.TrimSpace(request.ABSItemID)
	if request.RunID == "" || request.ABSItemID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "run_id and abs_item_id are required")
		return
	}

	var outcome editionCreateOutcome
	err = h.multiUserService.CreateEditionWithAssociation(profileID, request.ABSItemID, func(profile *database.ProfileWithTokens) (statepkg.Association, error) {
		snapshot, record, snapshotErr := h.verifiedEditionCreateRecord(profileID, request.RunID, request.ABSItemID)
		if snapshotErr != nil {
			return statepkg.Association{}, snapshotErr
		}
		return h.createVerifiedEdition(ctx, profile, snapshot, record, request, &outcome)
	})
	if err != nil {
		h.writeEditionCreateError(w, profileID, err)
		return
	}
	h.writeSuccessResponse(w, outcome.response)
}

func decodeEditionCreateRequest(w http.ResponseWriter, r *http.Request) (editionCreateRequest, error) {
	var request editionCreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return editionCreateRequest{}, fmt.Errorf("invalid edition create request: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return editionCreateRequest{}, errors.New("invalid edition create request: trailing JSON value")
		}
		return editionCreateRequest{}, fmt.Errorf("invalid edition create request: %w", err)
	}
	return request, nil
}

func (h *Handler) verifiedEditionCreateRecord(profileID, runID, itemID string) (*sync.SyncSnapshot, sync.BookOutcomeRecord, error) {
	snapshot, err := h.multiUserService.GetSyncRunSnapshot(profileID, runID)
	if err != nil {
		return nil, sync.BookOutcomeRecord{}, fmt.Errorf("failed to retrieve requested sync run: %w", err)
	}
	if snapshot == nil || snapshot.RunID != runID ||
		(snapshot.ProfileID != "" && snapshot.ProfileID != profileID) ||
		snapshot.State != string(sync.RunPhaseCompleted) || snapshot.DryRun {
		return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
	}
	for _, record := range snapshot.BookOutcomes {
		if record.BookID != itemID {
			continue
		}
		if record.Outcome != sync.OutcomeNeedsReview || strings.TrimSpace(record.HardcoverBookID) == "" ||
			strings.TrimSpace(record.Format) == "" {
			return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
		}
		if _, err := strconv.Atoi(record.HardcoverBookID); err != nil {
			return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
		}
		laterRecord, found, laterErr := h.multiUserService.GetLaterCompletedSyncRunOutcome(profileID, runID, itemID)
		if laterErr != nil {
			return nil, sync.BookOutcomeRecord{}, fmt.Errorf("failed to inspect newer sync outcomes: %w", laterErr)
		}
		if found && !sameEditionCreateCandidate(record, laterRecord) {
			return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
		}
		return snapshot, record, nil
	}
	return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
}

func sameEditionCreateCandidate(requested, latest sync.BookOutcomeRecord) bool {
	return latest.BookID == requested.BookID && latest.Outcome == sync.OutcomeNeedsReview &&
		strings.TrimSpace(latest.HardcoverBookID) == strings.TrimSpace(requested.HardcoverBookID) &&
		normalizedEditionCreateFormat(latest.Format) == normalizedEditionCreateFormat(requested.Format) &&
		normalizeEditionCreateName(latest.Title) == normalizeEditionCreateName(requested.Title) &&
		normalizeEditionCreateName(latest.Author) == normalizeEditionCreateName(requested.Author) &&
		normalizeEditionCreateASIN(latest.ASIN) == normalizeEditionCreateASIN(requested.ASIN) &&
		isbn.Normalize(latest.ISBN) == isbn.Normalize(requested.ISBN)
}

var errStaleEditionCreateRun = errors.New("sync run no longer contains a usable needs-review source record")
var errEditionCreateSourceChanged = errors.New("Audiobookshelf source data or reading format changed; run a new sync before adding an edition")
var errEditionCreateInvalidInput = errors.New("invalid edition create input")

func (h *Handler) createVerifiedEdition(ctx context.Context, profile *database.ProfileWithTokens, snapshot *sync.SyncSnapshot, record sync.BookOutcomeRecord, request editionCreateRequest, outcome *editionCreateOutcome) (statepkg.Association, error) {
	if profile.SyncConfig.DryRun {
		return statepkg.Association{}, multiuser.ErrEditionCreateDryRun
	}
	absClient, err := h.editionCreateABSClient(profile.AudiobookshelfURL, profile.AudiobookshelfToken, h.multiUserService.AudiobookshelfNetworkTrust())
	if err != nil {
		return statepkg.Association{}, fmt.Errorf("invalid Audiobookshelf client configuration: %w", err)
	}
	item, err := absClient.GetLibraryItemByID(ctx, request.ABSItemID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return statepkg.Association{}, fmt.Errorf("Audiobookshelf item lookup timed out: %w", err)
		}
		return statepkg.Association{}, fmt.Errorf("failed to retrieve Audiobookshelf item: %w", err)
	}
	mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
	if mediaType != "book" && mediaType != "ebook" {
		return statepkg.Association{}, fmt.Errorf("%w: Audiobookshelf item must be a book", errEditionCreateInvalidInput)
	}
	if !editionCreateSourceMatches(record, item) {
		return statepkg.Association{}, errEditionCreateSourceChanged
	}
	if request.hasAudiobookMetadataCorrection() && item.ReadingFormat() == models.ReadingFormatAudiobook {
		return statepkg.Association{}, fmt.Errorf("%w: audiobook metadata is preview-only; only audible_identifier may be corrected", errEditionCreateInvalidInput)
	}
	if request.AudibleIdentifier != "" && item.ReadingFormat() != models.ReadingFormatAudiobook {
		return statepkg.Association{}, fmt.Errorf("%w: audible_identifier is only valid for an audiobook", errEditionCreateInvalidInput)
	}

	client := h.editionCreateHardcoverClient(profile.HardcoverToken)
	if item.ReadingFormat() == models.ReadingFormatAudiobook {
		return h.createRegionalAudiobook(ctx, profile, item, record, request, client, outcome)
	}
	return h.createEbook(ctx, item, record, request, client, outcome)
}

func (r editionCreateRequest) hasAudiobookMetadataCorrection() bool {
	return r.Title != nil || r.Subtitle != nil || r.ASIN != nil || r.ISBN10 != nil || r.ISBN13 != nil || r.ReleaseDate != nil || r.EditionFormat != nil
}

func editionCreateSourceMatches(record sync.BookOutcomeRecord, item *models.AudiobookshelfBook) bool {
	if item == nil || item.ID != record.BookID || normalizedEditionCreateFormat(record.Format) != item.ReadingFormat() {
		return false
	}
	return normalizeEditionCreateName(record.Title) == normalizeEditionCreateName(item.Media.Metadata.Title) &&
		normalizeEditionCreateName(record.Author) == normalizeEditionCreateName(item.Media.Metadata.AuthorName) &&
		normalizeEditionCreateASIN(record.ASIN) == normalizeEditionCreateASIN(item.Media.Metadata.ASIN) &&
		isbn.Normalize(record.ISBN) == isbn.Normalize(item.Media.Metadata.ISBN)
}

func normalizedEditionCreateFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "audiobook":
		return models.ReadingFormatAudiobook
	case "ebook":
		return models.ReadingFormatEbook
	default:
		return ""
	}
}

func normalizeEditionCreateASIN(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func normalizeEditionCreateName(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func (h *Handler) createRegionalAudiobook(ctx context.Context, profile *database.ProfileWithTokens, item *models.AudiobookshelfBook, record sync.BookOutcomeRecord, request editionCreateRequest, client editionCreateHardcoverClient, outcome *editionCreateOutcome) (statepkg.Association, error) {
	asin, region, err := parseSubmittedAudibleIdentifier(request.AudibleIdentifier)
	if err != nil {
		return statepkg.Association{}, err
	}
	correction := ""
	var regionalBook *audnex.Book
	if request.AudibleIdentifier == "" {
		asin, _ = audnex.CanonicalASIN(item.Media.Metadata.ASIN)
		if asin == "" {
			return statepkg.Association{}, fmt.Errorf("%w: audiobook needs a valid source ASIN or corrected regional Audible identifier", errEditionCreateInvalidInput)
		}
		preferred := strings.TrimSpace(profile.SyncConfig.AudnexusRegion)
		if !audnexregion.IsRegion(strings.ToLower(preferred)) {
			preferred = "us"
		}
		discoverer := h.editionCreateAudnexDiscoverer()
		found, discoveredRegion, discoverErr := discoverer.DiscoverBookByASIN(ctx, asin, preferred)
		if discoverErr != nil {
			if errors.Is(discoverErr, audnex.ErrRateLimited) || errors.Is(discoverErr, audnex.ErrTransient) || errors.Is(discoverErr, context.DeadlineExceeded) {
				return statepkg.Association{}, fmt.Errorf("Audnex region discovery is temporarily unavailable: %w", discoverErr)
			}
			return statepkg.Association{}, fmt.Errorf("failed to discover the Audible region: %w", discoverErr)
		}
		foundASIN, valid := audnex.CanonicalASIN(foundASINValue(found))
		if !valid || foundASIN != asin || !audnexregion.IsRegion(discoveredRegion) {
			return statepkg.Association{}, errAudibleRegionUnknown
		}
		region = strings.ToLower(discoveredRegion)
		regionalBook = found
	} else {
		correction = request.AudibleIdentifier
		found, lookupErr := h.editionCreateAudnexDiscoverer().GetBookByASIN(ctx, asin, region)
		if ctx.Err() != nil {
			return statepkg.Association{}, fmt.Errorf("Audnex lookup for corrected Audible identifier was canceled: %w", ctx.Err())
		}
		if lookupErr == nil {
			foundASIN, valid := audnex.CanonicalASIN(foundASINValue(found))
			if valid && foundASIN == asin {
				regionalBook = found
			}
		}
	}

	bookID, err := strconv.Atoi(record.HardcoverBookID)
	if err != nil || bookID <= 0 {
		return statepkg.Association{}, errStaleEditionCreateRun
	}
	result, err := client.ImportRegionalAudiobook(ctx, hardcover.RegionalAudiobookInput{BookID: bookID, ASIN: asin, Region: region})
	if err != nil {
		if errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput) || errors.Is(err, hardcover.ErrRegionalAudiobookDryRun) {
			return statepkg.Association{}, fmt.Errorf("Hardcover regional audiobook import failed: %w", err)
		}
		return statepkg.Association{}, fmt.Errorf("Hardcover regional audiobook import failed: %w", markEditionCreateRemoteOutcomeAmbiguous(err))
	}
	if result == nil || result.BookID != bookID || result.EditionID <= 0 || result.ReadingFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) ||
		(result.Status != hardcover.RegionalAudiobookLoaded && result.Status != hardcover.RegionalAudiobookCreated) {
		return statepkg.Association{}, markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
	}
	regionalID := asin + ":" + region
	if result.RegionalExternalID != "" {
		if !strings.EqualFold(result.RegionalExternalID, regionalID) {
			return statepkg.Association{}, markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
		}
		regionalID = result.RegionalExternalID
	}
	preview := buildEditionSourceDraft(item, false).MetadataPreview
	if regionalBook != nil && strings.TrimSpace(regionalBook.ReleaseDate) != "" {
		if releaseDate, ok := normalizeDraftDate(regionalBook.ReleaseDate); ok {
			preview.ReleaseDate = releaseDate
		}
	}
	association := createEditionAssociation(item, record.HardcoverBookID, strconv.Itoa(result.EditionID), regionalID,
		correction, models.ReadingFormatAudiobook, "api_regional_"+string(result.Status))
	outcome.response = editionCreateResponse{
		ABSItemID: item.ID, ReadingFormat: models.ReadingFormatAudiobook, Status: string(result.Status),
		HardcoverBookID: strconv.Itoa(result.BookID), HardcoverEditionID: strconv.Itoa(result.EditionID), RegionalExternalID: regionalID,
		MetadataPreview: preview,
	}
	return association, nil
}

func foundASINValue(book *audnex.Book) string {
	if book == nil {
		return ""
	}
	return book.ASIN
}

func parseSubmittedAudibleIdentifier(raw string) (string, string, error) {
	if raw == "" {
		return "", "", nil
	}
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%w: audible_identifier must be an ASIN followed by a supported region (ASIN:region)", errEditionCreateInvalidInput)
	}
	asin, valid := audnex.CanonicalASIN(parts[0])
	region := strings.ToLower(strings.TrimSpace(parts[1]))
	if !valid || !audnexregion.IsRegion(region) {
		return "", "", fmt.Errorf("%w: audible_identifier must contain a valid ASIN and supported region", errEditionCreateInvalidInput)
	}
	return asin, region, nil
}

var errAudibleRegionUnknown = errors.New("Audnex did not confirm an Audible region; supply an explicit regional Audible identifier")
var errHardcoverEditionIdentityConflict = errors.New("Hardcover returned an edition that does not match the reviewed book and format")
var errEditionCreateRemoteOutcomeAmbiguous = errors.New("Hardcover may have processed the edition request but its result could not be verified")

func markEditionCreateRemoteOutcomeAmbiguous(err error) error {
	return fmt.Errorf("%w: %w", errEditionCreateRemoteOutcomeAmbiguous, err)
}

func (h *Handler) createEbook(ctx context.Context, item *models.AudiobookshelfBook, record sync.BookOutcomeRecord, request editionCreateRequest, client editionCreateHardcoverClient, outcome *editionCreateOutcome) (statepkg.Association, error) {
	if request.AudibleIdentifier != "" {
		return statepkg.Association{}, fmt.Errorf("%w: audible_identifier is only valid for an audiobook", errEditionCreateInvalidInput)
	}
	bookID, err := strconv.Atoi(record.HardcoverBookID)
	if err != nil || bookID <= 0 {
		return statepkg.Association{}, errStaleEditionCreateRun
	}
	queryCtx := models.WithReadingFormat(ctx, models.ReadingFormatEbook)
	book, err := client.GetBookByID(queryCtx, record.HardcoverBookID)
	if err != nil {
		return statepkg.Association{}, fmt.Errorf("failed to verify Hardcover book for ebook insertion: %w", err)
	}
	if book == nil || book.ID != record.HardcoverBookID {
		return statepkg.Association{}, errHardcoverEditionIdentityConflict
	}

	draft := buildEditionSourceDraft(item, false)
	if draft.EbookCandidate == nil {
		return statepkg.Association{}, fmt.Errorf("%w: Audiobookshelf item has no ebook edition candidate", errEditionCreateInvalidInput)
	}
	input := &edition.EditionInput{
		BookID: bookID, Title: draft.EbookCandidate.Title, Subtitle: draft.EbookCandidate.Subtitle,
		ASIN: strings.TrimSpace(item.Media.Metadata.ASIN), ReleaseDate: draft.EbookCandidate.ReleaseDate,
		EditionFormat: "Ebook", ReadingFormat: models.ReadingFormatEbook,
		LanguageID: 1, CountryID: 1,
	}
	if draft.EbookCandidate.ISBN10 != "" {
		input.ISBN10 = draft.EbookCandidate.ISBN10
	}
	if draft.EbookCandidate.ISBN13 != "" {
		input.ISBN13 = draft.EbookCandidate.ISBN13
	}
	if request.Title != nil {
		input.Title = strings.TrimSpace(*request.Title)
	}
	if request.Subtitle != nil {
		input.Subtitle = strings.TrimSpace(*request.Subtitle)
	}
	if request.ASIN != nil {
		input.ASIN = strings.TrimSpace(*request.ASIN)
	}
	if request.ISBN10 != nil || request.ISBN13 != nil {
		input.ISBN10, input.ISBN13, err = correctedEditionISBNs(request)
		if err != nil {
			return statepkg.Association{}, err
		}
	}
	if request.ReleaseDate != nil {
		input.ReleaseDate = strings.TrimSpace(*request.ReleaseDate)
	}
	if request.EditionFormat != nil {
		input.EditionFormat = strings.TrimSpace(*request.EditionFormat)
	}
	if input.EditionFormat == "" {
		input.EditionFormat = "Ebook"
	}
	if input.ASIN != "" {
		canonicalASIN, valid := audnex.CanonicalASIN(input.ASIN)
		if !valid {
			return statepkg.Association{}, fmt.Errorf("%w: ebook ASIN must contain ten letters or digits", errEditionCreateInvalidInput)
		}
		input.ASIN = canonicalASIN
	}
	if input.ASIN == "" && isbn.Normalize(input.ISBN10) == "" && isbn.Normalize(input.ISBN13) == "" {
		return statepkg.Association{}, fmt.Errorf("%w: an ASIN or ISBN is required", errEditionCreateInvalidInput)
	}
	if input.ISBN10 != "" {
		if _, ok := isbn.Parse(input.ISBN10); !ok {
			return statepkg.Association{}, fmt.Errorf("%w: ISBN-10 must contain ten digits or end in X", errEditionCreateInvalidInput)
		}
	}
	if input.ISBN13 != "" {
		if _, ok := isbn.Parse(input.ISBN13); !ok {
			return statepkg.Association{}, fmt.Errorf("%w: ISBN-13 must contain thirteen digits", errEditionCreateInvalidInput)
		}
	}
	input.AuthorIDs, err = ebookAuthorIDs(ctx, client, book, item.Media.Metadata.AuthorName)
	if err != nil {
		return statepkg.Association{}, err
	}
	if publisher := strings.TrimSpace(item.Media.Metadata.Publisher); publisher != "" {
		publishers, searchErr := client.SearchPublishers(ctx, publisher, 10)
		if searchErr != nil {
			return statepkg.Association{}, fmt.Errorf("failed to resolve optional ebook publisher: %w", searchErr)
		}
		for _, candidate := range publishers {
			if strings.EqualFold(strings.TrimSpace(candidate.Name), publisher) {
				if id, parseErr := strconv.Atoi(candidate.ID); parseErr == nil && id > 0 {
					input.PublisherID = id
					break
				}
			}
		}
	}
	if err := input.Validate(); err != nil {
		return statepkg.Association{}, fmt.Errorf("%w: %v", errEditionCreateInvalidInput, err)
	}
	result, err := client.CreateEbook(ctx, input)
	if err != nil {
		if errors.Is(err, edition.ErrCreateEditionPreMutation) {
			return statepkg.Association{}, fmt.Errorf("Hardcover ebook pre-insertion checks failed: %w", err)
		}
		return statepkg.Association{}, fmt.Errorf("Hardcover ebook insertion failed: %w", markEditionCreateRemoteOutcomeAmbiguous(err))
	}
	if result == nil || !result.Success || result.EditionID <= 0 {
		return statepkg.Association{}, markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
	}
	createdEdition, err := client.GetEditionUncached(ctx, strconv.Itoa(result.EditionID))
	if err != nil {
		return statepkg.Association{}, fmt.Errorf("failed to verify Hardcover ebook edition: %w", markEditionCreateRemoteOutcomeAmbiguous(err))
	}
	if createdEdition == nil {
		return statepkg.Association{}, markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
	}
	formatID, formatErr := strconv.Atoi(createdEdition.ReadingFormatID)
	if createdEdition.ID != strconv.Itoa(result.EditionID) || createdEdition.BookID != record.HardcoverBookID || formatErr != nil || formatID != models.ReadingFormatID(models.ReadingFormatEbook) {
		return statepkg.Association{}, markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
	}
	status := "created"
	if result.Existing {
		status = "existing"
	}
	association := createEditionAssociation(item, record.HardcoverBookID, strconv.Itoa(result.EditionID), "", "", models.ReadingFormatEbook, "api_ebook_"+status)
	outcome.response = editionCreateResponse{
		ABSItemID: item.ID, ReadingFormat: models.ReadingFormatEbook, Status: status,
		HardcoverBookID: record.HardcoverBookID, HardcoverEditionID: strconv.Itoa(result.EditionID),
	}
	return association, nil
}

func correctedEditionISBNs(request editionCreateRequest) (string, string, error) {
	if request.ISBN10 == nil && request.ISBN13 == nil {
		return "", "", nil
	}
	if request.ISBN10 != nil && request.ISBN13 != nil {
		isbn10, ok10 := isbn.Parse(*request.ISBN10)
		isbn13, ok13 := isbn.Parse(*request.ISBN13)
		if *request.ISBN10 != "" && (!ok10 || isbn10.Is13) || *request.ISBN13 != "" && (!ok13 || !isbn13.Is13) {
			return "", "", fmt.Errorf("%w: ISBN correction has an invalid ISBN shape", errEditionCreateInvalidInput)
		}
		if *request.ISBN10 != "" && *request.ISBN13 != "" && isbn10.ISBN13() != isbn13.ISBN13() {
			return "", "", fmt.Errorf("%w: ISBN-10 and ISBN-13 corrections identify different books", errEditionCreateInvalidInput)
		}
		return isbn.Normalize(*request.ISBN10), isbn.Normalize(*request.ISBN13), nil
	}
	if request.ISBN10 != nil {
		if strings.TrimSpace(*request.ISBN10) == "" {
			return "", "", nil
		}
		parsed, ok := isbn.Parse(*request.ISBN10)
		if !ok || parsed.Is13 {
			return "", "", fmt.Errorf("%w: ISBN-10 correction has an invalid shape", errEditionCreateInvalidInput)
		}
		return parsed.ISBN10(), parsed.ISBN13(), nil
	}
	if strings.TrimSpace(*request.ISBN13) == "" {
		return "", "", nil
	}
	parsed, ok := isbn.Parse(*request.ISBN13)
	if !ok || !parsed.Is13 {
		return "", "", fmt.Errorf("%w: ISBN-13 correction has an invalid shape", errEditionCreateInvalidInput)
	}
	return parsed.ISBN10(), parsed.ISBN13(), nil
}

func ebookAuthorIDs(ctx context.Context, client editionCreateHardcoverClient, book *models.HardcoverBook, sourceAuthor string) ([]int, error) {
	ids := make([]int, 0, len(book.Authors))
	seen := make(map[int]struct{})
	for _, author := range book.Authors {
		id, err := strconv.Atoi(author.ID)
		if err != nil || id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		return ids, nil
	}
	name := strings.TrimSpace(sourceAuthor)
	if name == "" {
		return nil, fmt.Errorf("%w: Audiobookshelf has no author and the reviewed Hardcover book has none", errEditionCreateInvalidInput)
	}
	authors, err := client.SearchAuthors(ctx, name, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve required ebook author: %w", err)
	}
	for _, author := range authors {
		if !strings.EqualFold(strings.TrimSpace(author.Name), name) {
			continue
		}
		id, parseErr := strconv.Atoi(author.ID)
		if parseErr != nil || id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no matching Hardcover author was found for %q", errEditionCreateInvalidInput, name)
	}
	return ids, nil
}

func createEditionAssociation(item *models.AudiobookshelfBook, bookID, editionID, regionalID, correction, format, provenance string) statepkg.Association {
	asin, isbn10, isbn13 := statepkg.SourceIdentifiers(item.Media.Metadata.ASIN, item.Media.Metadata.ISBN)
	return statepkg.Association{
		ABSItemID: item.ID, SourceASIN: asin, SourceISBN10: isbn10, SourceISBN13: isbn13,
		RegionalExternalID: regionalID, Correction: correction, HardcoverBookID: bookID,
		HardcoverEditionID: editionID, ReadingFormat: format, Provenance: provenance,
	}
}

func (h *Handler) editionCreateHardcoverClient(token string) editionCreateHardcoverClient {
	if h.editionCreateHardcoverFactory != nil {
		return h.editionCreateHardcoverFactory(token)
	}
	client := h.multiUserService.NewHardcoverClient(token)
	return editionCreateHardcoverAdapter{client: client, log: &h.log}
}

func (h *Handler) editionCreateABSClient(baseURL, token, networkTrust string) (editionCreateABSClient, error) {
	if h.editionCreateABSClientFactory != nil {
		return h.editionCreateABSClientFactory(baseURL, token, networkTrust)
	}
	return audiobookshelf.NewClientWithNetworkTrust(baseURL, token, networkTrust)
}

func (h *Handler) editionCreateAudnexDiscoverer() editionCreateAudnexDiscoverer {
	if h.editionCreateAudnexClientFactory != nil {
		return h.editionCreateAudnexClientFactory()
	}
	return audnex.NewClient(&h.log)
}

func (h *Handler) writeEditionCreateError(w http.ResponseWriter, profileID string, err error) {
	switch {
	case errors.Is(err, multiuser.ErrProfileNotFound):
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
	case errors.Is(err, multiuser.ErrSyncAlreadyActive), errors.Is(err, multiuser.ErrProfileDeleting), errors.Is(err, multiuser.ErrEditionAssociationAlreadyExists), errors.Is(err, errStaleEditionCreateRun), errors.Is(err, errEditionCreateSourceChanged):
		h.writeErrorResponse(w, http.StatusConflict, err.Error())
	case errors.Is(err, multiuser.ErrServiceShuttingDown):
		h.writeErrorResponse(w, http.StatusServiceUnavailable, "Edition creation service is shutting down; retry shortly")
	case errors.Is(err, multiuser.ErrProfileStateBusy):
		w.Header().Set("Retry-After", "1")
		h.writeErrorResponse(w, http.StatusTooManyRequests, "Profile sync state is busy; retry shortly")
	case errors.Is(err, multiuser.ErrEditionCreateDryRun):
		h.writeErrorResponse(w, http.StatusConflict, "Edition creation is disabled while this profile is in dry run")
	case errors.Is(err, edition.ErrCreateEditionPreMutation):
		if errors.Is(err, edition.ErrEditionBelongsToOtherBook) {
			h.writeErrorResponse(w, http.StatusConflict, err.Error())
			return
		}
		h.log.Error(fmt.Sprintf("Hardcover edition lookup failed before insertion for profile %s: %v", profileID, err))
		h.writeErrorResponse(w, http.StatusServiceUnavailable, "Hardcover could not check for an existing edition before insertion; retry the edition create")
	case errors.Is(err, multiuser.ErrEditionAssociationSaveAfterRemoteSuccess):
		h.log.Error(fmt.Sprintf("Hardcover returned a verified edition but association save failed for profile %s: %v", profileID, err))
		h.writeErrorResponse(w, http.StatusBadGateway, "Hardcover returned a verified edition, but the local match could not be saved. Verify the Hardcover result before retrying; retrying may create another edition.")
	case errors.Is(err, errEditionCreateRemoteOutcomeAmbiguous):
		message := "Hardcover may have processed the edition request, but its result could not be confirmed. Verify the Hardcover result before retrying; retrying may create another edition."
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout):
			h.writeErrorResponse(w, http.StatusServiceUnavailable, message)
		case errors.Is(err, errHardcoverEditionIdentityConflict), errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict):
			h.writeErrorResponse(w, http.StatusConflict, fmt.Sprintf("%s. Verify the Hardcover result before retrying; retrying may create another edition.", err.Error()))
		default:
			h.log.Error(fmt.Sprintf("Hardcover edition result could not be confirmed for profile %s: %v", profileID, err))
			h.writeErrorResponse(w, http.StatusBadGateway, message)
		}
	case errors.Is(err, errEditionCreateInvalidInput):
		h.writeErrorResponse(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, errAudibleRegionUnknown):
		h.writeErrorResponse(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput):
		h.writeErrorResponse(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict):
		h.writeErrorResponse(w, http.StatusConflict, err.Error())
	case errors.Is(err, audnex.ErrRateLimited), errors.Is(err, audnex.ErrTransient), errors.Is(err, context.DeadlineExceeded):
		h.writeErrorResponse(w, http.StatusServiceUnavailable, "Audiobookshelf, Audnex, or Hardcover is temporarily unavailable; retry the edition create")
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout):
		h.writeErrorResponse(w, http.StatusServiceUnavailable, "Hardcover regional import timed out; retry the edition create")
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportFailed):
		h.writeErrorResponse(w, http.StatusBadGateway, "Hardcover could not import the regional Audible identifier")
	case errors.Is(err, errHardcoverEditionIdentityConflict):
		h.writeErrorResponse(w, http.StatusConflict, err.Error())
	default:
		h.log.Error(fmt.Sprintf("Failed to create edition for profile %s: %v", profileID, err))
		h.writeErrorResponse(w, http.StatusBadGateway, "Failed to create and verify the Hardcover edition")
	}
}
