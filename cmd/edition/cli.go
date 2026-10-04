package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

const (
	maxCreateInputBytes = 1 << 20
	// createHardcoverTimeout bounds each catalogue write, its polling, and its
	// read-back, matching the create API's request timeout.
	createHardcoverTimeout = 65 * time.Second
	// createMutationReserve is the time that must remain when a catalogue
	// mutation is sent, matching the create API. It covers regional import
	// polling and read-back, and makes the client send the mutation at most once.
	createMutationReserve = 35 * time.Second
)

type editionCreateInput struct {
	edition.EditionInput
	ABSItemID  string `json:"abs_item_id,omitempty"`
	ASINRegion string `json:"asin_region,omitempty"`
	Region     string `json:"region,omitempty"`
}

type createOptions struct {
	InputPath                   string
	ABSItemID                   string
	StateFile                   string
	PreferredRegion             string
	DryRun                      bool
	ConfirmIdentifierCorrection bool
	ConfirmAudnexus             bool
	ConfirmationReader          io.Reader
	PreviewWriter               io.Writer
}

type createOutput struct {
	Success            bool                        `json:"success"`
	Status             string                      `json:"status,omitempty"`
	BookID             int                         `json:"book_id,omitempty"`
	EditionID          int                         `json:"edition_id"`
	ImageID            int                         `json:"image_id"`
	ImageError         string                      `json:"image_error,omitempty"`
	Existing           bool                        `json:"existing,omitempty"`
	ReadingFormat      string                      `json:"reading_format,omitempty"`
	ABSItemID          string                      `json:"abs_item_id,omitempty"`
	AssociationSaved   bool                        `json:"association_saved"`
	Warning            string                      `json:"warning,omitempty"`
	AudnexusRegion     string                      `json:"audnexus_region,omitempty"`
	AudnexusRecord     *edition.AudnexusRecord     `json:"audnexus_record,omitempty"`
	AudnexusComparison *edition.AudnexusComparison `json:"audnexus_comparison,omitempty"`
}

type createServices struct {
	fetchABSItem        func(context.Context, string) (*models.AudiobookshelfBook, error)
	discoverAudible     func(context.Context, string, string) (string, error)
	getAudibleBook      func(context.Context, string, string) (*audnex.Book, error)
	discoverAudibleBook func(context.Context, string, string) (*audnex.Book, string, error)
	importAudiobook     func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error)
	createEdition       func(context.Context, *edition.EditionInput) (*edition.EditionResult, error)
	getEditionUncached  func(context.Context, string) (*models.Edition, error)
}

func newCreateServices(cfg *config.Config, log *logger.Logger, dryRun bool) (createServices, error) {
	clientConfig := hardcoverClientConfig(cfg)
	hc := hardcover.NewClientWithConfig(clientConfig, cfg.Hardcover.Token, log)
	hc.SetDryRun(dryRun)
	creator := edition.NewCreator(hc, log, dryRun, cfg.Audiobookshelf.Token)
	if err := creator.SetAudiobookshelfNetworkTrust(cfg.Audiobookshelf.NetworkTrust); err != nil {
		return createServices{}, fmt.Errorf("invalid Audiobookshelf network trust: %w", err)
	}
	if err := creator.SetAudiobookshelfBaseURL(cfg.Audiobookshelf.URL); err != nil {
		return createServices{}, fmt.Errorf("invalid Audiobookshelf URL: %w", err)
	}
	audnexClient := audnex.NewClient(log)
	return createServices{
		fetchABSItem: func(ctx context.Context, itemID string) (*models.AudiobookshelfBook, error) {
			if strings.TrimSpace(cfg.Audiobookshelf.URL) == "" {
				return nil, errors.New("audiobookshelf.url is required when associating an ABS item")
			}
			if strings.TrimSpace(cfg.Audiobookshelf.Token) == "" {
				return nil, errors.New("audiobookshelf.token is required when associating an ABS item")
			}
			abs, err := audiobookshelf.NewClientWithNetworkTrust(
				cfg.Audiobookshelf.URL,
				cfg.Audiobookshelf.Token,
				cfg.Audiobookshelf.NetworkTrust,
			)
			if err != nil {
				return nil, fmt.Errorf("invalid Audiobookshelf client configuration: %w", err)
			}
			return abs.GetLibraryItemByID(ctx, itemID)
		},
		discoverAudible: func(ctx context.Context, asin, preferred string) (string, error) {
			_, region, err := audnexClient.DiscoverBookByASIN(ctx, asin, preferred)
			if err != nil {
				return "", err
			}
			return region, nil
		},
		getAudibleBook: func(ctx context.Context, asin, region string) (*audnex.Book, error) {
			return audnexClient.GetBookByASIN(ctx, asin, region)
		},
		discoverAudibleBook: audnexClient.DiscoverBookByASIN,
		importAudiobook: func(ctx context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			return hc.ImportRegionalAudiobook(ctx, input)
		},
		createEdition: func(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
			return creator.CreateEditionWithMutationReserve(ctx, input, createMutationReserve)
		},
		getEditionUncached: hc.GetEditionUncached,
	}, nil
}

