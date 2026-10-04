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
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

const (
	editionCreateRequestTimeout  = 65 * time.Second
	editionCreateMutationReserve = 35 * time.Second
	editionImportCheckTimeout    = 25 * time.Second

	editionCreateExistingBookConflictGuidance = "An existing Hardcover edition belongs to a different book. No edition was added."
	editionCreateAmbiguousIdentityGuidance    = "Hardcover may have processed the edition request, but its result could not be confirmed. The returned edition identity conflicted with the reviewed book. Verify the Hardcover result before retrying; retrying may create another edition."
	editionCreateLocalFailureGuidance         = "Local profile or state data could not be prepared for edition creation"
	editionCreateFailedGuidance               = "Failed to create and verify the Hardcover edition"
	editionAssociationSaveFailureGuidance     = "Hardcover returned a verified edition, but the local match could not be saved. Verify the Hardcover result before retrying; retrying may create another edition."
	editionImportFailureGuidance              = "Hardcover could not import the regional Audible identifier"
	editionImportUnconfirmedGuidance          = "Hardcover has not confirmed this regional import yet. Check its status before trying edition creation again."
	editionImportOutcomeAmbiguousGuidance     = "Hardcover may have processed the edition request, but its result could not be confirmed. Verify the Hardcover result before retrying; retrying may create another edition."
	editionImportIdentityUnconfirmedGuidance  = "Hardcover returned an edition that does not match the submitted book or audiobook format; the match was not saved"
	editionCreateRequestTooSlowGuidance       = "The request took too long to add this edition. Nothing was added to Hardcover. Please try again."
	editionCreateQuotaLowGuidance             = "Hardcover's daily API quota is running low. Nothing was added to Hardcover. Please wait until Hardcover's daily API quota resets, then try again."
	editionCreateQuotaExhaustedGuidance       = "Hardcover's daily API quota is exhausted. Nothing was added to Hardcover. Please wait until Hardcover's daily API quota resets, then try again."
	editionCreateDiscoveryBudgetGuidance      = "Audnexus region discovery could not finish before the Hardcover write deadline; no mutation was sent. Supply audible_identifier (ASIN:region) to skip discovery"
	editionCreateExistingEditionCheckGuidance = "Hardcover could not finish an edition preparation lookup or check for an existing edition; retry the edition create"
)

type editionCreateABSClient interface {
	GetLibraryItemByID(context.Context, string) (*models.AudiobookshelfBook, error)
}

type editionCreateRegionalAudiobookImporter interface {
	ImportRegionalAudiobook(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error)
}

type editionCreateRegionalAudiobookChecker interface {
	CheckRegionalAudiobookImport(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error)
}

type editionCreateHardcoverClient interface {
	editionCreateRegionalAudiobookImporter
	editionCreateRegionalAudiobookChecker
	GetBookByID(context.Context, string) (*models.HardcoverBook, error)
	GetEditionUncached(context.Context, string) (*models.Edition, error)
	SearchAuthors(context.Context, string, int) ([]models.Author, error)
	SearchNarrators(context.Context, string, int) ([]models.Author, error)
	SearchPublishers(context.Context, string, int) ([]models.Publisher, error)
	InsertEdition(context.Context, *edition.EditionInput) (*edition.EditionResult, error)
}

type editionCreateAudnexDiscoverer interface {
	GetBookByASIN(context.Context, string, string) (*audnex.Book, error)
	DiscoverBookByASIN(context.Context, string, string) (*audnex.Book, string, error)
}

type editionCreateRequest struct {
	RunID             string  `json:"run_id"`
	ABSItemID         string  `json:"abs_item_id"`
	AudibleIdentifier string  `json:"audible_identifier,omitempty"`
	AudnexusConfirmed bool    `json:"audnexus_confirmed,omitempty"`
	Title             *string `json:"title,omitempty"`
	Subtitle          *string `json:"subtitle,omitempty"`
	ASIN              *string `json:"asin,omitempty"`
	ISBN10            *string `json:"isbn_10,omitempty"`
	ISBN13            *string `json:"isbn_13,omitempty"`
	ReleaseDate       *string `json:"release_date,omitempty"`
	EditionFormat     *string `json:"edition_format,omitempty"`
	// Resync opts in to syncing this book's read status right after the
	// edition is created. It defaults to false.
	Resync bool `json:"resync,omitempty"`
}

type editionImportCheckRequest struct {
	RunID             string `json:"run_id"`
	ABSItemID         string `json:"abs_item_id"`
	AudibleIdentifier string `json:"audible_identifier"`
	RecoveryToken     string `json:"recovery_token"`
}

type editionCreateResponse struct {
	ABSItemID          string                    `json:"abs_item_id"`
	ReadingFormat      string                    `json:"reading_format"`
	Status             string                    `json:"status"`
	HardcoverBookID    string                    `json:"hardcover_book_id"`
	HardcoverEditionID string                    `json:"hardcover_edition_id"`
	HardcoverTitle     string                    `json:"hardcover_title,omitempty"`
	RegionalExternalID string                    `json:"regional_external_id,omitempty"`
	MetadataPreview    *audiobookMetadataPreview `json:"metadata_preview,omitempty"`
	action             *sync.EditionActionRecord `json:"-"`
	// Resync is present only when the request asked for one. A failed resync is
	// reported here rather than failing the request, because the edition exists.
	Resync *editionResyncResponse `json:"resync,omitempty"`
	// sourceEdition is the freshly verified edition snapshot reused by resync.
	sourceEdition *models.Edition

	// sourceItem is the verified Audiobookshelf item, kept for the optional
	// resync so it does not need a second lookup.
	sourceItem *models.AudiobookshelfBook
	// recovery preserves the signed attempted-import identity for error
	// responses only. It is never exposed on successful create responses.
	recovery *editionRecoveryData `json:"-"`
}

