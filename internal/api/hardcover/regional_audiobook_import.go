package hardcover

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

const (
	// audiblePlatformID is the Hardcover platform ID for Audible. The regional
	// import flow was verified against platform 32 in the implementation plan.
	audiblePlatformID = 32

	regionalImportPollInterval = time.Second
	regionalImportMaxWait      = 30 * time.Second
)

var (
	ErrRegionalAudiobookInvalidInput     = errors.New("invalid regional audiobook import input")
	ErrRegionalAudiobookImportFailed     = errors.New("regional audiobook import failed")
	ErrRegionalAudiobookImportTimeout    = errors.New("regional audiobook import timed out")
	ErrRegionalAudiobookIdentityConflict = errors.New("regional audiobook import returned conflicting identity")
	ErrRegionalAudiobookDryRun           = errors.New("regional audiobook import skipped during dry run")
)

// RegionalAudiobookInput identifies an audiobook import for a known Hardcover
// book using an Audible ASIN and its confirmed region.
type RegionalAudiobookInput struct {
	BookID int
	ASIN   string
	Region string
}

// RegionalAudiobookStatus is the terminal state reported by Hardcover's
// regional book import status query.
type RegionalAudiobookStatus string

const (
	RegionalAudiobookLoaded  RegionalAudiobookStatus = "loaded"
	RegionalAudiobookCreated RegionalAudiobookStatus = "created"
)

// RegionalAudiobookResult contains identity confirmed by a fresh edition read.
type RegionalAudiobookResult struct {
	Status             RegionalAudiobookStatus
	BookID             int
	EditionID          int
	ReadingFormatID    int
	RegionalExternalID string
}

// ImportRegionalAudiobook imports or resolves a region-qualified Audible ID
// for an existing Hardcover book. It waits for Hardcover's import status to
// reach a terminal state, then verifies the returned edition belongs to the
// requested book and has audiobook reading format before reporting success.
func (c *Client) ImportRegionalAudiobook(ctx context.Context, input RegionalAudiobookInput) (*RegionalAudiobookResult, error) {
	if c.dryRun {
		return nil, ErrRegionalAudiobookDryRun
	}

	asin := strings.ToUpper(strings.TrimSpace(input.ASIN))
	region := strings.ToLower(strings.TrimSpace(input.Region))
	if input.BookID <= 0 || len(asin) != 10 || !isASIN(asin) || !audnexregion.IsRegion(region) {
		return nil, fmt.Errorf("%w: book ID, ten-character ASIN, and supported region are required", ErrRegionalAudiobookInvalidInput)
	}
	externalID := asin + ":" + region

	mutation := `
mutation UpsertRegionalAudibleBook($book: CreateBookFromPlatformInput!) {
  upsert_book(book: $book) {
    id
    status
    book { id }
    edition { id book_id reading_format_id }
    edition_id
    errors
  }
}`
	variables := map[string]interface{}{
		"book": map[string]interface{}{
			"book_id":     input.BookID,
			"external_id": externalID,
			"platform_id": audiblePlatformID,
		},
	}
	var mutationResponse struct {
		UpsertBook struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
			Book   *struct {
				ID int `json:"id"`
			} `json:"book"`
			Edition   *regionalImportEdition `json:"edition"`
			EditionID *int                   `json:"edition_id"`
			Errors    []string               `json:"errors"`
		} `json:"upsert_book"`
	}
	if err := c.GraphQLMutation(ctx, mutation, variables, &mutationResponse); err != nil {
		return nil, fmt.Errorf("upsert regional Audible book: %w", err)
	}
	upsert := mutationResponse.UpsertBook
	if len(upsert.Errors) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrRegionalAudiobookImportFailed, strings.Join(upsert.Errors, "; "))
	}
	if strings.EqualFold(strings.TrimSpace(upsert.Status), "failed") {
		return nil, fmt.Errorf("%w: Hardcover reported failed status", ErrRegionalAudiobookImportFailed)
	}
	if upsert.Book != nil && upsert.Book.ID != 0 && upsert.Book.ID != input.BookID {
		return nil, fmt.Errorf("%w: requested book %d, upsert returned book %d", ErrRegionalAudiobookIdentityConflict, input.BookID, upsert.Book.ID)
	}
	if err := validateRegionalEditionLink(pointerInt(upsert.EditionID), upsert.Edition, "upsert"); err != nil {
		return nil, err
	}
	if err := validateRegionalImportEdition(upsert.Edition, input.BookID); err != nil {
		return nil, err
	}

	status, editionID, err := c.pollRegionalAudiobookImport(ctx, input.BookID, externalID, upsert.Status, upsert.EditionID, upsert.Edition)
	if err != nil {
		return nil, err
	}
	verified, err := c.GetEditionUncached(ctx, strconv.Itoa(editionID))
	if err != nil {
		return nil, fmt.Errorf("verify regional Audible edition %d: %w", editionID, err)
	}
	if verified == nil {
		return nil, fmt.Errorf("%w: edition %d could not be read back", ErrRegionalAudiobookIdentityConflict, editionID)
	}
	verifiedEditionID, editionErr := strconv.Atoi(verified.ID)
	verifiedBookID, bookErr := strconv.Atoi(verified.BookID)
	verifiedFormatID, formatErr := strconv.Atoi(verified.ReadingFormatID)
	if editionErr != nil || bookErr != nil || formatErr != nil || verifiedEditionID != editionID || verifiedBookID != input.BookID || verifiedFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) {
		return nil, fmt.Errorf("%w: expected edition %d on book %d with audiobook format, got edition %q book %q format %q", ErrRegionalAudiobookIdentityConflict, editionID, input.BookID, verified.ID, verified.BookID, verified.ReadingFormatID)
	}

	return &RegionalAudiobookResult{
		Status: status, BookID: verifiedBookID, EditionID: editionID,
		ReadingFormatID: verifiedFormatID, RegionalExternalID: externalID,
	}, nil
}