func runCreate(ctx context.Context, options createOptions, services createServices) (result *createOutput, err error) {
	input, err := readEditionCreateInput(options.InputPath)
	if err != nil {
		return nil, err
	}
	if itemID := strings.TrimSpace(options.ABSItemID); itemID != "" {
		input.ABSItemID = itemID
	}
	input.ABSItemID = strings.TrimSpace(input.ABSItemID)
	format := strings.ToLower(strings.TrimSpace(input.ReadingFormat))
	switch format {
	case "", models.ReadingFormatAudiobook:
		input.ReadingFormat = models.ReadingFormatAudiobook
	case models.ReadingFormatEbook:
		input.ReadingFormat = models.ReadingFormatEbook
	default:
		return nil, fmt.Errorf("invalid reading_format %q, expected audiobook or ebook", input.ReadingFormat)
	}
	unanchoredAudible := input.ReadingFormat == models.ReadingFormatAudiobook && input.BookID == 0
	if err := validateCreateInput(&input.EditionInput, input.ABSItemID != "", unanchoredAudible); err != nil {
		return nil, err
	}

	statePath := strings.TrimSpace(options.StateFile)
	if statePath == "" {
		statePath = state.DefaultStateFile
	}
	associationStatePath := statePath
	var loadedState *state.State
	var fileLock *state.FileLock
	defer func() {
		if fileLock != nil {
			if closeErr := fileLock.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("failed to release sync state lock: %w", closeErr)
				result = nil
			}
		}
	}()
	loadAssociationState := func() error {
		if input.ABSItemID == "" || fileLock != nil {
			return nil
		}
		acquired, lockErr := state.AcquireFileLock(statePath)
		if lockErr != nil {
			return fmt.Errorf("failed to lock sync state file: %w", lockErr)
		}
		fileLock = acquired
		associationStatePath = fileLock.StatePath()
		if associationStatePath == "" {
			return errors.New("state file lock did not resolve a state path")
		}
		loadedState, err = state.LoadState(associationStatePath)
		if err != nil {
			return fmt.Errorf("failed to load sync state: %w", err)
		}
		if _, exists := loadedState.GetAssociation(input.ABSItemID); exists {
			return fmt.Errorf("Audiobookshelf item %q already has a confirmed Hardcover association", input.ABSItemID)
		}
		return nil
	}
	if input.ABSItemID != "" && !unanchoredAudible {
		if err := loadAssociationState(); err != nil {
			return nil, err
		}
	}

	var absItem *models.AudiobookshelfBook
	identifierWarning := ""
	if input.ABSItemID != "" {
		if services.fetchABSItem == nil {
			return nil, errors.New("Audiobookshelf item verification is unavailable")
		}
		absItem, err = services.fetchABSItem(ctx, input.ABSItemID)
		if err != nil {
			return nil, fmt.Errorf("failed to verify Audiobookshelf item %q: %w", input.ABSItemID, err)
		}
		if err := validateABSCreateItem(absItem, input.ABSItemID, input.ReadingFormat); err != nil {
			return nil, err
		}
	}

	if input.ReadingFormat == models.ReadingFormatAudiobook {
		input.ASIN = audiobookASIN(input.ASIN, absItem)
		if input.ASIN != "" {
			input.ASINRegion, err = input.selectedRegion()
			if err != nil {
				return nil, err
			}
		} else if input.ISBN10 == "" && input.ISBN13 == "" {
			return nil, errors.New("an ASIN or ISBN is required")
		}
	}
	if absItem != nil {
		identifierWarning = sourceIdentifierWarning(absItem, input)
		if identifierWarning != "" && !options.ConfirmIdentifierCorrection {
			return nil, fmt.Errorf("%s; review the item and input, then pass --confirm-identifier-correction to proceed", identifierWarning)
		}
	}

	if input.ReadingFormat == models.ReadingFormatAudiobook && input.ASIN != "" {
		var region string
		var audibleRecord *edition.AudnexusRecord
		var audibleComparison *edition.AudnexusComparison
		var audnexusConfirmedAt time.Time
		if unanchoredAudible {
			book, foundRegion, previewErr := resolveAudnexusPreview(ctx, input.ASIN, input.ASINRegion, options.PreferredRegion, services)
			if previewErr != nil {
				return nil, previewErr
			}
			region = foundRegion
			record := edition.BuildAudnexusRecord(book)
			audibleRecord = &record
			if absItem != nil {
				comparison := edition.CompareAudnexus(absItem, record)
				audibleComparison = &comparison
			}
			if err := writeAudnexusPreview(options.PreviewWriter, absItem, region, record, audibleComparison); err != nil {
				return nil, fmt.Errorf("failed to display Audnexus preview: %w", err)
			}
		} else {
			var regionErr error
			region, regionErr = resolveAudibleRegion(ctx, input.ASIN, input.ASINRegion, options.PreferredRegion, services)
			if regionErr != nil {
				return nil, regionErr
			}
		}
		if options.DryRun {
			if unanchoredAudible && input.ABSItemID != "" {
				if err := loadAssociationState(); err != nil {
					return nil, err
				}
			}
			return &createOutput{
				Success:            true,
				Status:             "dry_run",
				BookID:             input.BookID,
				ReadingFormat:      input.ReadingFormat,
				ABSItemID:          input.ABSItemID,
				AssociationSaved:   false,
				Warning:            identifierWarning,
				AudnexusRegion:     regionIfUnanchored(unanchoredAudible, region),
				AudnexusRecord:     audibleRecord,
				AudnexusComparison: audibleComparison,
			}, nil
		}
		if unanchoredAudible && !options.ConfirmAudnexus {
			confirmed, confirmErr := confirmAudnexusRecord(options.ConfirmationReader, options.PreviewWriter)
			if confirmErr != nil {
				return nil, confirmErr
			}
			if !confirmed {
				return nil, errors.New("Audnexus record was not confirmed; no Hardcover changes were made")
			}
		}
		if unanchoredAudible {
			audnexusConfirmedAt = time.Now().UTC()
			if input.ABSItemID != "" {
				if err := loadAssociationState(); err != nil {
					return nil, err
				}
				freshItem, fetchErr := services.fetchABSItem(ctx, input.ABSItemID)
				if fetchErr != nil {
					return nil, fmt.Errorf("failed to reverify Audiobookshelf item %q after Audnexus confirmation: %w", input.ABSItemID, fetchErr)
				}
				if validateErr := validateABSCreateItem(freshItem, input.ABSItemID, input.ReadingFormat); validateErr != nil {
					return nil, fmt.Errorf("Audiobookshelf item %q changed while awaiting Audnexus confirmation: %w", input.ABSItemID, validateErr)
				}
				if !sameABSSourceSnapshot(absItem, freshItem) {
					return nil, fmt.Errorf("Audiobookshelf item %q changed while awaiting Audnexus confirmation; no Hardcover changes were made", input.ABSItemID)
				}
				absItem = freshItem
			}
		}
		if services.importAudiobook == nil {
			return nil, errors.New("regional audiobook import is unavailable")
		}
		mutationCtx, cancel := withMutationBudget(ctx)
		resolved, importErr := services.importAudiobook(mutationCtx, hardcover.RegionalAudiobookInput{
			BookID:     input.BookID,
			ASIN:       input.ASIN,
			Region:     region,
			Unanchored: unanchoredAudible,
		})
		cancel()
		if importErr != nil {
			return nil, audiobookImportError(importErr)
		}
		if resolved == nil {
			return nil, errors.New("regional audiobook import returned no result")
		}
		if resolved.Status != hardcover.RegionalAudiobookLoaded && resolved.Status != hardcover.RegionalAudiobookCreated {
			return nil, fmt.Errorf("regional audiobook import returned unsupported status %q", resolved.Status)
		}
		expectedExternalID := strings.ToUpper(input.ASIN) + ":" + region
		if resolved.BookID <= 0 || (!unanchoredAudible && resolved.BookID != input.BookID) || resolved.EditionID <= 0 || resolved.ReadingFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) || resolved.RegionalExternalID != expectedExternalID {
			return nil, fmt.Errorf("regional audiobook import returned an identity or reading format that did not match the requested Audible identifier %q", expectedExternalID)
		}
		output := &createOutput{
			Success:            true,
			Status:             string(resolved.Status),
			BookID:             resolved.BookID,
			EditionID:          resolved.EditionID,
			ReadingFormat:      models.ReadingFormatAudiobook,
			ABSItemID:          input.ABSItemID,
			AssociationSaved:   false,
			Warning:            identifierWarning,
			AudnexusRegion:     regionIfUnanchored(unanchoredAudible, region),
			AudnexusRecord:     audibleRecord,
			AudnexusComparison: audibleComparison,
		}
		if absItem != nil {
			var association state.Association
			if unanchoredAudible {
				association, err = state.NewAudibleImportAssociation(absItem, resolved, resolved.RegionalExternalID, audnexusConfirmedAt)
				if err != nil {
					return nil, fmt.Errorf("Hardcover reported audiobook %s, but the verified local association could not be prepared: %w", resolved.Status, err)
				}
			} else {
				association = audiobookAssociation(absItem, input.ASIN, resolved)
			}
			if err := saveAssociation(loadedState, associationStatePath, association); err != nil {
				return nil, fmt.Errorf("Hardcover reported audiobook %s, but the local association could not be saved. Verify the Hardcover result before retrying; retrying may create another edition: %w", resolved.Status, err)
			}
			output.AssociationSaved = true
		}
		return output, nil
	}

	if err := input.EditionInput.Validate(); err != nil {
		return nil, fmt.Errorf("invalid %s input: %w", input.ReadingFormat, err)
	}
	if options.DryRun {
		// The Creator returns a zero edition ID in dry-run mode without writing.
		if services.createEdition == nil {
			return nil, fmt.Errorf("%s edition creation is unavailable", input.ReadingFormat)
		}
		created, createErr := services.createEdition(ctx, &input.EditionInput)
		if createErr != nil {
			return nil, fmt.Errorf("failed to create %s edition: %w", input.ReadingFormat, createErr)
		}
		if created == nil || !created.Success {
			return nil, fmt.Errorf("%s dry run did not return a successful result", input.ReadingFormat)
		}
		output := editionOutput(created, input, "dry_run")
		output.Warning = identifierWarning
		return output, nil
	}
	if services.createEdition == nil {
		return nil, fmt.Errorf("%s edition creation is unavailable", input.ReadingFormat)
	}
	mutationCtx, cancel := withMutationBudget(ctx)
	defer cancel()
	created, createErr := services.createEdition(mutationCtx, &input.EditionInput)
	if createErr != nil {
		return nil, editionCreateError(input.ReadingFormat, createErr)
	}
	if created == nil || !created.Success || created.EditionID <= 0 {
		return nil, fmt.Errorf("%s edition creation returned no confirmed edition", input.ReadingFormat)
	}
	output := editionOutput(created, input, "created")
	output.Warning = identifierWarning
	if created.Existing {
		output.Status = "existing"
	}
	if services.getEditionUncached == nil {
		return nil, fmt.Errorf("Hardcover returned %s edition %d, but verification is unavailable. Check Hardcover before retrying; retrying may create another edition", input.ReadingFormat, created.EditionID)
	}
	if err := verifyCreatedEdition(mutationCtx, created.EditionID, input.BookID, input.ReadingFormat, services.getEditionUncached); err != nil {
		return nil, fmt.Errorf("Hardcover returned %s edition %d, but it could not be verified. Check Hardcover before retrying; retrying may create another edition: %w", input.ReadingFormat, created.EditionID, err)
	}
	if absItem != nil {
		association := editionAssociation(absItem, input.ASIN, output.Status, input.BookID, created.EditionID, input.ReadingFormat)
		if err := saveAssociation(loadedState, associationStatePath, association); err != nil {
			return nil, fmt.Errorf("Hardcover returned %s edition %d, but the local association could not be saved. Verify the Hardcover result before retrying; retrying may create another edition: %w", input.ReadingFormat, created.EditionID, err)
		}
		output.AssociationSaved = true
	}
	return output, nil
}

