package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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

type editionCreateInput struct {
	edition.EditionInput
	ABSItemID  string `json:"abs_item_id,omitempty"`
	ASINRegion string `json:"asin_region,omitempty"`
	Region     string `json:"region,omitempty"`
}

type createOptions struct {
	InputPath         string
	ABSItemID         string
	StateFile         string
	StateFileExplicit bool
	PreferredRegion   string
	DryRun            bool
}

type createOutput struct {
	Success          bool   `json:"success"`
	Status           string `json:"status,omitempty"`
	BookID           int    `json:"book_id,omitempty"`
	EditionID        int    `json:"edition_id"`
	ImageID          int    `json:"image_id"`
	ImageError       string `json:"image_error,omitempty"`
	Existing         bool   `json:"existing,omitempty"`
	ReadingFormat    string `json:"reading_format,omitempty"`
	ABSItemID        string `json:"abs_item_id,omitempty"`
	AssociationSaved bool   `json:"association_saved"`
}

type createServices struct {
	fetchABSItem       func(context.Context, string) (*models.AudiobookshelfBook, error)
	checkAudibleRegion func(context.Context, string, string) error
	discoverAudible    func(context.Context, string, string) (string, error)
	importAudiobook    func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error)
	createEbook        func(context.Context, *edition.EditionInput) (*edition.EditionResult, error)
	getEditionUncached func(context.Context, string) (*models.Edition, error)
}