type regionalImportEdition struct {
	ID              int  `json:"id"`
	BookID          int  `json:"book_id"`
	ReadingFormatID *int `json:"reading_format_id"`
}

type regionalImportMapping struct {
	ID         int                    `json:"id"`
	State      string                 `json:"state"`
	BookID     int                    `json:"book_id"`
	PlatformID int                    `json:"platform_id"`
	ExternalID string                 `json:"external_id"`
	EditionID  *int                   `json:"edition_id"`
	Edition    *regionalImportEdition `json:"edition"`
}

type regionalImportStatus struct {
	Status     string `json:"status"`
	BookID     *int   `json:"book_id"`
	EditionID  *int   `json:"edition_id"`
	ExternalID string `json:"external_id"`
	PlatformID int    `json:"platform_id"`
	Error      string `json:"error"`
}

func (c *Client) pollRegionalAudiobookImport(ctx context.Context, bookID int, externalID, mutationStatus string, mutationEditionID *int, mutationEdition *regionalImportEdition) (RegionalAudiobookStatus, int, error) {
	// book_import_statuses is documented in Hardcover's current upstream
	// schema. Its response for an unmapped regional loaded import has not been
	// independently verified against a live API token.
	query := `
query RegionalAudibleImport($entries: [ImportStatusEntryInput!]!, $platformId: Int!, $externalId: String!) {
  book_import_statuses(entries: $entries) {
    status
    book_id
    edition_id
    external_id
    platform_id
    error
  }
  book_mappings(where: {platform_id: {_eq: $platformId}, external_id: {_eq: $externalId}}, limit: 2) {
    id
    state
    book_id
    platform_id
    external_id
    edition_id
    edition { id book_id reading_format_id }
  }
	}`
	pollCtx, cancel := context.WithTimeout(ctx, regionalImportMaxWait)
	defer cancel()
	for {
		var response struct {
			Statuses []regionalImportStatus  `json:"book_import_statuses"`
			Mappings []regionalImportMapping `json:"book_mappings"`
		}
		if err := c.GraphQLQuery(pollCtx, query, map[string]interface{}{
			"entries": []map[string]interface{}{{
				"platform_id": audiblePlatformID,
				"external_id": externalID,
			}},
			"platformId": audiblePlatformID,
			"externalId": externalID,
		}, &response); err != nil {
			if ctx.Err() == nil && errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
				return "", 0, fmt.Errorf("%w: regional Audible import did not reach a terminal state", ErrRegionalAudiobookImportTimeout)
			}
			return "", 0, fmt.Errorf("poll regional Audible import: %w", err)
		}
		if len(response.Statuses) > 1 {
			return "", 0, fmt.Errorf("%w: Audible identifier %s matched multiple import statuses", ErrRegionalAudiobookIdentityConflict, externalID)
		}
		if len(response.Mappings) > 1 {
			return "", 0, fmt.Errorf("%w: Audible identifier %s matched multiple book mappings", ErrRegionalAudiobookIdentityConflict, externalID)
		}
		if len(response.Statuses) == 1 {
			importStatus := &response.Statuses[0]
			if importStatus.PlatformID != audiblePlatformID || importStatus.ExternalID != externalID {
				return "", 0, fmt.Errorf("%w: import status did not match requested Audible identifier", ErrRegionalAudiobookIdentityConflict)
			}
			state := strings.ToLower(strings.TrimSpace(importStatus.Status))
			switch state {
			case "failed":
				return "", 0, fmt.Errorf("%w: %s", ErrRegionalAudiobookImportFailed, firstNonEmpty(importStatus.Error, "Hardcover marked regional import failed"))
			case "not_found":
				return "", 0, fmt.Errorf("%w: %s", ErrRegionalAudiobookImportFailed, firstNonEmpty(importStatus.Error, "Hardcover found no result for the regional Audible identifier"))
			case "fetching":
				// Continue polling while Hardcover resolves its upstream source.
			case string(RegionalAudiobookLoaded), string(RegionalAudiobookCreated):
				if importStatus.BookID == nil || *importStatus.BookID != bookID || importStatus.EditionID == nil || *importStatus.EditionID <= 0 {
					return "", 0, fmt.Errorf("%w: completed import status returned book %v and edition %v for requested book %d", ErrRegionalAudiobookIdentityConflict, importStatus.BookID, importStatus.EditionID, bookID)
				}
				if upsertStatus := strings.ToLower(strings.TrimSpace(mutationStatus)); upsertStatus == "failed" {
					return "", 0, fmt.Errorf("%w: upsert response reported failed status", ErrRegionalAudiobookImportFailed)
				} else if (upsertStatus == string(RegionalAudiobookLoaded) || upsertStatus == string(RegionalAudiobookCreated)) && upsertStatus != state {
					return "", 0, fmt.Errorf("%w: upsert reported %s while import status reported %s", ErrRegionalAudiobookIdentityConflict, upsertStatus, state)
				}
				if mutationID := firstPositiveInt(pointerInt(mutationEditionID), regionalEditionID(mutationEdition)); mutationID > 0 && mutationID != *importStatus.EditionID {
					return "", 0, fmt.Errorf("%w: upsert returned edition %d but import status returned edition %d", ErrRegionalAudiobookIdentityConflict, mutationID, *importStatus.EditionID)
				}
				if err := validateRegionalImportEdition(mutationEdition, bookID); err != nil {
					return "", 0, err
				}
				if len(response.Mappings) == 1 {
					if err := validateRegionalImportMapping(response.Mappings[0], bookID, externalID, RegionalAudiobookStatus(state), *importStatus.EditionID); err != nil {
						return "", 0, err
					}
				}
				return RegionalAudiobookStatus(state), *importStatus.EditionID, nil
			default:
				return "", 0, fmt.Errorf("%w: Hardcover returned unsupported regional import status %q", ErrRegionalAudiobookImportFailed, importStatus.Status)
			}
		}
		if len(response.Mappings) == 1 {
			if err := validateRegionalImportMapping(response.Mappings[0], bookID, externalID, "", 0); err != nil {
				return "", 0, err
			}
		}
		select {
		case <-ctx.Done():
			return "", 0, fmt.Errorf("regional audiobook import polling canceled: %w", ctx.Err())
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return "", 0, fmt.Errorf("regional audiobook import polling canceled: %w", ctx.Err())
			}
			return "", 0, fmt.Errorf("%w: regional Audible import did not reach a terminal state", ErrRegionalAudiobookImportTimeout)
		case <-time.After(regionalImportPollInterval):
		}
	}
}