// withMutationBudget bounds a catalogue write like the create API does. The
// Hardcover client then refuses to send the mutation without the reserve left
// and never retries it after an attempt that may have reached Hardcover.
func withMutationBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, createHardcoverTimeout)
	return hardcover.WithMinimumMutationBudget(ctx, createMutationReserve), cancel
}

// audiobookImportError says whether a failed regional import could have
// changed Hardcover, so the operator knows whether a retry is safe.
func audiobookImportError(err error) error {
	switch {
	case errors.Is(err, hardcover.ErrMutationInsufficientBudget):
		return fmt.Errorf("failed to import audiobook: too little time remained to send the import, and no Hardcover change was made; retry: %w", err)
	case errors.Is(err, hardcover.ErrMutationScopeDenied):
		return fmt.Errorf("failed to import audiobook: Hardcover catalogue write permission is required, and no edition was created: %w", err)
	case errors.Is(err, hardcover.ErrRegionalAudiobookInvalidInput),
		errors.Is(err, hardcover.ErrRegionalAudiobookDryRun),
		errors.Is(err, hardcover.ErrRegionalAudiobookImportFailed):
		return fmt.Errorf("failed to import audiobook: %w", err)
	default:
		return fmt.Errorf("failed to import audiobook: Hardcover may have processed the import; verify the book in Hardcover before retrying: %w", err)
	}
}