type editionResyncResponse struct {
	Attempted bool   `json:"attempted"`
	Outcome   string `json:"outcome,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
}

type editionCreateHardcoverAdapter struct {
	*hardcover.Client
	log *logger.Logger
}

func (c editionCreateHardcoverAdapter) InsertEdition(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
	creator := edition.NewCreator(c.Client, c.log, false, "")
	return creator.CreateEditionWithMutationReserve(ctx, input, editionCreateMutationReserve)
}

// CreateEditionFromDraft handles POST /api/profiles/{id}/edition-drafts/create.
// The run and item IDs identify the exact completed or canceled source snapshot.
// The book ID comes from that verified snapshot, never from the request.
func (h *Handler) CreateEditionFromDraft(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), editionCreateRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeEditionCreateStructuredError(w, http.StatusBadRequest, "Profile ID is required", "edition_create_invalid_request", editionOutcomeNotSubmitted, nil)
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
		h.writeEditionCreateStructuredError(w, http.StatusTooManyRequests, "Edition creation service is busy; retry shortly", "edition_create_busy", editionOutcomeNotSubmitted, nil)
		return
	}

	request, err := decodeEditionCreateRequest(w, r)
	if err != nil {
		h.writeEditionCreateStructuredError(w, http.StatusBadRequest, err.Error(), "edition_create_invalid_request", editionOutcomeNotSubmitted, nil)
		return
	}
	request.RunID = strings.TrimSpace(request.RunID)
	request.ABSItemID = strings.TrimSpace(request.ABSItemID)
	if request.RunID == "" || request.ABSItemID == "" {
		h.writeEditionCreateStructuredError(w, http.StatusBadRequest, "run_id and abs_item_id are required", "edition_create_invalid_request", editionOutcomeNotSubmitted, nil)
		return
	}

	var response editionCreateResponse
	var resync multiuser.EditionResyncOperation
	if request.Resync {
		resync = func(profile *database.ProfileWithTokens, syncState *statepkg.State, statePath string) {
			response.Resync = h.resyncCreatedEdition(ctx, profile, response.sourceItem, response.sourceEdition, syncState, statePath)
		}
	}
	err = h.multiUserService.CreateEditionWithAssociationAndResync(ctx, profileID, request.ABSItemID, func(profile *database.ProfileWithTokens) (statepkg.Association, error) {
		snapshot, record, snapshotErr := h.verifiedEditionCreateRecord(profileID, request.RunID, request.ABSItemID)
		if snapshotErr != nil {
			return statepkg.Association{}, snapshotErr
		}
		return h.createVerifiedEdition(ctx, profile, snapshot, record, request, &response)
	}, resync)
	if err != nil {
		h.log.Warn("Edition creation failed", map[string]interface{}{
			"profile_id":  profileID,
			"run_id":      request.RunID,
			"abs_item_id": request.ABSItemID,
			"error":       err.Error(),
		})
		h.writeEditionCreateErrorWithAction(w, profileID, err, response.recovery, response.action)
		return
	}
	h.writeSuccessResponse(w, response)
}

// CheckEditionImport handles POST /api/profiles/{id}/edition-drafts/check-import.
// It verifies an earlier signed audiobook import attempt and saves its local
// association. This endpoint performs only Hardcover reads.
func (h *Handler) CheckEditionImport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), editionImportCheckTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeEditionCreateStructuredError(w, http.StatusBadRequest, "Profile ID is required", "edition_create_invalid_request", editionOutcomeNotSubmitted, nil)
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
		h.writeEditionCreateStructuredError(w, http.StatusTooManyRequests, "Edition recovery service is busy; retry shortly", "edition_create_busy", editionOutcomeNotSubmitted, nil)
		return
	}
	request, err := decodeEditionImportCheckRequest(w, r)
	if err != nil {
		h.writeEditionCreateStructuredError(w, http.StatusBadRequest, err.Error(), "edition_create_invalid_request", editionOutcomeNotSubmitted, nil)
		return
	}
	request.RunID = strings.TrimSpace(request.RunID)
	request.ABSItemID = strings.TrimSpace(request.ABSItemID)
	request.AudibleIdentifier = strings.TrimSpace(request.AudibleIdentifier)
	request.RecoveryToken = strings.TrimSpace(request.RecoveryToken)
	if request.RunID == "" || request.ABSItemID == "" || request.AudibleIdentifier == "" || request.RecoveryToken == "" {
		h.writeEditionCreateStructuredError(w, http.StatusBadRequest, "run_id, abs_item_id, audible_identifier, and recovery_token are required", "edition_create_invalid_request", editionOutcomeNotSubmitted, nil)
		return
	}
	asin, region, err := parseSubmittedAudibleIdentifier(request.AudibleIdentifier)
	if err != nil {
		h.writeEditionCreateStructuredError(w, http.StatusUnprocessableEntity, err.Error(), "edition_create_invalid_input", editionOutcomeNotSubmitted, nil)
		return
	}
	externalID := asin + ":" + region
	var response editionCreateResponse
	var recovery *editionRecoveryData
	var action *sync.EditionActionRecord
	err = h.multiUserService.RecoverEditionAssociation(ctx, profileID, request.ABSItemID, func(profile *database.ProfileWithTokens) (statepkg.Association, error) {
		_, record, snapshotErr := h.verifiedEditionCreateRecord(profileID, request.RunID, request.ABSItemID)
		if snapshotErr != nil {
			return statepkg.Association{}, snapshotErr
		}
		unanchored := record.Reason == mismatch.ReasonAudibleImportAvailable
		bookID := 0
		if unanchored {
			if normalizedEditionCreateFormat(record.Format) != models.ReadingFormatAudiobook || strings.TrimSpace(record.HardcoverBookID) != "" {
				return statepkg.Association{}, errStaleEditionCreateRun
			}
		} else {
			var parseErr error
			bookID, parseErr = strconv.Atoi(record.HardcoverBookID)
			if parseErr != nil || bookID <= 0 || normalizedEditionCreateFormat(record.Format) != models.ReadingFormatAudiobook {
				return statepkg.Association{}, errStaleEditionCreateRun
			}
		}
		storedAction, foundAction, actionErr := h.multiUserService.GetEditionAction(profileID, request.RunID, request.ABSItemID)
		if actionErr != nil {
			return statepkg.Association{}, fmt.Errorf("failed to inspect pending edition request: %w: %w", multiuser.ErrEditionCreateLocalFailure, actionErr)
		}
		claims := editionRecoveryClaims{
			ProfileID: profileID, RunID: request.RunID, ABSItemID: request.ABSItemID,
			HardcoverBookID: record.HardcoverBookID, AudibleIdentifier: externalID,
		}
		verifiedClaims, validToken := verifyEditionRecoveryToken(profile.HardcoverToken, request.RecoveryToken, claims)
		if !validToken {
			if foundAction && storedAction != nil && (storedAction.Outcome == editionOutcomeUnconfirmed || storedAction.Outcome == editionOutcomeTransportUnknown || storedAction.Outcome == editionOutcomeCreated) {
				storedTokenValid := false
				if storedAction.Data != nil && storedAction.Data.RecoveryToken != "" {
					storedTokenClaims := editionRecoveryClaims{
						ProfileID: profileID, RunID: request.RunID, ABSItemID: request.ABSItemID,
						// The signed token binds the book ID recorded by this run. For
						// unanchored imports that value remains empty even after the
						// action journal stores the resolved Hardcover book ID.
						HardcoverBookID: record.HardcoverBookID, AudibleIdentifier: storedAction.Data.AudibleIdentifier,
					}
					_, storedTokenValid = verifyEditionRecoveryToken(profile.HardcoverToken, storedAction.Data.RecoveryToken, storedTokenClaims)
				}
				if storedTokenValid {
					// A malformed or mismatched request must not revoke the still-valid
					// recovery capability already stored for this exact item.
					return statepkg.Association{}, errEditionRecoveryInvalid
				}
				h.markEditionRecoveryUnavailable(profileID, storedAction)
				return statepkg.Association{}, errEditionRecoveryUnavailable
			}
			return statepkg.Association{}, errEditionRecoveryInvalid
		}
		if foundAction {
			if storedAction == nil || storedAction.SubmittedBody == nil || storedAction.Data == nil ||
				storedAction.SubmittedBody.RunID != request.RunID || storedAction.SubmittedBody.ABSItemID != request.ABSItemID ||
				(storedAction.Outcome != editionOutcomeUnconfirmed && storedAction.Outcome != editionOutcomeTransportUnknown && storedAction.Outcome != editionOutcomeCreated) ||
				storedAction.Data.RecoveryToken != request.RecoveryToken ||
				!strings.EqualFold(storedAction.Data.AudibleIdentifier, externalID) {
				return statepkg.Association{}, errEditionRecoveryInvalid
			}
			action = storedAction
			recovery = editionRecoveryFromAction(action.Data)
		} else {
			// Before durable edition journals existed, the browser held the signed
			// recovery capability. Keep that path usable after validating the full
			// profile/run/item/book/identifier binding. Do not create a journal
			// merely by checking a legacy token; a confirmed association will be
			// saved by RecoverEditionAssociation below.
			action = &sync.EditionActionRecord{
				Outcome:   editionOutcomeUnconfirmed,
				Error:     editionImportUnconfirmedGuidance,
				ErrorCode: "hardcover_import_unconfirmed",
				Data: &sync.EditionActionData{
					AudibleIdentifier: externalID, HardcoverBookID: record.HardcoverBookID,
					RecoveryToken: request.RecoveryToken, RecoveryExpiresAt: verifiedClaims.ExpiresAt,
				},
				SubmittedBody: &sync.EditionActionSubmittedBody{
					RunID: request.RunID, ABSItemID: request.ABSItemID, AudibleIdentifier: externalID,
				},
			}
			recovery = editionRecoveryFromAction(action.Data)
		}
		absClient, clientErr := h.editionCreateABSClient(profile.AudiobookshelfURL, profile.AudiobookshelfToken, h.multiUserService.AudiobookshelfNetworkTrust())
		if clientErr != nil {
			return statepkg.Association{}, fmt.Errorf("%w: invalid Audiobookshelf client configuration: %w", errEditionImportUnconfirmed, clientErr)
		}
		item, itemErr := absClient.GetLibraryItemByID(ctx, request.ABSItemID)
		if itemErr != nil {
			return statepkg.Association{}, fmt.Errorf("%w: failed to retrieve Audiobookshelf item: %w", errEditionImportUnconfirmed, itemErr)
		}
		if item == nil {
			return statepkg.Association{}, fmt.Errorf("%w: Audiobookshelf returned no item for edition recovery", errEditionImportUnconfirmed)
		}
		mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
		if (mediaType != "book" && mediaType != "ebook") || item.ReadingFormat() != models.ReadingFormatAudiobook {
			return statepkg.Association{}, fmt.Errorf("%w: %w", errEditionImportUnconfirmed, errEditionCreateSourceChanged)
		}
		if !editionCreateSourceMatches(record, item) {
			return statepkg.Association{}, fmt.Errorf("%w: %w", errEditionImportUnconfirmed, errEditionCreateSourceChanged)
		}
		client := h.editionCreateHardcoverClient(profile.Profile.ID, profile.HardcoverToken)
		result, confirmed, checkErr := client.CheckRegionalAudiobookImport(ctx, hardcover.RegionalAudiobookInput{
			BookID: bookID, ASIN: asin, Region: region, Unanchored: unanchored,
		})
		if checkErr != nil {
			if errors.Is(checkErr, hardcover.ErrRegionalAudiobookImportFailed) || errors.Is(checkErr, hardcover.ErrRegionalAudiobookWrongFormat) {
				response.action = action
				response.recovery = nil
				h.finalizeEditionCreateAction(profileID, &response, checkErr)
				return statepkg.Association{}, checkErr
			}
			if errors.Is(checkErr, hardcover.ErrRegionalAudiobookIdentityConflict) {
				return statepkg.Association{}, fmt.Errorf("%w: %w", errEditionRecoveryIdentityUnconfirmed, checkErr)
			}
			return statepkg.Association{}, fmt.Errorf("%w: %w", errEditionImportUnconfirmed, checkErr)
		}
		if !confirmed || result == nil {
			return statepkg.Association{}, errEditionImportUnconfirmed
		}
		if result.BookID <= 0 || (!unanchored && result.BookID != bookID) || result.EditionID <= 0 || result.ReadingFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) ||
			(result.Status != hardcover.RegionalAudiobookLoaded && result.Status != hardcover.RegionalAudiobookCreated) ||
			!strings.EqualFold(result.RegionalExternalID, externalID) {
			return statepkg.Association{}, fmt.Errorf("%w: %w", errEditionImportUnconfirmed, errHardcoverEditionIdentityConflict)
		}
		correction := verifiedClaims.Correction
		if unanchored {
			// Older signed tokens may contain the raw form submitted by the user.
			// Persist the canonical external ID while leaving signature verification
			// bound to the original claims.
			correction = externalID
		}
		var association statepkg.Association
		if unanchored {
			confirmedAt := time.Unix(verifiedClaims.IssuedAt, 0).UTC()
			association, clientErr = statepkg.NewAudibleImportAssociation(item, result, correction, confirmedAt)
			if clientErr != nil {
				return statepkg.Association{}, fmt.Errorf("%w: %v", errEditionImportUnconfirmed, clientErr)
			}
		} else {
			association = createEditionAssociation(item, record.HardcoverBookID, strconv.Itoa(result.EditionID), externalID,
				correction, models.ReadingFormatAudiobook, "api_regional_recovered")
		}
		hardcoverTitle := strings.TrimSpace(result.BookTitle)
		if unanchored && hardcoverTitle == "" {
			if resolvedBook, titleErr := client.GetBookByID(ctx, strconv.Itoa(result.BookID)); titleErr == nil && resolvedBook != nil && resolvedBook.ID == strconv.Itoa(result.BookID) {
				hardcoverTitle = strings.TrimSpace(resolvedBook.Title)
			}
		}
		response = editionCreateResponse{
			ABSItemID: item.ID, ReadingFormat: models.ReadingFormatAudiobook, Status: string(result.Status),
			HardcoverBookID: strconv.Itoa(result.BookID), HardcoverEditionID: strconv.Itoa(result.EditionID), HardcoverTitle: hardcoverTitle,
			RegionalExternalID: externalID, MetadataPreview: buildEditionSourceDraft(item, false).MetadataPreview,
			sourceItem: item,
		}
		return association, nil
	})
	if err != nil {
		if errors.Is(err, errEditionRecoveryUnavailable) {
			h.writeEditionCreateStructuredError(w, http.StatusConflict, editionRecoveryUnavailableGuidance, "edition_recovery_unavailable", editionOutcomeTransportUnknown, nil)
			return
		}
		if errors.Is(err, errEditionRecoveryInvalid) {
			h.writeEditionCreateStructuredError(w, http.StatusConflict, "Recovery token is invalid; refresh the edition draft and inspect Hardcover", "edition_recovery_invalid", editionOutcomeNotSubmitted, nil)
			return
		}
		h.writeEditionCreateErrorWithAction(w, profileID, err, recovery, nil)
		return
	}
	h.writeSuccessResponse(w, response)
}

func decodeEditionImportCheckRequest(w http.ResponseWriter, r *http.Request) (editionImportCheckRequest, error) {
	var request editionImportCheckRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return editionImportCheckRequest{}, fmt.Errorf("invalid edition import check request: %w", err)
	}
	var trailing interface{}
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return editionImportCheckRequest{}, errors.New("invalid edition import check request: trailing JSON value")
	}
	return request, nil
}

// resyncCreatedEdition runs the opt-in one-book resync after a successful
// create. Any failure is returned in the result instead of as an error: the
// edition already exists and its association is saved.
func (h *Handler) resyncCreatedEdition(ctx context.Context, profile *database.ProfileWithTokens, item *models.AudiobookshelfBook, verifiedEdition *models.Edition, syncState *statepkg.State, statePath string) *editionResyncResponse {
	if item == nil {
		return &editionResyncResponse{Attempted: false, Error: "resync could not start: the Audiobookshelf item was unavailable"}
	}
	if verifiedEdition == nil {
		return &editionResyncResponse{Attempted: false, Error: "resync could not start: the verified Hardcover edition was unavailable"}
	}
	var result sync.BookResyncResult
	var err error
	if h.editionResyncRunner == nil {
		result, err = h.multiUserService.ResyncBookWithEdition(ctx, profile, *item, verifiedEdition, syncState, statePath)
	} else {
		result, err = h.editionResyncRunner(ctx, profile, *item, syncState, statePath)
	}
	if err != nil {
		h.log.Warn(fmt.Sprintf("Resync after edition creation failed for profile %s: %v", profile.Profile.ID, err))
		return &editionResyncResponse{Attempted: true, Error: err.Error()}
	}
	return &editionResyncResponse{Attempted: true, Outcome: string(result.Outcome), Reason: result.Reason, Error: result.Error}
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
		return nil, sync.BookOutcomeRecord{}, fmt.Errorf("failed to retrieve requested sync run: %w: %w", multiuser.ErrEditionCreateLocalFailure, err)
	}
	if snapshot == nil || snapshot.RunID != runID ||
		(snapshot.ProfileID != "" && snapshot.ProfileID != profileID) ||
		(snapshot.State != string(sync.RunPhaseCompleted) && snapshot.State != string(sync.RunPhaseCanceled)) ||
		snapshot.DryRun {
		return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
	}
	for _, record := range snapshot.BookOutcomes {
		if record.BookID != itemID {
			continue
		}
		if record.Outcome != sync.OutcomeNeedsReview || strings.TrimSpace(record.Format) == "" {
			return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
		}
		if record.Reason == mismatch.ReasonAudibleImportAvailable {
			if normalizedEditionCreateFormat(record.Format) != models.ReadingFormatAudiobook || strings.TrimSpace(record.HardcoverBookID) != "" || strings.TrimSpace(record.EditionID) != "" {
				return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
			}
			sourceASIN := record.SourceASIN
			if sourceASIN == "" {
				sourceASIN = record.ASIN
			}
			if _, valid := audnex.CanonicalASIN(sourceASIN); !valid {
				return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
			}
		} else {
			if strings.TrimSpace(record.HardcoverBookID) == "" {
				return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
			}
			if _, err := strconv.Atoi(record.HardcoverBookID); err != nil {
				return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
			}
		}
		laterRecord, found, laterErr := h.multiUserService.GetLaterUsableSyncRunOutcome(profileID, runID, itemID)
		if laterErr != nil {
			return nil, sync.BookOutcomeRecord{}, fmt.Errorf("failed to inspect newer sync outcomes: %w: %w", multiuser.ErrEditionCreateLocalFailure, laterErr)
		}
		if found && !sameEditionCreateCandidate(record, laterRecord) {
			return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
		}
		return snapshot, record, nil
	}
	return nil, sync.BookOutcomeRecord{}, errStaleEditionCreateRun
}

func sameEditionCreateCandidate(requested, latest sync.BookOutcomeRecord) bool {
	if latest.BookID != requested.BookID || latest.Outcome != sync.OutcomeNeedsReview ||
		strings.TrimSpace(latest.HardcoverBookID) != strings.TrimSpace(requested.HardcoverBookID) ||
		normalizedEditionCreateFormat(latest.Format) != normalizedEditionCreateFormat(requested.Format) ||
		edition.NormalizeNameOrTitle(latest.Title) != edition.NormalizeNameOrTitle(requested.Title) ||
		edition.NormalizeNameOrTitle(latest.Author) != edition.NormalizeNameOrTitle(requested.Author) {
		return false
	}
	if requested.Reason != mismatch.ReasonAudibleImportAvailable && latest.Reason != mismatch.ReasonAudibleImportAvailable {
		return normalizeEditionCreateASIN(latest.ASIN) == normalizeEditionCreateASIN(requested.ASIN) &&
			isbn.Normalize(latest.ISBN) == isbn.Normalize(requested.ISBN)
	}
	return latest.Reason == requested.Reason &&
		normalizeEditionCreateASIN(firstNonEmptyString(latest.SourceASIN, latest.ASIN)) == normalizeEditionCreateASIN(firstNonEmptyString(requested.SourceASIN, requested.ASIN)) &&
		isbn.Normalize(latest.ISBN) == isbn.Normalize(requested.ISBN) &&
		isbn.Normalize(latest.SourceISBN10) == isbn.Normalize(requested.SourceISBN10) &&
		isbn.Normalize(latest.SourceISBN13) == isbn.Normalize(requested.SourceISBN13)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var errStaleEditionCreateRun = errors.New("sync run no longer contains a usable needs-review source record")
var errEditionCreateSourceChanged = errors.New("Audiobookshelf source data or reading format changed; run a new sync before adding an edition")
var errEditionCreateInvalidInput = errors.New("invalid edition create input")
var errEditionAudnexusConfirmationRequired = errors.New("Audnexus confirmation and a regional Audible identifier are required")
var errEditionRecoveryInvalid = errors.New("edition import recovery token is invalid")
var errEditionRecoveryUnavailable = errors.New("edition import recovery is unavailable; inspect Hardcover manually before retrying")
var errEditionImportUnconfirmed = errors.New("Hardcover has not confirmed the regional audiobook import")
var errEditionRecoveryIdentityUnconfirmed = errors.New("Hardcover returned an unverified regional audiobook identity")
var errEditionAudnexusConfirmationChanged = errors.New("Audnexus no longer confirms the reviewed regional Audible identifier")
var errEditionCreateInsufficientBudget = errors.New("edition create has too little time remaining for a Hardcover write")
var errEditionCreateDiscoveryBudget = errors.New("Audnexus region discovery could not finish before the Hardcover write deadline")

func requireEditionCreateMutationBudget(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errEditionCreateInsufficientBudget
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < editionCreateMutationReserve {
		return errEditionCreateInsufficientBudget
	}
	return nil
}

func (h *Handler) createVerifiedEdition(ctx context.Context, profile *database.ProfileWithTokens, snapshot *sync.SyncSnapshot, record sync.BookOutcomeRecord, request editionCreateRequest, response *editionCreateResponse) (statepkg.Association, error) {
	if profile.SyncConfig.DryRun {
		return statepkg.Association{}, multiuser.ErrEditionCreateDryRun
	}
	previousAction, found, err := h.multiUserService.GetEditionAction(profile.Profile.ID, request.RunID, request.ABSItemID)
	if err != nil {
		return statepkg.Association{}, fmt.Errorf("failed to inspect prior edition request: %w: %w", multiuser.ErrEditionCreateLocalFailure, err)
	}
	if found && previousAction != nil && previousAction.Outcome != editionOutcomeNotSubmitted {
		response.action = previousAction
		response.recovery = editionRecoveryFromAction(previousAction.Data)
		return statepkg.Association{}, fmt.Errorf("%w: prior outcome is %s", multiuser.ErrEditionActionRequiresResolution, previousAction.Outcome)
	}
	unanchoredAudibleImport := record.Reason == mismatch.ReasonAudibleImportAvailable
	if unanchoredAudibleImport {
		if !request.AudnexusConfirmed || strings.TrimSpace(request.AudibleIdentifier) == "" {
			return statepkg.Association{}, errEditionAudnexusConfirmationRequired
		}
	} else if request.AudnexusConfirmed {
		return statepkg.Association{}, errEditionAudnexusConfirmationRequired
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
	if item == nil {
		return statepkg.Association{}, errors.New("Audiobookshelf returned no item for edition creation")
	}
	mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
	if mediaType != "book" && mediaType != "ebook" {
		return statepkg.Association{}, fmt.Errorf("%w: Audiobookshelf item must be a book", errEditionCreateInvalidInput)
	}
	if !editionCreateSourceMatches(record, item) {
		return statepkg.Association{}, errEditionCreateSourceChanged
	}
	action := &sync.EditionActionRecord{
		Outcome:       editionOutcomeNotSubmitted,
		SubmittedBody: submittedEditionActionBody(request),
	}
	if err := h.persistEditionAction(profile.Profile.ID, action, response); err != nil {
		return statepkg.Association{}, err
	}
	if request.hasAudiobookMetadataCorrection() && item.ReadingFormat() == models.ReadingFormatAudiobook {
		invalidErr := fmt.Errorf("%w: audiobook metadata is preview-only; only audible_identifier may be corrected", errEditionCreateInvalidInput)
		h.finalizeEditionCreateAction(profile.Profile.ID, response, invalidErr)
		return statepkg.Association{}, invalidErr
	}
	if request.AudibleIdentifier != "" && item.ReadingFormat() != models.ReadingFormatAudiobook {
		invalidErr := fmt.Errorf("%w: audible_identifier is only valid for an audiobook", errEditionCreateInvalidInput)
		h.finalizeEditionCreateAction(profile.Profile.ID, response, invalidErr)
		return statepkg.Association{}, invalidErr
	}

	client := h.editionCreateHardcoverClient(profile.Profile.ID, profile.HardcoverToken)
	var association statepkg.Association
	_, validSourceASIN := audnex.CanonicalASIN(item.Media.Metadata.ASIN)
	if item.ReadingFormat() == models.ReadingFormatAudiobook && (validSourceASIN || request.AudibleIdentifier != "") {
		association, err = h.createRegionalAudiobook(ctx, profile, item, record, request, client, response)
	} else {
		association, err = h.createInsertedEdition(ctx, profile, item, record, request, client, response)
	}
	if err != nil {
		if response.action != nil && response.action.Outcome == editionOutcomeNotSubmitted {
			h.finalizeEditionCreateAction(profile.Profile.ID, response, err)
		}
		return statepkg.Association{}, err
	}
	response.sourceItem = item
	return association, nil
}

func (r editionCreateRequest) hasAudiobookMetadataCorrection() bool {
	return r.Title != nil || r.Subtitle != nil || r.ASIN != nil || r.ISBN10 != nil || r.ISBN13 != nil || r.ReleaseDate != nil || r.EditionFormat != nil
}

func editionCreateSourceMatches(record sync.BookOutcomeRecord, item *models.AudiobookshelfBook) bool {
	if item == nil || item.ID != record.BookID || normalizedEditionCreateFormat(record.Format) != item.ReadingFormat() {
		return false
	}
	recordASIN := firstNonEmptyString(record.SourceASIN, record.ASIN)
	if record.Reason == mismatch.ReasonAudibleImportAvailable {
		currentASIN, valid := audnex.CanonicalASIN(item.Media.Metadata.ASIN)
		sourceASIN, sourceValid := audnex.CanonicalASIN(recordASIN)
		if !valid || !sourceValid || currentASIN != sourceASIN {
			return false
		}
		_, sourceISBN10, sourceISBN13 := statepkg.SourceIdentifiers("", record.ISBN)
		if record.SourceISBN10 != "" || record.SourceISBN13 != "" {
			sourceISBN10, sourceISBN13 = record.SourceISBN10, record.SourceISBN13
		}
		_, currentISBN10, currentISBN13 := statepkg.SourceIdentifiers("", item.Media.Metadata.ISBN)
		if isbn.Normalize(record.ISBN) != isbn.Normalize(item.Media.Metadata.ISBN) {
			return false
		}
		if (sourceISBN10 != "" && isbn.Normalize(sourceISBN10) != isbn.Normalize(currentISBN10)) ||
			(sourceISBN13 != "" && isbn.Normalize(sourceISBN13) != isbn.Normalize(currentISBN13)) {
			return false
		}
		return edition.NormalizeNameOrTitle(record.Title) == edition.NormalizeNameOrTitle(item.Media.Metadata.Title) &&
			edition.NormalizeNameOrTitle(record.Author) == edition.NormalizeNameOrTitle(item.Media.Metadata.AuthorName)
	}
	return edition.NormalizeNameOrTitle(record.Title) == edition.NormalizeNameOrTitle(item.Media.Metadata.Title) &&
		edition.NormalizeNameOrTitle(record.Author) == edition.NormalizeNameOrTitle(item.Media.Metadata.AuthorName) &&
		normalizeEditionCreateASIN(recordASIN) == normalizeEditionCreateASIN(item.Media.Metadata.ASIN) &&
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

func (h *Handler) createRegionalAudiobook(ctx context.Context, profile *database.ProfileWithTokens, item *models.AudiobookshelfBook, record sync.BookOutcomeRecord, request editionCreateRequest, client editionCreateHardcoverClient, response *editionCreateResponse) (statepkg.Association, error) {
	unanchored := record.Reason == mismatch.ReasonAudibleImportAvailable
	asin, region, err := parseSubmittedAudibleIdentifier(request.AudibleIdentifier)
	if err != nil {
		return statepkg.Association{}, err
	}
	correction := ""
	var regionalBook *audnex.Book
	if unanchored {
		// The reviewed region is a confirmation boundary. Re-read exactly the
		// ASIN and region the user saw; never rediscover or silently switch regions.
		correction = asin + ":" + region
		lookupCtx := ctx
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			lookupCtx, cancel = context.WithDeadline(ctx, deadline.Add(-editionCreateMutationReserve-time.Second))
			defer cancel()
		}
		found, lookupErr := h.editionCreateAudnexDiscoverer().GetBookByASIN(lookupCtx, asin, region)
		if ctx.Err() != nil {
			return statepkg.Association{}, fmt.Errorf("Audnexus lookup for confirmed Audible identifier was canceled: %w", ctx.Err())
		}
		if errors.Is(lookupErr, audnex.ErrNotFound) || lookupErr == nil && found == nil {
			return statepkg.Association{}, errEditionAudnexusConfirmationChanged
		}
		if lookupErr != nil {
			return statepkg.Association{}, fmt.Errorf("Audnexus lookup for confirmed Audible identifier failed: %w", lookupErr)
		}
		foundASIN, valid := audnex.CanonicalASIN(foundASINValue(found))
		if !valid || foundASIN != asin {
			return statepkg.Association{}, errEditionAudnexusConfirmationChanged
		}
		regionalBook = found
	} else if request.AudibleIdentifier == "" {
		asin, _ = audnex.CanonicalASIN(item.Media.Metadata.ASIN)
		if asin == "" {
			return statepkg.Association{}, fmt.Errorf("%w: audiobook needs a valid source ASIN or corrected regional Audible identifier", errEditionCreateInvalidInput)
		}
		preferred := strings.TrimSpace(h.multiUserService.ProfileAudnexusRegion(profile.SyncConfig))
		if !audnexregion.IsRegion(strings.ToLower(preferred)) {
			preferred = "us"
		}
		discoverer := h.editionCreateAudnexDiscoverer()
		if err := requireEditionCreateMutationBudget(ctx); err != nil {
			return statepkg.Association{}, err
		}
		discoveryCtx := ctx
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			discoveryCtx, cancel = context.WithDeadline(ctx, deadline.Add(-editionCreateMutationReserve))
			defer cancel()
		}
		found, discoveredRegion, discoverErr := discoverer.DiscoverBookByASIN(discoveryCtx, asin, preferred)
		if errors.Is(discoveryCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return statepkg.Association{}, errEditionCreateDiscoveryBudget
		}
		if discoverErr != nil {
			if errors.Is(discoverErr, audnex.ErrRateLimited) || errors.Is(discoverErr, audnex.ErrTransient) || errors.Is(discoverErr, context.DeadlineExceeded) {
				return statepkg.Association{}, fmt.Errorf("Audnexus region discovery is temporarily unavailable: %w", discoverErr)
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
		lookupCtx := ctx
		if deadline, ok := ctx.Deadline(); ok {
			// Leave a small margin to check the lookup result before admitting the write.
			var cancel context.CancelFunc
			lookupCtx, cancel = context.WithDeadline(ctx, deadline.Add(-editionCreateMutationReserve-time.Second))
			defer cancel()
		}
		found, lookupErr := h.editionCreateAudnexDiscoverer().GetBookByASIN(lookupCtx, asin, region)
		if ctx.Err() != nil {
			return statepkg.Association{}, fmt.Errorf("Audnexus lookup for corrected Audible identifier was canceled: %w", ctx.Err())
		}
		if lookupErr == nil {
			foundASIN, valid := audnex.CanonicalASIN(foundASINValue(found))
			if valid && foundASIN == asin {
				regionalBook = found
			}
		}
	}

	bookID := 0
	if !unanchored {
		bookID, err = strconv.Atoi(record.HardcoverBookID)
		if err != nil || bookID <= 0 {
			return statepkg.Association{}, errStaleEditionCreateRun
		}
	}
	if err := requireEditionCreateMutationBudget(ctx); err != nil {
		if request.AudibleIdentifier == "" && ctx.Err() == nil {
			return statepkg.Association{}, errEditionCreateDiscoveryBudget
		}
		return statepkg.Association{}, err
	}
	externalID := asin + ":" + region
	claims := editionRecoveryClaims{
		ProfileID: profile.Profile.ID, RunID: request.RunID, ABSItemID: item.ID,
		AudibleIdentifier: externalID,
		Correction:        correction,
	}
	if !unanchored {
		claims.HardcoverBookID = record.HardcoverBookID
	}
	recoveryIssuedAt := time.Now().UTC()
	response.recovery = &editionRecoveryData{
		AudibleIdentifier: externalID,
		RecoveryToken:     signEditionRecoveryTokenAt(profile.HardcoverToken, claims, recoveryIssuedAt),
		RecoveryExpiresAt: recoveryIssuedAt.Add(editionRecoveryLifetime).Unix(),
	}
	if !unanchored {
		response.recovery.HardcoverBookID = record.HardcoverBookID
	}
	response.action.Outcome = editionOutcomeUnconfirmed
	response.action.HTTPStatus = 0
	response.action.Error = ""
	response.action.ErrorCode = ""
	response.action.Data = editionActionDataFromRecovery(response.recovery)
	if err := h.persistEditionAction(profile.Profile.ID, response.action, response); err != nil {
		// The mutation has not been sent. Restore a definite safe-to-retry state
		// in memory; the previously saved not_submitted record remains durable.
		response.action.Outcome = editionOutcomeNotSubmitted
		response.action.Data = nil
		return statepkg.Association{}, err
	}
	mutationCtx := hardcover.WithMinimumMutationBudget(ctx, editionCreateMutationReserve)
	result, err := client.ImportRegionalAudiobook(mutationCtx, hardcover.RegionalAudiobookInput{BookID: bookID, ASIN: asin, Region: region, Unanchored: unanchored})
	if err != nil {
		if errors.Is(err, hardcover.ErrMutationInsufficientBudget) {
			writeErr := errors.Join(errEditionCreateInsufficientBudget, err)
			response.recovery = nil
			h.finalizeEditionCreateAction(profile.Profile.ID, response, writeErr)
			return statepkg.Association{}, writeErr
		}
		if errors.Is(err, hardcover.ErrMutationScopeDenied) {
			writeErr := fmt.Errorf("Hardcover catalogue write permission is required: %w", err)
			h.finalizeEditionCreateAction(profile.Profile.ID, response, writeErr)
			return statepkg.Association{}, writeErr
		}
		if errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput) || errors.Is(err, hardcover.ErrRegionalAudiobookDryRun) || errors.Is(err, hardcover.ErrRegionalAudiobookImportFailed) ||
			errors.Is(err, hardcover.ErrRegionalAudiobookWrongFormat) {
			writeErr := fmt.Errorf("Hardcover regional audiobook import failed: %w", err)
			h.finalizeEditionCreateAction(profile.Profile.ID, response, writeErr)
			return statepkg.Association{}, writeErr
		}
		writeErr := fmt.Errorf("Hardcover regional audiobook import failed: %w", markEditionCreateRemoteOutcomeAmbiguous(err))
		h.finalizeEditionCreateAction(profile.Profile.ID, response, writeErr)
		return statepkg.Association{}, writeErr
	}
	if result == nil || result.BookID <= 0 || (!unanchored && result.BookID != bookID) || result.EditionID <= 0 || result.ReadingFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) ||
		(result.Status != hardcover.RegionalAudiobookLoaded && result.Status != hardcover.RegionalAudiobookCreated) {
		writeErr := markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
		h.finalizeEditionCreateAction(profile.Profile.ID, response, writeErr)
		return statepkg.Association{}, writeErr
	}
	regionalID := externalID
	if result.RegionalExternalID != "" {
		if !strings.EqualFold(result.RegionalExternalID, regionalID) {
			writeErr := markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
			h.finalizeEditionCreateAction(profile.Profile.ID, response, writeErr)
			return statepkg.Association{}, writeErr
		}
		regionalID = result.RegionalExternalID
	}
	preview := buildEditionSourceDraft(item, false).MetadataPreview
	if regionalBook != nil && strings.TrimSpace(regionalBook.ReleaseDate) != "" {
		if releaseDate, ok := edition.NormalizeAudnexusDate(regionalBook.ReleaseDate); ok {
			preview.ReleaseDate = releaseDate
		}
	}
	var association statepkg.Association
	if unanchored {
		confirmedAt := time.Unix(recoveryIssuedAt.Unix(), 0).UTC()
		association, err = statepkg.NewAudibleImportAssociation(item, result, correction, confirmedAt)
		if err != nil {
			return statepkg.Association{}, markEditionCreateRemoteOutcomeAmbiguous(err)
		}
	} else {
		association = createEditionAssociation(item, record.HardcoverBookID, strconv.Itoa(result.EditionID), regionalID,
			correction, models.ReadingFormatAudiobook, "api_regional_"+string(result.Status))
	}
	hardcoverTitle := strings.TrimSpace(result.BookTitle)
	if unanchored && hardcoverTitle == "" {
		// The verified import identity is sufficient to save the association;
		// this lookup only fills its display title and may fail after the write.
		bookID := strconv.Itoa(result.BookID)
		if resolvedBook, titleErr := client.GetBookByID(ctx, bookID); titleErr == nil && resolvedBook != nil && resolvedBook.ID == bookID {
			hardcoverTitle = strings.TrimSpace(resolvedBook.Title)
		}
	}
	recovery := response.recovery
	*response = editionCreateResponse{
		ABSItemID: item.ID, ReadingFormat: models.ReadingFormatAudiobook, Status: string(result.Status),
		HardcoverBookID: strconv.Itoa(result.BookID), HardcoverEditionID: strconv.Itoa(result.EditionID), HardcoverTitle: hardcoverTitle, RegionalExternalID: regionalID,
		MetadataPreview: preview, recovery: recovery, sourceEdition: result.Edition, action: response.action,
	}
	response.action.Outcome = editionOutcomeCreated
	response.action.HTTPStatus = http.StatusBadGateway
	response.action.Error = editionAssociationSaveFailureGuidance
	response.action.ErrorCode = "edition_association_save_failed"
	response.action.Data = editionActionDataFromRecovery(recovery)
	response.action.Data.HardcoverBookID = strconv.Itoa(result.BookID)
	response.action.Data.HardcoverEditionID = strconv.Itoa(result.EditionID)
	h.persistEditionActionBestEffort(profile.Profile.ID, response.action)
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

var errAudibleRegionUnknown = errors.New("Audnexus did not confirm an Audible region; supply an explicit regional Audible identifier")
var errHardcoverEditionIdentityConflict = errors.New("Hardcover returned an edition that does not match the reviewed book and format")
var errEditionCreateRemoteOutcomeAmbiguous = errors.New("Hardcover may have processed the edition request but its result could not be verified")

func markEditionCreateRemoteOutcomeAmbiguous(err error) error {
	return fmt.Errorf("%w: %w", errEditionCreateRemoteOutcomeAmbiguous, err)
}

func (h *Handler) createInsertedEdition(ctx context.Context, profile *database.ProfileWithTokens, item *models.AudiobookshelfBook, record sync.BookOutcomeRecord, request editionCreateRequest, client editionCreateHardcoverClient, response *editionCreateResponse) (statepkg.Association, error) {
	bookID, err := strconv.Atoi(record.HardcoverBookID)
	if err != nil || bookID <= 0 {
		return statepkg.Association{}, errStaleEditionCreateRun
	}
	format := item.ReadingFormat()
	queryCtx := models.WithReadingFormat(ctx, format)
	book, err := client.GetBookByID(queryCtx, record.HardcoverBookID)
	if err != nil {
		return statepkg.Association{}, fmt.Errorf("failed to verify Hardcover book for edition insertion: %w", err)
	}
	if book == nil || book.ID != record.HardcoverBookID {
		return statepkg.Association{}, errHardcoverEditionIdentityConflict
	}

	draft := buildEditionSourceDraft(item, false)
	input := &edition.EditionInput{BookID: bookID, ReadingFormat: format, LanguageID: 1, CountryID: 1}
	if item.IsEbook() {
		candidate := draft.EbookCandidate
		input.Title, input.Subtitle, input.ReleaseDate = candidate.Title, candidate.Subtitle, candidate.ReleaseDate
		input.EditionFormat = "Ebook"
		input.ISBN10, input.ISBN13 = candidate.ISBN10, candidate.ISBN13
		if sourceASIN, valid := audnex.CanonicalASIN(item.Media.Metadata.ASIN); valid {
			input.ASIN = sourceASIN
		}
	} else {
		candidate := draft.MetadataPreview
		input.Title, input.Subtitle, input.ReleaseDate = candidate.Title, candidate.Subtitle, candidate.ReleaseDate
		input.EditionFormat = candidate.EditionFormat
		if input.EditionFormat == "" {
			input.EditionFormat = "Audiobook"
		}
		input.ISBN10, input.ISBN13 = candidate.ISBN10, candidate.ISBN13
		input.AudioLength, input.EditionInfo = candidate.AudioSeconds, candidate.EditionInformation
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
	input.AuthorIDs, err = editionAuthorIDs(ctx, client, book, item.Media.Metadata.AuthorName)
	if err != nil {
		return statepkg.Association{}, err
	}
	if !item.IsEbook() {
		for _, rawName := range strings.Split(item.Media.Metadata.NarratorName, ",") {
			name := strings.TrimSpace(rawName)
			if name == "" {
				continue
			}
			narrators, lookupErr := client.SearchNarrators(ctx, name, 10)
			if lookupErr != nil {
				if ctx.Err() != nil {
					return statepkg.Association{}, fmt.Errorf("narrator lookup canceled: %w", ctx.Err())
				}
				return statepkg.Association{}, fmt.Errorf("narrator lookup failed before insertion: %w: %w", edition.ErrCreateEditionPreMutation, lookupErr)
			}
			var ids []int
			for _, narrator := range narrators {
				if strings.EqualFold(strings.TrimSpace(narrator.Name), name) {
					if id, parseErr := strconv.Atoi(narrator.ID); parseErr == nil && id > 0 {
						ids = append(ids, id)
					}
				}
			}
			if len(ids) == 1 {
				input.NarratorIDs = append(input.NarratorIDs, ids[0])
			}
		}
	}
	if publisher := strings.TrimSpace(item.Media.Metadata.Publisher); publisher != "" {
		publishers, searchErr := client.SearchPublishers(ctx, publisher, 10)
		if searchErr != nil {
			// Publisher is optional metadata. Ignore lookup failures while the
			// request remains active; the mutation budget check below prevents a
			// canceled or expired request from proceeding to Hardcover.
			if ctx.Err() != nil {
				return statepkg.Association{}, fmt.Errorf("edition publisher lookup canceled: %w", ctx.Err())
			}
			publishers = nil
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
	if err := requireEditionCreateMutationBudget(ctx); err != nil {
		return statepkg.Association{}, err
	}
	response.action.Outcome = editionOutcomeUnconfirmed
	response.action.HTTPStatus = 0
	response.action.Error = ""
	response.action.ErrorCode = ""
	response.action.Data = &sync.EditionActionData{HardcoverBookID: record.HardcoverBookID}
	if err := h.persistEditionAction(profile.Profile.ID, response.action, response); err != nil {
		response.action.Outcome = editionOutcomeNotSubmitted
		response.action.Data = nil
		return statepkg.Association{}, err
	}
	mutationCtx := hardcover.WithMinimumMutationBudget(ctx, editionCreateMutationReserve)
	result, err := client.InsertEdition(mutationCtx, input)
	if err != nil {
		if errors.Is(err, hardcover.ErrMutationInsufficientBudget) {
			createErr := errors.Join(edition.ErrCreateEditionPreMutation, edition.ErrCreateEditionInsufficientMutationBudget, err)
			h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
			return statepkg.Association{}, createErr
		}
		if errors.Is(err, hardcover.ErrMutationScopeDenied) {
			createErr := fmt.Errorf("Hardcover catalogue write permission is required: %w", err)
			h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
			return statepkg.Association{}, createErr
		}
		if errors.Is(err, edition.ErrCreateEditionPreMutation) {
			createErr := fmt.Errorf("Hardcover edition pre-insertion checks failed: %w", err)
			h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
			return statepkg.Association{}, createErr
		}
		createErr := fmt.Errorf("Hardcover edition insertion failed: %w", markEditionCreateRemoteOutcomeAmbiguous(err))
		h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
		return statepkg.Association{}, createErr
	}
	if result == nil || !result.Success || result.EditionID <= 0 {
		createErr := markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
		h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
		return statepkg.Association{}, createErr
	}
	createdEdition, err := client.GetEditionUncached(ctx, strconv.Itoa(result.EditionID))
	if err != nil {
		createErr := fmt.Errorf("failed to verify Hardcover edition: %w", markEditionCreateRemoteOutcomeAmbiguous(err))
		h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
		return statepkg.Association{}, createErr
	}
	if createdEdition == nil {
		createErr := markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
		h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
		return statepkg.Association{}, createErr
	}
	formatID, formatErr := strconv.Atoi(createdEdition.ReadingFormatID)
	if createdEdition.ID != strconv.Itoa(result.EditionID) || createdEdition.BookID != record.HardcoverBookID {
		createErr := markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
		h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
		return statepkg.Association{}, createErr
	}
	if formatErr != nil || formatID != models.ReadingFormatID(format) {
		var createErr error = markEditionCreateRemoteOutcomeAmbiguous(errHardcoverEditionIdentityConflict)
		if format == models.ReadingFormatAudiobook {
			createErr = &hardcover.RegionalAudiobookWrongFormatError{BookID: bookID, EditionID: result.EditionID, ReadingFormatID: createdEdition.ReadingFormatID}
		}
		h.finalizeEditionCreateAction(profile.Profile.ID, response, createErr)
		return statepkg.Association{}, createErr
	}
	status := "created"
	if result.Existing {
		status = "existing"
	}
	association := createEditionAssociation(item, record.HardcoverBookID, strconv.Itoa(result.EditionID), "", "", format, "api_"+format+"_"+status)
	*response = editionCreateResponse{
		ABSItemID: item.ID, ReadingFormat: format, Status: status,
		HardcoverBookID: record.HardcoverBookID, HardcoverEditionID: strconv.Itoa(result.EditionID),
		sourceEdition: createdEdition, action: response.action,
	}
	response.action.Outcome = editionOutcomeCreated
	response.action.HTTPStatus = http.StatusBadGateway
	response.action.Error = editionAssociationSaveFailureGuidance
	response.action.ErrorCode = "edition_association_save_failed"
	response.action.Data = &sync.EditionActionData{
		HardcoverBookID: record.HardcoverBookID, HardcoverEditionID: strconv.Itoa(result.EditionID),
	}
	h.persistEditionActionBestEffort(profile.Profile.ID, response.action)
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

func editionAuthorIDs(ctx context.Context, client editionCreateHardcoverClient, book *models.HardcoverBook, sourceAuthor string) ([]int, error) {
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
		return nil, fmt.Errorf("failed to resolve required edition author: %w", err)
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
	if len(ids) > 1 {
		return nil, fmt.Errorf("%w: multiple Hardcover authors named %q match; resolve the duplicate author records before creating an edition", errEditionCreateInvalidInput, name)
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

func (h *Handler) editionCreateHardcoverClient(profileID, token string) editionCreateHardcoverClient {
	if h.editionCreateHardcoverFactory != nil {
		return h.editionCreateHardcoverFactory(token)
	}
	client := h.multiUserService.NewHardcoverClientForProfile(profileID, token)
	return editionCreateHardcoverAdapter{Client: client, log: &h.log}
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

const (
	editionOutcomeNotSubmitted     = "not_submitted"
	editionOutcomeUnconfirmed      = "unconfirmed"
	editionOutcomeTransportUnknown = "transport_unknown"
	editionOutcomeFailed           = "failed"
	editionOutcomeCreated          = "created"
)

func submittedEditionActionBody(request editionCreateRequest) *sync.EditionActionSubmittedBody {
	return &sync.EditionActionSubmittedBody{
		RunID: request.RunID, ABSItemID: request.ABSItemID, AudibleIdentifier: request.AudibleIdentifier,
		Title: request.Title, Subtitle: request.Subtitle, ASIN: request.ASIN,
		ISBN10: request.ISBN10, ISBN13: request.ISBN13, ReleaseDate: request.ReleaseDate,
		EditionFormat: request.EditionFormat, Resync: request.Resync,
	}
}

func editionActionDataFromRecovery(recovery *editionRecoveryData) *sync.EditionActionData {
	if recovery == nil {
		return nil
	}
	return &sync.EditionActionData{
		AudibleIdentifier: recovery.AudibleIdentifier,
		HardcoverBookID:   recovery.HardcoverBookID,
		RecoveryToken:     recovery.RecoveryToken,
		RecoveryExpiresAt: recovery.RecoveryExpiresAt,
	}
}

func editionRecoveryFromAction(data *sync.EditionActionData) *editionRecoveryData {
	if data == nil || data.RecoveryToken == "" {
		return nil
	}
	return &editionRecoveryData{
		AudibleIdentifier: data.AudibleIdentifier, HardcoverBookID: data.HardcoverBookID,
		RecoveryToken: data.RecoveryToken, RecoveryExpiresAt: data.RecoveryExpiresAt,
	}
}

func (h *Handler) persistEditionAction(profileID string, action *sync.EditionActionRecord, response *editionCreateResponse) error {
	if action == nil || action.SubmittedBody == nil {
		return errors.New("edition action and submitted body are required")
	}
	if response != nil {
		response.action = action
	}
	if err := h.multiUserService.SaveEditionAction(profileID, action.SubmittedBody.RunID, action.SubmittedBody.ABSItemID, *action); err != nil {
		return fmt.Errorf("%w: failed to persist edition action before catalogue write: %w", multiuser.ErrEditionCreateLocalFailure, err)
	}
	return nil
}

func (h *Handler) persistEditionActionBestEffort(profileID string, action *sync.EditionActionRecord) {
	if action == nil || action.SubmittedBody == nil {
		return
	}
	if err := h.multiUserService.SaveEditionAction(profileID, action.SubmittedBody.RunID, action.SubmittedBody.ABSItemID, *action); err != nil {
		h.log.Warn("Edition action result could not be finalized; the pre-write journal remains in place", map[string]interface{}{
			"profile_id": profileID, "run_id": action.SubmittedBody.RunID,
			"abs_item_id": action.SubmittedBody.ABSItemID, "error": err.Error(),
		})
	}
}

func editionActionOutcomeForCreateError(err error) string {
	switch {
	case errors.Is(err, errEditionImportUnconfirmed), errors.Is(err, errEditionRecoveryIdentityUnconfirmed):
		return editionOutcomeUnconfirmed
	case errors.Is(err, errEditionCreateRemoteOutcomeAmbiguous):
		return editionOutcomeTransportUnknown
	case errors.Is(err, multiuser.ErrEditionAssociationSaveAfterRemoteSuccess):
		return editionOutcomeCreated
	}
	_, outcome := editionCreateErrorMetadata(err)
	return outcome
}

func editionActionHTTPStatus(err error) int {
	switch {
	case errors.Is(err, errEditionAudnexusConfirmationRequired):
		return http.StatusBadRequest
	case errors.Is(err, errEditionCreateInsufficientBudget), errors.Is(err, errEditionCreateDiscoveryBudget),
		errors.Is(err, edition.ErrCreateEditionInsufficientMutationBudget), errors.Is(err, edition.ErrCreateEditionPreMutation),
		errors.Is(err, errEditionImportUnconfirmed), errors.Is(err, audnex.ErrRateLimited), errors.Is(err, audnex.ErrTransient),
		errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable
	case errors.Is(err, multiuser.ErrEditionCreateLocalFailure):
		return http.StatusInternalServerError
	case errors.Is(err, errEditionCreateRemoteOutcomeAmbiguous):
		if errors.Is(err, errHardcoverEditionIdentityConflict) || errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict) {
			return http.StatusConflict
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout) {
			return http.StatusServiceUnavailable
		}
		return http.StatusBadGateway
	case errors.Is(err, hardcover.ErrRegionalAudiobookWrongFormat), errors.Is(err, errEditionRecoveryIdentityUnconfirmed),
		errors.Is(err, errStaleEditionCreateRun), errors.Is(err, errEditionCreateSourceChanged),
		errors.Is(err, multiuser.ErrEditionAssociationAlreadyExists), errors.Is(err, multiuser.ErrEditionActionRequiresResolution),
		errors.Is(err, errEditionAudnexusConfirmationChanged),
		errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict):
		return http.StatusConflict
	case errors.Is(err, errEditionCreateInvalidInput), errors.Is(err, errAudibleRegionUnknown),
		errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput):
		return http.StatusUnprocessableEntity
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportFailed):
		return http.StatusBadGateway
	case errors.Is(err, hardcover.ErrMutationScopeDenied):
		return http.StatusForbidden
	default:
		return http.StatusBadGateway
	}
}

func (h *Handler) finalizeEditionCreateAction(profileID string, response *editionCreateResponse, err error) {
	if response == nil || response.action == nil {
		return
	}
	action := response.action
	action.Outcome = editionActionOutcomeForCreateError(err)
	if errors.Is(err, errEditionCreateRemoteOutcomeAmbiguous) && response.recovery != nil && response.recovery.RecoveryToken != "" {
		// Audiobook attempts have a bounded read-only recovery path and keep the
		// same unconfirmed outcome as the immediate HTTP response.
		action.Outcome = editionOutcomeUnconfirmed
	}
	action.HTTPStatus = editionActionHTTPStatus(err)
	action.Error = editionCreatePublicErrorMessage(err)
	action.ErrorCode, _ = editionCreateErrorMetadata(err)
	if action.Outcome == editionOutcomeFailed {
		var wrongFormat *hardcover.RegionalAudiobookWrongFormatError
		if errors.As(err, &wrongFormat) {
			guidance := editionWrongFormatMessage(wrongFormat)
			editionID := strconv.Itoa(wrongFormat.EditionID)
			action.Error = guidance
			action.Data = &sync.EditionActionData{
				HardcoverBookID: strconv.Itoa(wrongFormat.BookID), HardcoverEditionID: editionID,
				ReadingFormatID:     wrongFormat.ReadingFormatID,
				HardcoverEditionURL: "https://hardcover.app/editions/" + editionID,
				Guidance:            guidance,
			}
		} else {
			action.Data = nil
		}
	} else if action.Outcome == editionOutcomeNotSubmitted {
		action.Data = nil
	}
	h.persistEditionActionBestEffort(profileID, action)
}

func (h *Handler) markEditionRecoveryUnavailable(profileID string, action *sync.EditionActionRecord) {
	if action == nil {
		return
	}
	if action.Data == nil {
		action.Data = &sync.EditionActionData{}
	}
	action.Outcome = editionOutcomeTransportUnknown
	action.HTTPStatus = http.StatusConflict
	action.ErrorCode = "edition_recovery_unavailable"
	action.Error = editionRecoveryUnavailableGuidance
	action.Data.RecoveryToken = ""
	action.Data.Guidance = editionRecoveryUnavailableGuidance
	h.persistEditionActionBestEffort(profileID, action)
}

func (h *Handler) writeEditionCreateStructuredError(w http.ResponseWriter, status int, message, errorCode, outcome string, recovery *editionRecoveryData) {
	var data *editionRecoveryData
	if (outcome == editionOutcomeUnconfirmed || outcome == editionOutcomeTransportUnknown || outcome == editionOutcomeCreated) && recovery != nil {
		data = recovery
	}
	h.writeJSONResponse(w, status, APIResponse{
		Success: false, Error: message, ErrorCode: errorCode, Outcome: outcome, Data: data,
	})
}

func (h *Handler) writeEditionCreateErrorWithAction(w http.ResponseWriter, profileID string, err error, recovery *editionRecoveryData, action *sync.EditionActionRecord) {
	if errors.Is(err, multiuser.ErrEditionActionRequiresResolution) && action != nil {
		message := editionActionPublicErrorMessage(action)
		if message == "" {
			message = "A previous edition request must be resolved before another create can be submitted."
		}
		action.Error = message
		h.writeJSONResponse(w, http.StatusConflict, APIResponse{
			Success: false, Error: message, ErrorCode: action.ErrorCode,
			Outcome: action.Outcome, Data: action.Data, EditionAction: action,
		})
		return
	}
	errorCode, outcome := editionCreateErrorMetadata(err)
	respond := func(status int) {
		h.writeEditionCreateStructuredError(w, status, editionCreatePublicErrorMessage(err), errorCode, outcome, recovery)
	}
	var wrongFormat *hardcover.RegionalAudiobookWrongFormatError
	if errors.As(err, &wrongFormat) {
		h.writeEditionWrongFormatError(w, errorCode, outcome, wrongFormat)
		return
	}
	switch {
	case errors.Is(err, multiuser.ErrProfileNotFound):
		respond(http.StatusNotFound)
	case errors.Is(err, multiuser.ErrSyncAlreadyActive), errors.Is(err, multiuser.ErrProfileDeleting), errors.Is(err, multiuser.ErrEditionAssociationAlreadyExists), errors.Is(err, errStaleEditionCreateRun), errors.Is(err, errEditionCreateSourceChanged), errors.Is(err, errEditionAudnexusConfirmationChanged):
		respond(http.StatusConflict)
	case errors.Is(err, multiuser.ErrServiceShuttingDown):
		respond(http.StatusServiceUnavailable)
	case errors.Is(err, errEditionCreateInsufficientBudget), errors.Is(err, edition.ErrCreateEditionInsufficientMutationBudget):
		switch {
		case errors.Is(err, hardcover.ErrMutationDailyQuotaLow):
			respond(http.StatusServiceUnavailable)
		case errors.Is(err, hardcover.ErrMutationDailyQuotaExhausted):
			respond(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Retry-After", "1")
			respond(http.StatusServiceUnavailable)
		}
	case errors.Is(err, errEditionCreateDiscoveryBudget):
		respond(http.StatusServiceUnavailable)
	case errors.Is(err, multiuser.ErrProfileStateBusy):
		w.Header().Set("Retry-After", "1")
		respond(http.StatusTooManyRequests)
	case errors.Is(err, multiuser.ErrEditionCreateDryRun):
		respond(http.StatusConflict)
	case errors.Is(err, multiuser.ErrEditionCreateLocalFailure):
		h.log.Error(fmt.Sprintf("Local profile or state failure before edition creation for profile %s: %v", profileID, err))
		respond(http.StatusInternalServerError)
	case errors.Is(err, hardcover.ErrMutationScopeDenied):
		respond(http.StatusForbidden)
	case errors.Is(err, edition.ErrCreateEditionPreMutation):
		if errors.Is(err, edition.ErrEditionBelongsToOtherBook) {
			respond(http.StatusConflict)
			return
		}
		h.log.Error(fmt.Sprintf("Hardcover edition preparation failed before insertion for profile %s: %v", profileID, err))
		respond(http.StatusServiceUnavailable)
	case errors.Is(err, multiuser.ErrEditionAssociationSaveAfterRemoteSuccess):
		h.log.Error(fmt.Sprintf("Hardcover returned a verified edition but association save failed for profile %s: %v", profileID, err))
		respond(http.StatusBadGateway)
	case errors.Is(err, errEditionImportUnconfirmed):
		respond(http.StatusServiceUnavailable)
	case errors.Is(err, errEditionRecoveryIdentityUnconfirmed):
		respond(http.StatusConflict)
	case errors.Is(err, errEditionCreateRemoteOutcomeAmbiguous):
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout):
			respond(http.StatusServiceUnavailable)
		case errors.Is(err, errHardcoverEditionIdentityConflict), errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict):
			respond(http.StatusConflict)
		default:
			h.log.Error(fmt.Sprintf("Hardcover edition result could not be confirmed for profile %s: %v", profileID, err))
			respond(http.StatusBadGateway)
		}
	case errors.Is(err, errEditionCreateInvalidInput):
		respond(http.StatusUnprocessableEntity)
	case errors.Is(err, errEditionAudnexusConfirmationRequired):
		respond(http.StatusBadRequest)
	case errors.Is(err, errAudibleRegionUnknown):
		respond(http.StatusUnprocessableEntity)
	case errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput):
		respond(http.StatusUnprocessableEntity)
	case errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict):
		respond(http.StatusConflict)
	case errors.Is(err, audnex.ErrRateLimited), errors.Is(err, audnex.ErrTransient), errors.Is(err, context.DeadlineExceeded):
		respond(http.StatusServiceUnavailable)
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout):
		respond(http.StatusServiceUnavailable)
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportFailed):
		respond(http.StatusBadGateway)
	case errors.Is(err, errHardcoverEditionIdentityConflict):
		respond(http.StatusConflict)
	default:
		h.log.Error(fmt.Sprintf("Failed to create edition for profile %s: %v", profileID, err))
		respond(http.StatusBadGateway)
	}
}

// editionCreatePublicErrorMessage is the shared projection from an internal
// create failure to the message returned to the caller and saved in the
// edition-action journal. Detailed wrapped errors remain available to logs.
func editionCreatePublicErrorMessage(err error) string {
	var wrongFormat *hardcover.RegionalAudiobookWrongFormatError
	if errors.As(err, &wrongFormat) {
		return editionWrongFormatMessage(wrongFormat)
	}
	if errors.Is(err, errEditionImportUnconfirmed) && errors.Is(err, errEditionCreateSourceChanged) {
		return errEditionCreateSourceChanged.Error()
	}
	switch {
	case errors.Is(err, multiuser.ErrProfileNotFound):
		return "Sync profile not found"
	case errors.Is(err, multiuser.ErrSyncAlreadyActive), errors.Is(err, multiuser.ErrProfileDeleting), errors.Is(err, multiuser.ErrEditionAssociationAlreadyExists), errors.Is(err, errStaleEditionCreateRun), errors.Is(err, errEditionCreateSourceChanged):
		return err.Error()
	case errors.Is(err, errEditionAudnexusConfirmationRequired):
		return errEditionAudnexusConfirmationRequired.Error()
	case errors.Is(err, errEditionAudnexusConfirmationChanged):
		return errEditionAudnexusConfirmationChanged.Error()
	case errors.Is(err, multiuser.ErrServiceShuttingDown):
		return "Edition creation service is shutting down; retry shortly"
	case errors.Is(err, errEditionCreateInsufficientBudget), errors.Is(err, edition.ErrCreateEditionInsufficientMutationBudget):
		switch {
		case errors.Is(err, hardcover.ErrMutationDailyQuotaLow):
			return editionCreateQuotaLowGuidance
		case errors.Is(err, hardcover.ErrMutationDailyQuotaExhausted):
			return editionCreateQuotaExhaustedGuidance
		default:
			return editionCreateRequestTooSlowGuidance
		}
	case errors.Is(err, errEditionCreateDiscoveryBudget):
		return editionCreateDiscoveryBudgetGuidance
	case errors.Is(err, multiuser.ErrProfileStateBusy):
		return "Profile sync state is busy; retry shortly"
	case errors.Is(err, multiuser.ErrEditionCreateDryRun):
		return "Edition creation is disabled while this profile is in dry run"
	case errors.Is(err, multiuser.ErrEditionCreateLocalFailure):
		return editionCreateLocalFailureGuidance
	case errors.Is(err, hardcover.ErrMutationScopeDenied):
		return "Hardcover token is missing catalogue write permission; no edition was created"
	case errors.Is(err, edition.ErrCreateEditionPreMutation):
		if errors.Is(err, edition.ErrEditionBelongsToOtherBook) {
			return editionCreateExistingBookConflictGuidance
		}
		return editionCreateExistingEditionCheckGuidance
	case errors.Is(err, multiuser.ErrEditionAssociationSaveAfterRemoteSuccess):
		return editionAssociationSaveFailureGuidance
	case errors.Is(err, errEditionImportUnconfirmed):
		return editionImportUnconfirmedGuidance
	case errors.Is(err, errEditionRecoveryIdentityUnconfirmed):
		return editionImportIdentityUnconfirmedGuidance
	case errors.Is(err, errEditionCreateRemoteOutcomeAmbiguous):
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout):
			return editionImportOutcomeAmbiguousGuidance
		case errors.Is(err, errHardcoverEditionIdentityConflict), errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict):
			return editionCreateAmbiguousIdentityGuidance
		default:
			return editionImportOutcomeAmbiguousGuidance
		}
	case errors.Is(err, errEditionCreateInvalidInput), errors.Is(err, errAudibleRegionUnknown), errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput), errors.Is(err, hardcover.ErrRegionalAudiobookIdentityConflict):
		return err.Error()
	case errors.Is(err, audnex.ErrRateLimited), errors.Is(err, audnex.ErrTransient), errors.Is(err, context.DeadlineExceeded):
		return "Audiobookshelf, Audnexus, or Hardcover is temporarily unavailable; retry the edition create"
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout):
		return "Hardcover regional import timed out; retry the edition create"
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportFailed):
		return editionImportFailureGuidance
	case errors.Is(err, errHardcoverEditionIdentityConflict):
		return err.Error()
	default:
		return editionCreateFailedGuidance
	}
}

// editionActionPublicErrorMessage projects older journal rows that may still
// contain internal error strings. Messages emitted by the current public
// projection are retained exactly, while known log-only categories receive
// the same safe guidance used by the create endpoint.
func editionActionPublicErrorMessage(action *sync.EditionActionRecord) string {
	if action == nil || action.Error == "" {
		return ""
	}
	if knownPublicEditionActionError(action.ErrorCode, action.Error) {
		return action.Error
	}
	switch action.ErrorCode {
	case "edition_create_local_failure":
		return editionCreateLocalFailureGuidance
	case "edition_create_not_submitted":
		return "The edition request was not submitted to Hardcover. Retry the edition create."
	case "hardcover_import_unconfirmed":
		return editionImportOutcomeAmbiguousGuidance
	case "edition_association_save_failed":
		return editionAssociationSaveFailureGuidance
	case "hardcover_import_failed":
		return editionImportFailureGuidance
	case "edition_create_failed":
		return editionCreateFailedGuidance
	default:
		// Validation and explicit conflict messages are public API guidance and
		// must retain their existing detail.
		return action.Error
	}
}

func knownPublicEditionActionError(errorCode, message string) bool {
	switch errorCode {
	case "edition_create_local_failure":
		return message == editionCreateLocalFailureGuidance
	case "edition_create_not_submitted":
		return message == editionCreateRequestTooSlowGuidance ||
			message == editionCreateQuotaLowGuidance ||
			message == editionCreateQuotaExhaustedGuidance ||
			message == editionCreateDiscoveryBudgetGuidance ||
			message == editionCreateExistingEditionCheckGuidance ||
			message == editionCreateExistingBookConflictGuidance
	case "hardcover_import_unconfirmed":
		return message == editionImportUnconfirmedGuidance ||
			message == editionImportIdentityUnconfirmedGuidance ||
			message == editionImportOutcomeAmbiguousGuidance ||
			message == editionCreateAmbiguousIdentityGuidance
	case "edition_association_save_failed":
		return message == editionAssociationSaveFailureGuidance
	case "hardcover_import_failed":
		return message == editionImportFailureGuidance
	case "edition_create_failed":
		return message == editionCreateFailedGuidance
	default:
		return false
	}
}

func projectEditionCreateActionErrors(snapshot *sync.SyncSnapshot) {
	if snapshot == nil {
		return
	}
	for i := range snapshot.BookOutcomes {
		if action := snapshot.BookOutcomes[i].EditionAction; action != nil {
			action.Error = editionActionPublicErrorMessage(action)
		}
	}
}

// editionWrongFormatData identifies the existing Hardcover edition that a
// insertion or regional import resolved to, so the user can ask Hardcover to
// correct it.
type editionWrongFormatData struct {
	HardcoverBookID     string `json:"hardcover_book_id"`
	HardcoverEditionID  string `json:"hardcover_edition_id"`
	ReadingFormatID     string `json:"reading_format_id,omitempty"`
	HardcoverEditionURL string `json:"hardcover_edition_url"`
}

// writeEditionWrongFormatError reports a definitive edition result on
// the reviewed book whose verified edition is not an audiobook. No recovery
// data is offered; the existing correction dialog links to that edition.
func (h *Handler) writeEditionWrongFormatError(w http.ResponseWriter, errorCode, outcome string, wrongFormat *hardcover.RegionalAudiobookWrongFormatError) {
	editionID := strconv.Itoa(wrongFormat.EditionID)
	message := editionWrongFormatMessage(wrongFormat)
	h.writeJSONResponse(w, http.StatusConflict, APIResponse{
		Success: false, Error: message, ErrorCode: errorCode, Outcome: outcome,
		Data: editionWrongFormatData{
			HardcoverBookID: strconv.Itoa(wrongFormat.BookID), HardcoverEditionID: editionID,
			ReadingFormatID:     wrongFormat.ReadingFormatID,
			HardcoverEditionURL: "https://hardcover.app/editions/" + editionID,
		},
	})
}

func editionWrongFormatMessage(wrongFormat *hardcover.RegionalAudiobookWrongFormatError) string {
	editionID := strconv.Itoa(wrongFormat.EditionID)
	return fmt.Sprintf("Hardcover returned existing edition %s, which Hardcover lists as %s, not an audiobook. The match was not saved, and trying again returns the same edition. Report the problem on Hardcover so the edition's format can be corrected, then run a new sync.",
		editionID, hardcoverReadingFormatName(wrongFormat.ReadingFormatID))
}

// hardcoverReadingFormatName names a Hardcover reading_format_id for users.
func hardcoverReadingFormatName(formatID string) string {
	switch formatID {
	case "1":
		return "a physical book"
	case "3":
		return "both physical and audiobook formats"
	case "4":
		return "an ebook"
	default:
		return "another format"
	}
}

func editionCreateErrorMetadata(err error) (errorCode, outcome string) {
	switch {
	case errors.Is(err, hardcover.ErrRegionalAudiobookWrongFormat):
		return "hardcover_edition_wrong_format", editionOutcomeFailed
	case errors.Is(err, errEditionCreateRemoteOutcomeAmbiguous), errors.Is(err, errEditionImportUnconfirmed), errors.Is(err, errEditionRecoveryIdentityUnconfirmed):
		return "hardcover_import_unconfirmed", editionOutcomeUnconfirmed
	case errors.Is(err, multiuser.ErrEditionAssociationSaveAfterRemoteSuccess):
		return "edition_association_save_failed", editionOutcomeCreated
	case errors.Is(err, errEditionRecoveryInvalid):
		return "edition_recovery_invalid", editionOutcomeNotSubmitted
	case errors.Is(err, hardcover.ErrRegionalAudiobookImportFailed):
		return "hardcover_import_failed", editionOutcomeFailed
	case errors.Is(err, hardcover.ErrMutationScopeDenied):
		return "hardcover_write_permission_denied", editionOutcomeNotSubmitted
	case errors.Is(err, multiuser.ErrEditionCreateDryRun):
		return "edition_create_dry_run", editionOutcomeNotSubmitted
	case errors.Is(err, errEditionAudnexusConfirmationRequired):
		return "edition_create_invalid_request", editionOutcomeNotSubmitted
	case errors.Is(err, errEditionAudnexusConfirmationChanged):
		return "edition_create_audnexus_changed", editionOutcomeNotSubmitted
	case errors.Is(err, errEditionCreateInvalidInput), errors.Is(err, errAudibleRegionUnknown), errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput):
		return "edition_create_invalid_input", editionOutcomeNotSubmitted
	case errors.Is(err, errStaleEditionCreateRun), errors.Is(err, errEditionCreateSourceChanged), errors.Is(err, multiuser.ErrEditionAssociationAlreadyExists):
		return "edition_create_conflict", editionOutcomeNotSubmitted
	case errors.Is(err, errEditionCreateInsufficientBudget), errors.Is(err, errEditionCreateDiscoveryBudget), errors.Is(err, edition.ErrCreateEditionInsufficientMutationBudget), errors.Is(err, edition.ErrCreateEditionPreMutation):
		return "edition_create_not_submitted", editionOutcomeNotSubmitted
	case errors.Is(err, multiuser.ErrEditionCreateLocalFailure):
		return "edition_create_local_failure", editionOutcomeNotSubmitted
	default:
		return "edition_create_failed", editionOutcomeNotSubmitted
	}
}