func validateRegionalEditionLink(editionID int, edition *regionalImportEdition, source string) error {
	if editionID < 0 {
		return fmt.Errorf("%w: %s returned invalid edition ID %d", ErrRegionalAudiobookIdentityConflict, source, editionID)
	}
	if edition != nil && edition.ID > 0 && editionID > 0 && edition.ID != editionID {
		return fmt.Errorf("%w: %s returned edition ID %d but nested edition ID %d", ErrRegionalAudiobookIdentityConflict, source, editionID, edition.ID)
	}
	return nil
}

func validateRegionalImportMapping(mapping regionalImportMapping, bookID int, externalID string, status RegionalAudiobookStatus, editionID int) error {
	if mapping.BookID != bookID || mapping.PlatformID != audiblePlatformID || mapping.ExternalID != externalID {
		return fmt.Errorf("%w: mapping %d did not match requested book and Audible identifier", ErrRegionalAudiobookIdentityConflict, mapping.ID)
	}
	if err := validateRegionalEditionLink(pointerInt(mapping.EditionID), mapping.Edition, "mapping"); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(mapping.State), "failed") {
		return fmt.Errorf("%w: Hardcover marked mapping %d failed", ErrRegionalAudiobookImportFailed, mapping.ID)
	}
	mappingStatus := RegionalAudiobookStatus(strings.ToLower(strings.TrimSpace(mapping.State)))
	if status != "" && (mappingStatus == RegionalAudiobookLoaded || mappingStatus == RegionalAudiobookCreated) && mappingStatus != status {
		return fmt.Errorf("%w: mapping %d reported %s while import status reported %s", ErrRegionalAudiobookIdentityConflict, mapping.ID, mappingStatus, status)
	}
	mappingEditionID := firstPositiveInt(pointerInt(mapping.EditionID), regionalEditionID(mapping.Edition))
	if editionID > 0 && mappingEditionID > 0 && mappingEditionID != editionID {
		return fmt.Errorf("%w: mapping %d returned edition %d but import status returned edition %d", ErrRegionalAudiobookIdentityConflict, mapping.ID, mappingEditionID, editionID)
	}
	return validateRegionalImportEdition(mapping.Edition, bookID)
}

func validateRegionalImportEdition(edition *regionalImportEdition, bookID int) error {
	if edition == nil {
		return nil
	}
	if edition.ID < 0 || (edition.BookID != 0 && edition.BookID != bookID) {
		return fmt.Errorf("%w: expected book %d, upsert returned edition book %d", ErrRegionalAudiobookIdentityConflict, bookID, edition.BookID)
	}
	if edition.ReadingFormatID != nil && *edition.ReadingFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) {
		return fmt.Errorf("%w: returned edition %d has reading format %d", ErrRegionalAudiobookIdentityConflict, edition.ID, *edition.ReadingFormatID)
	}
	return nil
}

func pointerInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func regionalEditionID(edition *regionalImportEdition) int {
	if edition == nil {
		return 0
	}
	return edition.ID
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func isASIN(value string) bool {
	for _, character := range value {
		if !((character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')) {
			return false
		}
	}
	return true
}