// editionCreateError says whether a failed edition insertion could have changed
// Hardcover, so the operator knows whether a retry is safe.
func editionCreateError(readingFormat string, err error) error {
	switch {
	case errors.Is(err, hardcover.ErrMutationInsufficientBudget),
		errors.Is(err, edition.ErrCreateEditionInsufficientMutationBudget):
		return fmt.Errorf("failed to create %s edition: too little time remained to send the insertion, and no Hardcover change was made; retry: %w", readingFormat, err)
	case errors.Is(err, hardcover.ErrMutationScopeDenied):
		return fmt.Errorf("failed to create %s edition: Hardcover catalogue write permission is required, and no edition was created: %w", readingFormat, err)
	case errors.Is(err, edition.ErrCreateEditionPreMutation):
		return fmt.Errorf("failed to create %s edition: %w", readingFormat, err)
	default:
		return fmt.Errorf("failed to create %s edition: Hardcover may have processed the insertion; verify the book in Hardcover before retrying: %w", readingFormat, err)
	}
}

func validateABSCreateItem(item *models.AudiobookshelfBook, expectedID, expectedFormat string) error {
	if item == nil || strings.TrimSpace(item.ID) != expectedID {
		return fmt.Errorf("Audiobookshelf returned a different item than %q", expectedID)
	}
	if item.ReadingFormat() != expectedFormat {
		return fmt.Errorf("Audiobookshelf item %q is %s, but input reading_format is %s", expectedID, item.ReadingFormat(), expectedFormat)
	}
	return nil
}

// sameABSSourceSnapshot compares the identifiers retained with an association.
// ISBN separators and ASIN casing do not change the identity being confirmed.
func sameABSSourceSnapshot(before, after *models.AudiobookshelfBook) bool {
	if before == nil || after == nil || strings.TrimSpace(before.ID) != strings.TrimSpace(after.ID) || before.ReadingFormat() != after.ReadingFormat() {
		return false
	}
	beforeASIN, beforeISBN10, beforeISBN13 := state.SourceIdentifiers(before.Media.Metadata.ASIN, before.Media.Metadata.ISBN)
	afterASIN, afterISBN10, afterISBN13 := state.SourceIdentifiers(after.Media.Metadata.ASIN, after.Media.Metadata.ISBN)
	return strings.EqualFold(beforeASIN, afterASIN) && isbn.Normalize(beforeISBN10) == isbn.Normalize(afterISBN10) && isbn.Normalize(beforeISBN13) == isbn.Normalize(afterISBN13)
}