func newCreateServices(cfg *config.Config, log *logger.Logger, dryRun bool) (createServices, error) {
	clientConfig := hardcoverClientConfig(cfg.Hardcover.BaseURL)
	hc := hardcover.NewClientWithConfig(clientConfig, cfg.Hardcover.Token, log)
	hc.SetDryRun(dryRun)
	creator := edition.NewCreator(hc, log, dryRun, cfg.Audiobookshelf.Token)
	if err := creator.SetAudiobookshelfNetworkTrust(cfg.Audiobookshelf.NetworkTrust); err != nil {
		return createServices{}, fmt.Errorf("invalid Audiobookshelf network trust: %w", err)
	}
	if err := creator.SetAudiobookshelfBaseURL(cfg.Audiobookshelf.URL); err != nil {
		return createServices{}, fmt.Errorf("invalid Audiobookshelf URL: %w", err)
	}
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
		checkAudibleRegion: func(ctx context.Context, asin, region string) error {
			book, err := audnex.NewClient(log).GetBookByASIN(ctx, asin, region)
			if err != nil {
				return fmt.Errorf("Audnex could not confirm ASIN %q in region %q: %w", asin, region, err)
			}
			if canonical, ok := audnex.CanonicalASIN(book.ASIN); !ok || canonical != strings.ToUpper(asin) {
				return fmt.Errorf("Audnex returned an unexpected ASIN for region %q", region)
			}
			return nil
		},
		discoverAudible: func(ctx context.Context, asin, preferred string) (string, error) {
			_, region, err := audnex.NewClient(log).DiscoverBookByASIN(ctx, asin, preferred)
			if err != nil {
				return "", err
			}
			return region, nil
		},
		importAudiobook: func(ctx context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			return hc.ImportRegionalAudiobook(ctx, input)
		},
		createEbook: func(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
			return creator.CreateEdition(ctx, input)
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
	if input.ABSItemID != "" && (!options.StateFileExplicit || strings.TrimSpace(options.StateFile) == "") {
		return nil, errors.New("--state-file is required when associating an Audiobookshelf item; pass the sync state file for this profile")
	}
	input.ASIN = strings.TrimSpace(input.ASIN)
	input.ISBN10 = strings.TrimSpace(input.ISBN10)
	input.ISBN13 = strings.TrimSpace(input.ISBN13)
	format := strings.ToLower(strings.TrimSpace(input.ReadingFormat))
	switch format {
	case "", models.ReadingFormatAudiobook:
		input.ReadingFormat = models.ReadingFormatAudiobook
	case models.ReadingFormatEbook:
		input.ReadingFormat = models.ReadingFormatEbook
	default:
		return nil, fmt.Errorf("invalid reading_format %q, expected audiobook or ebook", input.ReadingFormat)
	}
	input.ASINRegion, err = input.selectedRegion()
	if err != nil {
		return nil, err
	}
	if err := validateCreateInput(&input.EditionInput); err != nil {
		return nil, err
	}

	statePath := strings.TrimSpace(options.StateFile)
	if statePath == "" {
		statePath = state.DefaultStateFile
	}
	associationStatePath := statePath
	var loadedState *state.State
	if input.ABSItemID != "" {
		fileLock, lockErr := state.AcquireFileLock(statePath)
		if lockErr != nil {
			return nil, fmt.Errorf("failed to lock sync state file: %w", lockErr)
		}
		defer func() {
			if closeErr := fileLock.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("failed to release sync state lock: %w", closeErr)
				result = nil
			}
		}()
		associationStatePath = fileLock.StatePath()
		if associationStatePath == "" {
			return nil, errors.New("state file lock did not resolve a state path")
		}
		loadedState, err = state.LoadState(associationStatePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load sync state: %w", err)
		}
		if _, exists := loadedState.GetAssociation(input.ABSItemID); exists {
			return nil, fmt.Errorf("Audiobookshelf item %q already has a confirmed Hardcover association", input.ABSItemID)
		}
	}

	var absItem *models.AudiobookshelfBook
	if input.ABSItemID != "" {
		if services.fetchABSItem == nil {
			return nil, errors.New("Audiobookshelf item verification is unavailable")
		}
		absItem, err = services.fetchABSItem(ctx, input.ABSItemID)
		if err != nil {
			return nil, fmt.Errorf("failed to verify Audiobookshelf item %q: %w", input.ABSItemID, err)
		}
		if absItem == nil || strings.TrimSpace(absItem.ID) != input.ABSItemID {
			return nil, fmt.Errorf("Audiobookshelf returned a different item than %q", input.ABSItemID)
		}
		if absItem.ReadingFormat() != input.ReadingFormat {
			return nil, fmt.Errorf("Audiobookshelf item %q is %s, but input reading_format is %s", input.ABSItemID, absItem.ReadingFormat(), input.ReadingFormat)
		}
		if err := validateABSItemIdentifiers(absItem, input); err != nil {
			return nil, fmt.Errorf("Audiobookshelf item %q source identifiers do not match the input: %w", input.ABSItemID, err)
		}
	}

	if input.ReadingFormat == models.ReadingFormatAudiobook {
		region, regionErr := resolveAudibleRegion(ctx, input.ASIN, input.ASINRegion, options.PreferredRegion, services)
		if regionErr != nil {
			return nil, regionErr
		}
		if options.DryRun {
			return &createOutput{
				Success:          true,
				Status:           "dry_run",
				BookID:           input.BookID,
				ReadingFormat:    input.ReadingFormat,
				ABSItemID:        input.ABSItemID,
				AssociationSaved: false,
			}, nil
		}
		if services.importAudiobook == nil {
			return nil, errors.New("regional audiobook import is unavailable")
		}
		resolved, importErr := services.importAudiobook(ctx, hardcover.RegionalAudiobookInput{
			BookID: input.BookID,
			ASIN:   input.ASIN,
			Region: region,
		})
		if importErr != nil {
			return nil, fmt.Errorf("failed to import audiobook: %w", importErr)
		}
		if resolved == nil {
			return nil, errors.New("regional audiobook import returned no result")
		}
		if resolved.Status != hardcover.RegionalAudiobookLoaded && resolved.Status != hardcover.RegionalAudiobookCreated {
			return nil, fmt.Errorf("regional audiobook import returned unsupported status %q", resolved.Status)
		}
		expectedExternalID := strings.ToUpper(input.ASIN) + ":" + region
		if resolved.BookID != input.BookID || resolved.EditionID <= 0 || resolved.ReadingFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) || resolved.RegionalExternalID != expectedExternalID {
			return nil, fmt.Errorf("regional audiobook import returned an identity or reading format that did not match book %d", input.BookID)
		}
		output := &createOutput{
			Success:          true,
			Status:           string(resolved.Status),
			BookID:           resolved.BookID,
			EditionID:        resolved.EditionID,
			ReadingFormat:    models.ReadingFormatAudiobook,
			ABSItemID:        input.ABSItemID,
			AssociationSaved: false,
		}
		if absItem != nil {
			association := audiobookAssociation(absItem, input.ASIN, resolved)
			if err := saveAssociation(loadedState, associationStatePath, association); err != nil {
				return nil, fmt.Errorf("Hardcover reported audiobook %s, but the local association could not be saved. Verify the Hardcover result before retrying; retrying may create another edition: %w", resolved.Status, err)
			}
			output.AssociationSaved = true
		}
		return output, nil
	}

	if err := input.EditionInput.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ebook input: %w", err)
	}
	if options.DryRun {
		// The Creator returns a zero edition ID in dry-run mode without writing.
		if services.createEbook == nil {
			return nil, errors.New("ebook edition creation is unavailable")
		}
		created, createErr := services.createEbook(ctx, &input.EditionInput)
		if createErr != nil {
			return nil, fmt.Errorf("failed to create ebook edition: %w", createErr)
		}
		if created == nil || !created.Success {
			return nil, errors.New("ebook dry run did not return a successful result")
		}
		return ebookOutput(created, input, "dry_run", false), nil
	}
	if services.createEbook == nil {
		return nil, errors.New("ebook edition creation is unavailable")
	}
	created, createErr := services.createEbook(ctx, &input.EditionInput)
	if createErr != nil {
		return nil, fmt.Errorf("failed to create ebook edition: %w", createErr)
	}
	if created == nil || !created.Success || created.EditionID <= 0 {
		return nil, errors.New("ebook edition creation returned no confirmed edition")
	}
	output := ebookOutput(created, input, "created", false)
	if created.Existing {
		output.Status = "existing"
	}
	if absItem != nil {
		if services.getEditionUncached == nil {
			return nil, errors.New("Hardcover ebook edition verification is unavailable")
		}
		if err := verifyCreatedEbookEdition(ctx, created.EditionID, input.BookID, services.getEditionUncached); err != nil {
			return nil, err
		}
		association := ebookAssociation(absItem, input.BookID, created.EditionID)
		if err := saveAssociation(loadedState, associationStatePath, association); err != nil {
			return nil, fmt.Errorf("Hardcover returned ebook edition %d, but the local association could not be saved. Verify the Hardcover result before retrying; retrying may create another edition: %w", created.EditionID, err)
		}
		output.AssociationSaved = true
	}
	return output, nil
}