func readEditionCreateInput(path string) (editionCreateInput, error) {
	file, err := os.Open(path)
	if err != nil {
		return editionCreateInput{}, fmt.Errorf("failed to read input file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCreateInputBytes+1))
	if err != nil {
		return editionCreateInput{}, fmt.Errorf("failed to read input file: %w", err)
	}
	if len(data) > maxCreateInputBytes {
		return editionCreateInput{}, fmt.Errorf("input file exceeds %d bytes", maxCreateInputBytes)
	}
	var input editionCreateInput
	if err := json.Unmarshal(data, &input); err != nil {
		return editionCreateInput{}, fmt.Errorf("invalid JSON input: %w", err)
	}
	return input, nil
}

func (input editionCreateInput) selectedRegion() (string, error) {
	asinRegion := strings.ToLower(strings.TrimSpace(input.ASINRegion))
	regionAlias := strings.ToLower(strings.TrimSpace(input.Region))
	if asinRegion != "" && regionAlias != "" && asinRegion != regionAlias {
		return "", errors.New("asin_region and region specify different Audible regions")
	}
	if asinRegion == "" {
		asinRegion = regionAlias
	}
	if asinRegion != "" && !audnexregion.IsRegion(asinRegion) {
		return "", fmt.Errorf("unknown Audible region %q", asinRegion)
	}
	return asinRegion, nil
}

func validateCreateInput(input *edition.EditionInput, hasABSItem, allowUnanchoredAudible bool) error {
	if input.BookID < 0 || input.BookID == 0 && !allowUnanchoredAudible {
		return errors.New("book_id must be a positive Hardcover book ID")
	}
	format := strings.ToLower(strings.TrimSpace(input.ReadingFormat))
	switch format {
	case "", models.ReadingFormatAudiobook:
		input.ReadingFormat = models.ReadingFormatAudiobook
	case models.ReadingFormatEbook:
		input.ReadingFormat = models.ReadingFormatEbook
	default:
		return fmt.Errorf("invalid reading_format %q, expected audiobook or ebook", input.ReadingFormat)
	}
	if allowUnanchoredAudible && input.ReadingFormat != models.ReadingFormatAudiobook {
		return errors.New("book_id may be omitted only for an audiobook with a usable Audible ASIN")
	}
	input.ASIN = strings.TrimSpace(input.ASIN)
	input.ISBN10 = strings.TrimSpace(input.ISBN10)
	input.ISBN13 = strings.TrimSpace(input.ISBN13)
	if input.ReadingFormat == models.ReadingFormatAudiobook {
		canonicalASIN, valid := audnex.CanonicalASIN(input.ASIN)
		if valid {
			input.ASIN = canonicalASIN
		} else {
			input.ASIN = ""
		}
		if allowUnanchoredAudible && input.ASIN == "" {
			return errors.New("book_id may be omitted only for an audiobook with a usable Audible ASIN")
		}
		if input.ASIN == "" && input.ISBN10 == "" && input.ISBN13 == "" && !hasABSItem {
			return errors.New("an ASIN or ISBN is required")
		}
	} else if input.ASIN == "" && input.ISBN10 == "" && input.ISBN13 == "" {
		return errors.New("an ASIN or ISBN is required")
	}
	return nil
}

// audiobookASIN applies the identifier precedence for the regional import.
// A valid ABS ASIN is the default, while a valid submitted ASIN can explicitly
// correct it or fill a missing ABS ASIN after correction confirmation.
func audiobookASIN(submitted string, item *models.AudiobookshelfBook) string {
	submittedASIN, submittedValid := audnex.CanonicalASIN(submitted)
	if submittedValid {
		return submittedASIN
	}
	if item != nil {
		if asin, valid := audnex.CanonicalASIN(item.Media.Metadata.ASIN); valid {
			return asin
		}
	}
	return ""
}

// sourceIdentifierWarning identifies submitted values that do not identify the
// fetched ABS item. A correction is allowed only after explicit confirmation.
func sourceIdentifierWarning(item *models.AudiobookshelfBook, input editionCreateInput) string {
	source := item.Media.Metadata
	var conflicts []string
	isAudiobook := input.ReadingFormat == models.ReadingFormatAudiobook
	var sourceASINValid, submittedASINValid bool
	if isAudiobook {
		sourceASIN, valid := audnex.CanonicalASIN(source.ASIN)
		sourceASINValid = valid
		submittedASIN, valid := audnex.CanonicalASIN(input.ASIN)
		submittedASINValid = valid
		if submittedASINValid && (!sourceASINValid || submittedASIN != sourceASIN) {
			conflicts = append(conflicts, "ASIN")
		}
	} else {
		if input.ASIN != "" && !strings.EqualFold(strings.TrimSpace(source.ASIN), input.ASIN) {
			conflicts = append(conflicts, "ASIN")
		}
	}
	if !isAudiobook || (!sourceASINValid && !submittedASINValid) {
		for _, submitted := range []string{input.ISBN10, input.ISBN13} {
			if submitted != "" && !sameISBN(source.ISBN, submitted) {
				conflicts = append(conflicts, "ISBN")
				break
			}
		}
	}
	if len(conflicts) == 0 {
		return ""
	}
	return fmt.Sprintf("WARNING: submitted %s does not match Audiobookshelf item %q; saving this match will pin that item to the selected Hardcover book", strings.Join(conflicts, " and "), item.ID)
}

func sameISBN(source, submitted string) bool {
	sourceISBN, sourceOK := isbn.Parse(source)
	submittedISBN, submittedOK := isbn.Parse(submitted)
	if !sourceOK || !submittedOK {
		return isbn.Normalize(source) != "" && isbn.Normalize(source) == isbn.Normalize(submitted)
	}
	return sourceISBN.Given == submittedISBN.Given || sourceISBN.Counterpart != "" && sourceISBN.Counterpart == submittedISBN.Given
}

// resolveAudibleRegion returns the region for the regional Audible import. An
// explicit region is the user's regional identifier and is used as given, like
// the create API's audible_identifier; otherwise Audnex discovery must find it.
func resolveAudibleRegion(ctx context.Context, asin, requested, preferred string, services createServices) (string, error) {
	if requested != "" {
		return requested, nil
	}
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred != "" && !audnexregion.IsRegion(preferred) {
		return "", fmt.Errorf("configured audiobookshelf.audnexus_region %q is unsupported", preferred)
	}
	if services.discoverAudible == nil {
		return "", errors.New("Audnex region discovery is unavailable")
	}
	region, err := services.discoverAudible(ctx, asin, preferred)
	if err != nil {
		if errors.Is(err, audnex.ErrRateLimited) || errors.Is(err, audnex.ErrTransient) || errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("Audnex region discovery for ASIN %q is temporarily unavailable; retry later or set asin_region: %w", asin, err)
		}
		return "", fmt.Errorf("failed to discover an Audible region for ASIN %q; set asin_region to import it: %w", asin, err)
	}
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return "", fmt.Errorf("Audnex did not find ASIN %q in any supported region; set asin_region to import it", asin)
	}
	if !audnexregion.IsRegion(region) {
		return "", fmt.Errorf("Audnex discovery returned unsupported region %q", region)
	}
	return region, nil
}

// resolveAudnexusPreview fetches the exact user-supplied region or discovers a
// region and returns the record that will be shown and confirmed before import.
func resolveAudnexusPreview(ctx context.Context, asin, requested, preferred string, services createServices) (*audnex.Book, string, error) {
	var book *audnex.Book
	var region string
	var err error
	if requested != "" {
		if services.getAudibleBook == nil {
			return nil, "", errors.New("Audnexus regional lookup is unavailable")
		}
		region = requested
		book, err = services.getAudibleBook(ctx, asin, region)
	} else {
		preferred = strings.ToLower(strings.TrimSpace(preferred))
		if preferred != "" && !audnexregion.IsRegion(preferred) {
			return nil, "", fmt.Errorf("configured audiobookshelf.audnexus_region %q is unsupported", preferred)
		}
		if services.discoverAudibleBook == nil {
			return nil, "", errors.New("Audnexus region discovery is unavailable")
		}
		book, region, err = services.discoverAudibleBook(ctx, asin, preferred)
	}
	if err != nil {
		return nil, "", audnexusPreviewError(asin, region, err)
	}
	if book == nil {
		if requested != "" {
			return nil, "", fmt.Errorf("Audnexus did not find ASIN %q in region %q", asin, requested)
		}
		return nil, "", fmt.Errorf("Audnexus did not find ASIN %q in any supported region; set asin_region to choose a region", asin)
	}
	returnedASIN, validASIN := audnex.CanonicalASIN(book.ASIN)
	if !validASIN || returnedASIN != asin {
		return nil, "", fmt.Errorf("Audnexus returned a record for ASIN %q instead of %q", book.ASIN, asin)
	}
	region = strings.ToLower(strings.TrimSpace(region))
	if !audnexregion.IsRegion(region) {
		return nil, "", fmt.Errorf("Audnexus lookup returned unsupported region %q", region)
	}
	return book, region, nil
}

func audnexusPreviewError(asin, region string, err error) error {
	switch {
	case errors.Is(err, audnex.ErrNotFound):
		return fmt.Errorf("Audnexus did not find ASIN %q in region %q", asin, region)
	case errors.Is(err, audnex.ErrRateLimited):
		return fmt.Errorf("Audnexus rate limited the regional lookup for ASIN %q; retry later: %w", asin, err)
	case errors.Is(err, audnex.ErrTransient), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("Audnexus regional lookup for ASIN %q is temporarily unavailable; retry later: %w", asin, err)
	default:
		return fmt.Errorf("failed to retrieve Audnexus metadata for ASIN %q: %w", asin, err)
	}
}

func regionIfUnanchored(unanchored bool, region string) string {
	if !unanchored {
		return ""
	}
	return region
}