func readEditionCreateInput(path string) (editionCreateInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return editionCreateInput{}, fmt.Errorf("failed to read input file: %w", err)
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

func validateCreateInput(input *edition.EditionInput) error {
	if input.BookID <= 0 {
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
	input.ASIN = strings.TrimSpace(input.ASIN)
	input.ISBN10 = strings.TrimSpace(input.ISBN10)
	input.ISBN13 = strings.TrimSpace(input.ISBN13)
	if input.ASIN == "" && input.ISBN10 == "" && input.ISBN13 == "" {
		return errors.New("an ASIN or ISBN is required")
	}
	if input.ReadingFormat == models.ReadingFormatAudiobook && input.ASIN == "" {
		return errors.New("an audiobook import requires a regional Audible ASIN")
	}
	return nil
}

func resolveAudibleRegion(ctx context.Context, asin, requested, preferred string, services createServices) (string, error) {
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred != "" && !audnexregion.IsRegion(preferred) {
		return "", fmt.Errorf("configured audiobookshelf.audnexus_region %q is unsupported", preferred)
	}
	if requested != "" {
		if services.checkAudibleRegion == nil {
			return "", errors.New("Audnex region confirmation is unavailable")
		}
		if err := services.checkAudibleRegion(ctx, asin, requested); err != nil {
			return "", err
		}
		return requested, nil
	}
	if services.discoverAudible == nil {
		return "", errors.New("Audnex region discovery is unavailable")
	}
	region, err := services.discoverAudible(ctx, asin, preferred)
	if err != nil {
		return "", fmt.Errorf("failed to discover an Audible region for ASIN %q: %w", asin, err)
	}
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return "", fmt.Errorf("Audnex did not find ASIN %q in any supported region", asin)
	}
	if !audnexregion.IsRegion(region) {
		return "", fmt.Errorf("Audnex discovery returned unsupported region %q", region)
	}
	return region, nil
}

func audiobookAssociation(item *models.AudiobookshelfBook, requestedASIN string, resolved *hardcover.RegionalAudiobookResult) state.Association {
	asin, isbn10, isbn13 := sourceIdentifiers(item)
	correction := ""
	if !strings.EqualFold(strings.TrimSpace(asin), strings.TrimSpace(requestedASIN)) {
		correction = strings.TrimSpace(requestedASIN)
	}
	return state.Association{
		ABSItemID:          item.ID,
		SourceASIN:         asin,
		SourceISBN10:       isbn10,
		SourceISBN13:       isbn13,
		Correction:         correction,
		RegionalExternalID: resolved.RegionalExternalID,
		HardcoverBookID:    strconv.Itoa(resolved.BookID),
		HardcoverEditionID: strconv.Itoa(resolved.EditionID),
		ReadingFormat:      models.ReadingFormatAudiobook,
		Provenance:         string(hardcover.ASINMatchAudibleMapping),
	}
}

func verifyCreatedEbookEdition(ctx context.Context, expectedEditionID, expectedBookID int, getEdition func(context.Context, string) (*models.Edition, error)) error {
	verified, err := getEdition(ctx, strconv.Itoa(expectedEditionID))
	if err != nil {
		return fmt.Errorf("failed to read back Hardcover ebook edition %d: %w", expectedEditionID, err)
	}
	if verified == nil {
		return fmt.Errorf("Hardcover ebook edition %d could not be read back", expectedEditionID)
	}

	verifiedEditionID, editionErr := strconv.Atoi(strings.TrimSpace(verified.ID))
	verifiedBookID, bookErr := strconv.Atoi(strings.TrimSpace(verified.BookID))
	verifiedFormatID, formatErr := strconv.Atoi(strings.TrimSpace(verified.ReadingFormatID))
	expectedFormatID := models.ReadingFormatID(models.ReadingFormatEbook)
	if editionErr != nil || bookErr != nil || formatErr != nil ||
		verifiedEditionID != expectedEditionID || verifiedBookID != expectedBookID || verifiedFormatID != expectedFormatID {
		return fmt.Errorf("Hardcover ebook edition identity did not match: expected edition %d on book %d with reading format %d, got edition %q book %q format %q",
			expectedEditionID, expectedBookID, expectedFormatID, verified.ID, verified.BookID, verified.ReadingFormatID)
	}
	return nil
}

func ebookAssociation(item *models.AudiobookshelfBook, bookID, editionID int) state.Association {
	asin, isbn10, isbn13 := sourceIdentifiers(item)
	return state.Association{
		ABSItemID:          item.ID,
		SourceASIN:         asin,
		SourceISBN10:       isbn10,
		SourceISBN13:       isbn13,
		HardcoverBookID:    strconv.Itoa(bookID),
		HardcoverEditionID: strconv.Itoa(editionID),
		ReadingFormat:      models.ReadingFormatEbook,
		Provenance:         "edition_cli",
	}
}

func sourceIdentifiers(item *models.AudiobookshelfBook) (asin, isbn10, isbn13 string) {
	asin = strings.TrimSpace(item.Media.Metadata.ASIN)
	rawISBN := strings.TrimSpace(item.Media.Metadata.ISBN)
	switch len(isbn.Normalize(rawISBN)) {
	case 10:
		isbn10 = rawISBN
	default:
		isbn13 = rawISBN
	}
	return asin, isbn10, isbn13
}

func validateABSItemIdentifiers(item *models.AudiobookshelfBook, input editionCreateInput) error {
	asin, _, _ := sourceIdentifiers(item)
	// For audiobook imports, the submitted ASIN is the explicit regional Audible
	// identifier. It may intentionally differ from the ASIN currently recorded by
	// ABS; the regional resolver confirms that identifier for the target book.
	if input.ReadingFormat != models.ReadingFormatAudiobook && input.ASIN != "" && !strings.EqualFold(strings.TrimSpace(asin), strings.TrimSpace(input.ASIN)) {
		return fmt.Errorf("ASIN %q differs from the fetched item ASIN %q", input.ASIN, asin)
	}

	itemISBN := strings.TrimSpace(item.Media.Metadata.ISBN)
	for _, candidate := range []struct {
		name  string
		value string
	}{
		{name: "ISBN-10", value: input.ISBN10},
		{name: "ISBN-13", value: input.ISBN13},
	} {
		if candidate.value != "" && !sameISBN(candidate.value, itemISBN) {
			return fmt.Errorf("%s %q differs from the fetched item ISBN %q", candidate.name, candidate.value, itemISBN)
		}
	}
	return nil
}

func sameISBN(left, right string) bool {
	leftNormalized, rightNormalized := isbn.Normalize(left), isbn.Normalize(right)
	if leftNormalized == rightNormalized {
		return true
	}
	leftParsed, leftOK := isbn.Parse(leftNormalized)
	rightParsed, rightOK := isbn.Parse(rightNormalized)
	if !leftOK || !rightOK || !leftParsed.Valid || !rightParsed.Valid {
		return false
	}
	return leftParsed.Given == rightParsed.Given ||
		(leftParsed.Counterpart != "" && leftParsed.Counterpart == rightParsed.Given) ||
		(rightParsed.Counterpart != "" && rightParsed.Counterpart == leftParsed.Given) ||
		(leftParsed.Counterpart != "" && leftParsed.Counterpart == rightParsed.Counterpart)
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

func ebookOutput(result *edition.EditionResult, input editionCreateInput, status string, associationSaved bool) *createOutput {
	if result == nil {
		return nil
	}
	return &createOutput{
		Success:          result.Success,
		Status:           status,
		BookID:           input.BookID,
		EditionID:        result.EditionID,
		ImageID:          result.ImageID,
		ImageError:       result.ImageError,
		Existing:         result.Existing,
		ReadingFormat:    models.ReadingFormatEbook,
		ABSItemID:        input.ABSItemID,
		AssociationSaved: associationSaved,
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

func hardcoverClientConfig(baseURL string) *hardcover.ClientConfig {
	clientConfig := hardcover.DefaultClientConfig()
	if strings.TrimSpace(baseURL) != "" {
		clientConfig.BaseURL = strings.TrimSpace(baseURL)
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