func writeAudnexusPreview(writer io.Writer, absItem *models.AudiobookshelfBook, region string, record edition.AudnexusRecord, comparison *edition.AudnexusComparison) error {
	if writer == nil {
		return nil
	}
	if _, err := fmt.Fprintf(writer, "Audnexus regional record %s:%s\n", displayText(record.ASIN), region); err != nil {
		return err
	}
	audnexusFields := []struct{ name, value string }{
		{"Title", record.Title}, {"Subtitle", record.Subtitle},
		{"Authors", strings.Join(record.Authors, ", ")}, {"Narrators", strings.Join(record.Narrators, ", ")},
		{"Series", strings.Join(record.Series, ", ")}, {"Series position", record.SeriesPosition},
		{"Publisher", record.Publisher}, {"Release date", record.ReleaseDate},
		{"Runtime", displayRuntime(record.RuntimeSeconds)}, {"Language", record.Language}, {"Cover URL", record.CoverURL},
	}
	for _, field := range audnexusFields {
		if err := writePreviewValue(writer, field.name, field.value); err != nil {
			return err
		}
	}
	if absItem == nil {
		return nil
	}
	if _, err := fmt.Fprintf(writer, "Audiobookshelf item %s\n", displayText(absItem.ID)); err != nil {
		return err
	}
	metadata := absItem.Media.Metadata
	absSeries, absPosition := edition.AudiobookshelfSeriesFields(metadata)
	absReleaseDate := strings.TrimSpace(metadata.PublishedDate)
	if absReleaseDate == "" {
		absReleaseDate = strings.TrimSpace(metadata.PublishedYear)
	}
	absFields := []struct{ name, value string }{
		{"Title", metadata.Title}, {"Subtitle", metadata.Subtitle},
		{"Authors", metadata.AuthorName}, {"Narrators", metadata.NarratorName},
		{"Series", absSeries}, {"Series position", absPosition},
		{"Publisher", metadata.Publisher}, {"Release date", absReleaseDate},
		{"Runtime", displayRuntime(int(absItem.Media.Duration + 0.5))}, {"Language", metadata.Language},
		{"Cover path", absItem.Media.CoverPath},
	}
	for _, field := range absFields {
		if err := writePreviewValue(writer, field.name, field.value); err != nil {
			return err
		}
	}
	if comparison == nil {
		return nil
	}
	if _, err := fmt.Fprintln(writer, "Per-field comparison (ABS / Audnexus):"); err != nil {
		return err
	}
	comparisonFields := []struct {
		name, abs, audnexus string
		status              edition.AudnexusFieldStatus
	}{
		{"Title", metadata.Title, record.Title, comparison.Title},
		{"Subtitle", metadata.Subtitle, record.Subtitle, comparison.Subtitle},
		{"Authors", metadata.AuthorName, strings.Join(record.Authors, ", "), comparison.Authors},
		{"Narrators", metadata.NarratorName, strings.Join(record.Narrators, ", "), comparison.Narrators},
		{"Series", absSeries, strings.Join(record.Series, ", "), comparison.Series},
		{"Series position", absPosition, record.SeriesPosition, comparison.SeriesPosition},
		{"Publisher", metadata.Publisher, record.Publisher, comparison.Publisher},
		{"Release date", absReleaseDate, record.ReleaseDate, comparison.ReleaseDate},
		{"Runtime", displayRuntime(int(absItem.Media.Duration + 0.5)), displayRuntime(record.RuntimeSeconds), comparison.Runtime},
		{"Language", metadata.Language, record.Language, comparison.Language},
	}
	for _, field := range comparisonFields {
		if _, err := fmt.Fprintf(writer, "  %s: %s (ABS %s; Audnexus %s)\n", field.name, field.status, displayText(field.abs), displayText(field.audnexus)); err != nil {
			return err
		}
	}
	return nil
}

func writePreviewValue(writer io.Writer, label, value string) error {
	_, err := fmt.Fprintf(writer, "  %s: %s\n", label, displayText(value))
	return err
}

func displayText(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(missing)"
	}
	return strconv.Quote(value)
}

func displayRuntime(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func confirmAudnexusRecord(reader io.Reader, writer io.Writer) (bool, error) {
	if reader == nil {
		return false, errors.New("Audnexus confirmation requires interactive input or --confirm-audnexus")
	}
	if writer != nil {
		if _, err := fmt.Fprint(writer, "Import this audiobook without a Hardcover book ID? Type yes to confirm [y/N]: "); err != nil {
			return false, err
		}
	}
	answer, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("failed to read Audnexus confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func audiobookAssociation(item *models.AudiobookshelfBook, requestedASIN string, resolved *hardcover.RegionalAudiobookResult) state.Association {
	asin, isbn10, isbn13 := state.SourceIdentifiers(item.Media.Metadata.ASIN, item.Media.Metadata.ISBN)
	return state.Association{
		ABSItemID:          item.ID,
		SourceASIN:         asin,
		SourceISBN10:       isbn10,
		SourceISBN13:       isbn13,
		Correction:         asinCorrection(asin, requestedASIN),
		RegionalExternalID: resolved.RegionalExternalID,
		HardcoverBookID:    strconv.Itoa(resolved.BookID),
		HardcoverEditionID: strconv.Itoa(resolved.EditionID),
		ReadingFormat:      models.ReadingFormatAudiobook,
		Provenance:         "cli_regional_" + string(resolved.Status),
	}
}

func verifyCreatedEdition(ctx context.Context, expectedEditionID, expectedBookID int, readingFormat string, getEdition func(context.Context, string) (*models.Edition, error)) error {
	verified, err := getEdition(ctx, strconv.Itoa(expectedEditionID))
	if err != nil {
		return fmt.Errorf("failed to read back Hardcover %s edition %d: %w", readingFormat, expectedEditionID, err)
	}
	if verified == nil {
		return fmt.Errorf("Hardcover %s edition %d could not be read back", readingFormat, expectedEditionID)
	}

	verifiedEditionID, editionErr := strconv.Atoi(strings.TrimSpace(verified.ID))
	verifiedBookID, bookErr := strconv.Atoi(strings.TrimSpace(verified.BookID))
	verifiedFormatID, formatErr := strconv.Atoi(strings.TrimSpace(verified.ReadingFormatID))
	expectedFormatID := models.ReadingFormatID(readingFormat)
	if readingFormat == models.ReadingFormatAudiobook && editionErr == nil && bookErr == nil && formatErr == nil &&
		verifiedEditionID == expectedEditionID && verifiedBookID == expectedBookID && verifiedFormatID != expectedFormatID {
		return &hardcover.RegionalAudiobookWrongFormatError{
			BookID: expectedBookID, EditionID: expectedEditionID, ReadingFormatID: verified.ReadingFormatID,
		}
	}
	if editionErr != nil || bookErr != nil || formatErr != nil ||
		verifiedEditionID != expectedEditionID || verifiedBookID != expectedBookID || verifiedFormatID != expectedFormatID {
		return fmt.Errorf("Hardcover %s edition identity did not match: expected edition %d on book %d with reading format %d, got edition %q book %q format %q",
			readingFormat, expectedEditionID, expectedBookID, expectedFormatID, verified.ID, verified.BookID, verified.ReadingFormatID)
	}
	return nil
}

func editionAssociation(item *models.AudiobookshelfBook, submittedASIN, status string, bookID, editionID int, readingFormat string) state.Association {
	asin, isbn10, isbn13 := state.SourceIdentifiers(item.Media.Metadata.ASIN, item.Media.Metadata.ISBN)
	return state.Association{
		ABSItemID:          item.ID,
		SourceASIN:         asin,
		SourceISBN10:       isbn10,
		SourceISBN13:       isbn13,
		Correction:         asinCorrection(asin, submittedASIN),
		HardcoverBookID:    strconv.Itoa(bookID),
		HardcoverEditionID: strconv.Itoa(editionID),
		ReadingFormat:      readingFormat,
		Provenance:         "cli_" + readingFormat + "_" + status,
	}
}

// asinCorrection returns the submitted ASIN when it differs from the ASIN the
// Audiobookshelf item reports, so the saved match records the user's correction
// alongside the item's own source identifiers.
func asinCorrection(sourceASIN, submittedASIN string) string {
	submittedASIN = strings.TrimSpace(submittedASIN)
	if submittedASIN == "" || strings.EqualFold(strings.TrimSpace(sourceASIN), submittedASIN) {
		return ""
	}
	return submittedASIN
}

func saveAssociation(loadedState *state.State, path string, association state.Association) error {
	if loadedState == nil {
		return errors.New("sync state was not loaded under the state-file lock")
	}
	if err := loadedState.SetAssociation(association); err != nil {
		return err
	}
	if err := loadedState.Save(path); err != nil {
		return err
	}
	return nil
}

func editionOutput(result *edition.EditionResult, input editionCreateInput, status string) *createOutput {
	return &createOutput{
		Success:       result.Success,
		Status:        status,
		BookID:        input.BookID,
		EditionID:     result.EditionID,
		ImageID:       result.ImageID,
		ImageError:    result.ImageError,
		Existing:      result.Existing,
		ReadingFormat: input.ReadingFormat,
		ABSItemID:     input.ABSItemID,
	}
}

func writeJSON(writer io.Writer, value any) error {
	output, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode result: %w", err)
	}
	if _, err := fmt.Fprintln(writer, string(output)); err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	output, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode template: %w", err)
	}
	if err := os.WriteFile(path, output, 0644); err != nil {
		return err
	}
	return nil
}

// hardcoverClientConfig applies the configured Hardcover endpoint and request
// pacing, as the sync service and create API do.
func hardcoverClientConfig(cfg *config.Config) *hardcover.ClientConfig {
	clientConfig := hardcover.DefaultClientConfig()
	if baseURL := strings.TrimSpace(cfg.Hardcover.BaseURL); baseURL != "" {
		clientConfig.BaseURL = baseURL
	}
	if cfg.RateLimit.Rate > 0 {
		clientConfig.RateLimit = cfg.RateLimit.Rate
	}
	if cfg.RateLimit.MaxConcurrent > 0 {
		clientConfig.MaxConcurrent = cfg.RateLimit.MaxConcurrent
	}
	return clientConfig
}

// findConfigPath returns the --config value before command parsing so logging
// can be configured, and whether the path was named explicitly.
func findConfigPath(args []string) (string, bool) {
	path, explicit := defaultConfigPath, false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--":
			return path, explicit
		case arg == "--config" || arg == "-c":
			if index+1 < len(args) {
				index++
				path, explicit = args[index], true
			}
		case strings.HasPrefix(arg, "--config="):
			path, explicit = strings.TrimPrefix(arg, "--config="), true
		case strings.HasPrefix(arg, "-c="):
			path, explicit = strings.TrimPrefix(arg, "-c="), true
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			path, explicit = strings.TrimPrefix(arg, "-c"), true
		}
	}
	return path, explicit
}
