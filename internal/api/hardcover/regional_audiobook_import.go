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

	regionalImportInitialPollInterval = time.Second
	regionalImportMaxPollInterval     = 5 * time.Second
	regionalImportMaxWait             = 30 * time.Second
)

var (
	ErrRegionalAudiobookInvalidInput     = errors.New("invalid regional audiobook import input")
	ErrRegionalAudiobookImportFailed     = errors.New("regional audiobook import failed")
	ErrRegionalAudiobookImportTimeout    = errors.New("regional audiobook import timed out")
	ErrRegionalAudiobookIdentityConflict = errors.New("regional audiobook import returned conflicting identity")
	ErrRegionalAudiobookDryRun           = errors.New("regional audiobook import skipped during dry run")
	// ErrRegionalAudiobookWrongFormat identifies a terminal import whose freshly
	// read edition is on the requested book but is not an audiobook. Hardcover
	// can attach an ISBN-shaped Audible ASIN to an existing edition with that
	// ISBN, so resubmitting or rechecking returns the same edition.
	ErrRegionalAudiobookWrongFormat = errors.New("regional audiobook import returned a non-audiobook edition")
)

// RegionalAudiobookWrongFormatError reports the edition Hardcover returned for
// a regional import when a fresh read shows the requested book but a
// non-audiobook reading format. It matches ErrRegionalAudiobookWrongFormat and
// ErrRegionalAudiobookIdentityConflict.
type RegionalAudiobookWrongFormatError struct {
	BookID          int
	EditionID       int
	ReadingFormatID string
}

func (e *RegionalAudiobookWrongFormatError) Error() string {
	return fmt.Sprintf("%s: edition %d on book %d has reading format %s; expected audiobook (ID 2)",
		ErrRegionalAudiobookIdentityConflict, e.EditionID, e.BookID, regionalReadingFormatDescription(e.ReadingFormatID))
}

func (e *RegionalAudiobookWrongFormatError) Unwrap() []error {
	return []error{ErrRegionalAudiobookWrongFormat, ErrRegionalAudiobookIdentityConflict}
}

// RegionalAudiobookInput identifies a regional Audible audiobook import.
// Unanchored explicitly creates or resolves the Hardcover book from the
// regional identifier without sending book_id. The default anchored mode still
// requires BookID.
type RegionalAudiobookInput struct {
	BookID     int
	ASIN       string
	Region     string
	Unanchored bool
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
	BookTitle          string
	EditionID          int
	ReadingFormatID    int
	RegionalExternalID string
	// Edition is the freshly verified snapshot for reuse within this operation.
	Edition *models.Edition `json:"-"`
}

// ImportRegionalAudiobook imports or resolves a region-qualified Audible ID.
// It waits for Hardcover's import status to reach a terminal state, then
// verifies the returned edition belongs to the returned or requested book and
// has audiobook reading format before reporting success.
func (c *Client) ImportRegionalAudiobook(ctx context.Context, input RegionalAudiobookInput) (*RegionalAudiobookResult, error) {
	if c.dryRun {
		return nil, ErrRegionalAudiobookDryRun
	}

	asin, region, err := normalizeRegionalAudiobookInput(input)
	if err != nil {
		return nil, err
	}
	externalID := asin + ":" + region

	mutation := `
mutation UpsertRegionalAudibleBook($book: CreateBookFromPlatformInput!) {
  upsert_book(book: $book) {
    id
    status
    book { id title }
    edition { id book_id reading_format_id }
    edition_id
    errors
  }
}`
	bookInput := map[string]interface{}{
		"external_id": externalID,
		"platform_id": audiblePlatformID,
	}
	if !input.Unanchored {
		bookInput["book_id"] = input.BookID
	}
	variables := map[string]interface{}{
		"book": bookInput,
	}
	var mutationResponse struct {
		UpsertBook struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
			Book   *struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
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
	resolvedBookID := input.BookID
	if upsert.Book != nil && upsert.Book.ID > 0 {
		if !input.Unanchored && upsert.Book.ID != input.BookID {
			return nil, fmt.Errorf("%w: requested book %d, upsert returned book %d", ErrRegionalAudiobookIdentityConflict, input.BookID, upsert.Book.ID)
		}
		resolvedBookID = upsert.Book.ID
	}
	if err := validateRegionalEditionLink(pointerInt(upsert.EditionID), upsert.Edition, "upsert"); err != nil {
		return nil, err
	}
	if err := validateRegionalImportEdition(upsert.Edition, resolvedBookID); err != nil {
		return nil, err
	}

	status, resolvedBookID, editionID, err := c.pollRegionalAudiobookImport(ctx, resolvedBookID, input.Unanchored, externalID, upsert.Status, upsert.EditionID, upsert.Edition)
	if err != nil {
		return nil, err
	}
	verifiedBookID, verifiedFormatID, verified, err := c.verifyRegionalAudiobookEdition(ctx, resolvedBookID, editionID, "verify regional Audible edition")
	if err != nil {
		return nil, err
	}

	return &RegionalAudiobookResult{
		Status: status, BookID: verifiedBookID, BookTitle: func() string {
			if upsert.Book == nil {
				return ""
			}
			return strings.TrimSpace(upsert.Book.Title)
		}(), EditionID: editionID,
		ReadingFormatID: verifiedFormatID, RegionalExternalID: externalID, Edition: verified,
	}, nil
}

// CheckRegionalAudiobookImport performs one read-only status lookup for an
// already-submitted regional Audible import. A terminal result is returned
// only after its edition is freshly read and verified against the requested
// book and audiobook format. Pending imports return confirmed=false without
// issuing any Hardcover mutation.
func (c *Client) CheckRegionalAudiobookImport(ctx context.Context, input RegionalAudiobookInput) (*RegionalAudiobookResult, bool, error) {
	asin, region, err := normalizeRegionalAudiobookInput(input)
	if err != nil {
		return nil, false, err
	}
	externalID := asin + ":" + region
	statuses, mappings, err := c.queryRegionalAudiobookImport(ctx, externalID)
	if err != nil {
		return nil, false, fmt.Errorf("check regional Audible import: %w", err)
	}
	if len(statuses) > 1 {
		return nil, false, fmt.Errorf("%w: Audible identifier %s matched multiple import statuses", ErrRegionalAudiobookIdentityConflict, externalID)
	}
	if len(mappings) > 1 {
		return nil, false, fmt.Errorf("%w: Audible identifier %s matched multiple book mappings", ErrRegionalAudiobookIdentityConflict, externalID)
	}

	var status RegionalAudiobookStatus
	var resolvedBookID, editionID int
	if len(statuses) == 1 {
		importStatus := statuses[0]
		if importStatus.PlatformID != audiblePlatformID || importStatus.ExternalID != externalID {
			return nil, false, fmt.Errorf("%w: import status did not match requested Audible identifier", ErrRegionalAudiobookIdentityConflict)
		}
		switch state := strings.ToLower(strings.TrimSpace(importStatus.Status)); state {
		case "failed":
			return nil, false, fmt.Errorf("%w: %s", ErrRegionalAudiobookImportFailed, firstNonEmpty(importStatus.Error, "Hardcover marked regional import failed"))
		case "not_found":
			return nil, false, fmt.Errorf("%w: %s", ErrRegionalAudiobookImportFailed, firstNonEmpty(importStatus.Error, "Hardcover found no result for the regional Audible identifier"))
		case "fetching":
			if len(mappings) == 1 {
				if err := validateRegionalImportMapping(mappings[0], input.BookID, externalID, "", 0); err != nil {
					return nil, false, err
				}
			}
			return nil, false, nil
		case string(RegionalAudiobookLoaded), string(RegionalAudiobookCreated):
			if importStatus.BookID != nil && *importStatus.BookID <= 0 {
				return nil, false, fmt.Errorf("%w: completed import status returned nonpositive book ID %d", ErrRegionalAudiobookIdentityConflict, *importStatus.BookID)
			}
			if importStatus.BookID != nil {
				if !input.Unanchored && *importStatus.BookID != input.BookID {
					return nil, false, fmt.Errorf("%w: completed import status returned book %d for requested book %d", ErrRegionalAudiobookIdentityConflict, *importStatus.BookID, input.BookID)
				}
				resolvedBookID = *importStatus.BookID
			} else if input.Unanchored && len(mappings) == 1 {
				if err := validateRegionalImportMapping(mappings[0], 0, externalID, RegionalAudiobookStatus(state), 0); err != nil {
					return nil, false, err
				}
				resolvedBookID = mappings[0].BookID
			} else {
				return nil, false, fmt.Errorf("%w: completed import status did not return a positive Hardcover book ID", ErrRegionalAudiobookIdentityConflict)
			}
			status = RegionalAudiobookStatus(state)
			if importStatus.EditionID != nil && *importStatus.EditionID <= 0 {
				return nil, false, fmt.Errorf("%w: completed import status returned nonpositive edition ID %d", ErrRegionalAudiobookIdentityConflict, *importStatus.EditionID)
			}
			editionID = pointerInt(importStatus.EditionID)
			if editionID <= 0 && input.Unanchored && len(mappings) == 1 {
				editionID = firstPositiveInt(pointerInt(mappings[0].EditionID), regionalEditionID(mappings[0].Edition))
			}
			if resolvedBookID <= 0 || editionID <= 0 {
				return nil, false, fmt.Errorf("%w: completed import did not return positive book and edition IDs", ErrRegionalAudiobookIdentityConflict)
			}
		default:
			return nil, false, fmt.Errorf("%w: Hardcover returned unsupported regional import status %q", ErrRegionalAudiobookIdentityConflict, importStatus.Status)
		}
	} else {
		// The mapping can outlive the import-status row. It is sufficient for
		// recovery only when it itself reports a terminal state and points to
		// one exact edition on the reviewed book.
		if len(mappings) != 1 {
			return nil, false, nil
		}
		mapping := mappings[0]
		mappingState := strings.ToLower(strings.TrimSpace(mapping.State))
		if mappingState == "failed" {
			return nil, false, fmt.Errorf("%w: Hardcover marked mapping %d failed without a terminal failed import status", ErrRegionalAudiobookIdentityConflict, mapping.ID)
		}
		if mappingState != string(RegionalAudiobookLoaded) && mappingState != string(RegionalAudiobookCreated) && mappingState != "normalized" {
			return nil, false, nil
		}
		// Hardcover runtime evidence shows normalized mappings can remain after
		// their import-status row expires. Treat that confirmed mapping as loaded;
		// the fresh edition read below still verifies its exact identity and format.
		mappingStatus := RegionalAudiobookStatus(mappingState)
		if mappingState == "normalized" {
			mappingStatus = RegionalAudiobookLoaded
		}
		if input.Unanchored {
			resolvedBookID = mapping.BookID
		} else {
			resolvedBookID = input.BookID
		}
		if err := validateRegionalImportMapping(mapping, resolvedBookID, externalID, mappingStatus, 0); err != nil {
			return nil, false, err
		}
		status = mappingStatus
		editionID = firstPositiveInt(pointerInt(mapping.EditionID), regionalEditionID(mapping.Edition))
		if editionID <= 0 {
			return nil, false, fmt.Errorf("%w: terminal mapping %d has no edition ID", ErrRegionalAudiobookIdentityConflict, mapping.ID)
		}
	}

	if len(mappings) == 1 {
		if err := validateRegionalImportMapping(mappings[0], resolvedBookID, externalID, status, editionID); err != nil {
			return nil, false, err
		}
	}
	verifiedBookID, verifiedFormatID, verified, err := c.verifyRegionalAudiobookEdition(ctx, resolvedBookID, editionID, "verify recovered regional Audible edition")
	if err != nil {
		return nil, false, err
	}

	return &RegionalAudiobookResult{
		Status: status, BookID: verifiedBookID, EditionID: editionID,
		ReadingFormatID: verifiedFormatID, RegionalExternalID: externalID, Edition: verified,
	}, true, nil
}

func normalizeRegionalAudiobookInput(input RegionalAudiobookInput) (string, string, error) {
	asin := strings.ToUpper(strings.TrimSpace(input.ASIN))
	region := strings.ToLower(strings.TrimSpace(input.Region))
	bookIDValid := input.BookID > 0
	if input.Unanchored {
		bookIDValid = input.BookID == 0
	}
	if !bookIDValid || len(asin) != 10 || !isASIN(asin) || !audnexregion.IsRegion(region) {
		return "", "", fmt.Errorf("%w: %s, ten-character ASIN, and supported region are required", ErrRegionalAudiobookInvalidInput, map[bool]string{true: "an unanchored request", false: "a positive book ID"}[input.Unanchored])
	}
	return asin, region, nil
}

func (c *Client) verifyRegionalAudiobookEdition(ctx context.Context, bookID, editionID int, fetchContext string) (int, int, *models.Edition, error) {
	verified, err := c.GetEditionUncached(ctx, strconv.Itoa(editionID))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("%s %d: %w", fetchContext, editionID, err)
	}
	if verified == nil {
		return 0, 0, nil, fmt.Errorf("%w: edition %d could not be read back", ErrRegionalAudiobookIdentityConflict, editionID)
	}
	verifiedEditionID, editionErr := strconv.Atoi(verified.ID)
	verifiedBookID, bookErr := strconv.Atoi(verified.BookID)
	verifiedFormatID, formatErr := strconv.Atoi(verified.ReadingFormatID)
	if editionErr != nil || bookErr != nil || verifiedEditionID != editionID || verifiedBookID != bookID {
		return 0, 0, nil, fmt.Errorf("%w: expected edition %d on book %d, got edition %s on book %s", ErrRegionalAudiobookIdentityConflict, editionID, bookID, verified.ID, verified.BookID)
	}
	if formatErr != nil || verifiedFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) {
		return 0, 0, nil, &RegionalAudiobookWrongFormatError{BookID: bookID, EditionID: editionID, ReadingFormatID: verified.ReadingFormatID}
	}
	return verifiedBookID, verifiedFormatID, verified, nil
}

// regionalReadingFormatDescription names Hardcover formats in import diagnostics.
func regionalReadingFormatDescription(formatID string) string {
	switch formatID {
	case "1":
		return "physical book (ID 1)"
	case "2":
		return "audiobook (ID 2)"
	case "3":
		return "both formats (ID 3)"
	case "4":
		return "ebook (ID 4)"
	case "":
		return "missing"
	default:
		return fmt.Sprintf("unknown (ID %s)", formatID)
	}
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

func (c *Client) pollRegionalAudiobookImport(ctx context.Context, bookID int, allowBookResolution bool, externalID, mutationStatus string, mutationEditionID *int, mutationEdition *regionalImportEdition) (RegionalAudiobookStatus, int, int, error) {
	// Live tests observed failed, loaded, and created statuses. An ordinary-token
	// created import for a previously unmapped regional Audible ID returned the
	// expected book and edition IDs, and a fresh catalogue read found its exact
	// mapping. This confirms behavior, not every response shape or nullable/error
	// field; keep validation fail-closed for unknown or inconsistent results.
	pollCtx, cancel := context.WithTimeout(ctx, regionalImportMaxWait)
	defer cancel()
	pollInterval := regionalImportInitialPollInterval
	for {
		statuses, mappings, err := c.queryRegionalAudiobookImport(pollCtx, externalID)
		if err != nil {
			if ctx.Err() == nil && errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
				return "", 0, 0, fmt.Errorf("%w: regional Audible import did not reach a terminal state", ErrRegionalAudiobookImportTimeout)
			}
			return "", 0, 0, fmt.Errorf("poll regional Audible import: %w", err)
		}
		if len(statuses) > 1 {
			return "", 0, 0, fmt.Errorf("%w: Audible identifier %s matched multiple import statuses", ErrRegionalAudiobookIdentityConflict, externalID)
		}
		if len(mappings) > 1 {
			return "", 0, 0, fmt.Errorf("%w: Audible identifier %s matched multiple book mappings", ErrRegionalAudiobookIdentityConflict, externalID)
		}
		if len(statuses) == 1 {
			importStatus := &statuses[0]
			if importStatus.PlatformID != audiblePlatformID || importStatus.ExternalID != externalID {
				return "", 0, 0, fmt.Errorf("%w: import status did not match requested Audible identifier", ErrRegionalAudiobookIdentityConflict)
			}
			state := strings.ToLower(strings.TrimSpace(importStatus.Status))
			switch state {
			case "failed":
				return "", 0, 0, fmt.Errorf("%w: %s", ErrRegionalAudiobookImportFailed, firstNonEmpty(importStatus.Error, "Hardcover marked regional import failed"))
			case "not_found":
				return "", 0, 0, fmt.Errorf("%w: %s", ErrRegionalAudiobookImportFailed, firstNonEmpty(importStatus.Error, "Hardcover found no result for the regional Audible identifier"))
			case "fetching":
				if importStatus.BookID != nil && *importStatus.BookID > 0 {
					if bookID > 0 && *importStatus.BookID != bookID {
						return "", 0, 0, fmt.Errorf("%w: import status returned book %d for requested book %d", ErrRegionalAudiobookIdentityConflict, *importStatus.BookID, bookID)
					}
					if allowBookResolution && bookID == 0 {
						bookID = *importStatus.BookID
					}
				}
				// Continue polling while Hardcover resolves its upstream source.
			case string(RegionalAudiobookLoaded), string(RegionalAudiobookCreated):
				if importStatus.BookID != nil && *importStatus.BookID > 0 {
					if bookID > 0 && *importStatus.BookID != bookID {
						return "", 0, 0, fmt.Errorf("%w: import status returned book %d for requested book %d", ErrRegionalAudiobookIdentityConflict, *importStatus.BookID, bookID)
					}
					if allowBookResolution && bookID == 0 {
						bookID = *importStatus.BookID
					}
				}
				if !allowBookResolution && (importStatus.BookID == nil || *importStatus.BookID <= 0) {
					return "", 0, 0, fmt.Errorf("%w: completed anchored import did not return a positive Hardcover book ID", ErrRegionalAudiobookIdentityConflict)
				}
				if importStatus.BookID != nil && *importStatus.BookID <= 0 {
					return "", 0, 0, fmt.Errorf("%w: completed import status returned nonpositive book ID %d", ErrRegionalAudiobookIdentityConflict, *importStatus.BookID)
				}
				if allowBookResolution && bookID == 0 && len(mappings) == 1 {
					if err := validateRegionalImportMapping(mappings[0], bookID, externalID, RegionalAudiobookStatus(state), 0); err != nil {
						return "", 0, 0, err
					}
					bookID = mappings[0].BookID
				}
				if bookID <= 0 {
					return "", 0, 0, fmt.Errorf("%w: completed import did not return a positive Hardcover book ID", ErrRegionalAudiobookIdentityConflict)
				}
				if importStatus.BookID != nil && *importStatus.BookID != bookID {
					return "", 0, 0, fmt.Errorf("%w: completed import returned book %d for requested book %d", ErrRegionalAudiobookIdentityConflict, *importStatus.BookID, bookID)
				}
				if importStatus.EditionID != nil && *importStatus.EditionID <= 0 {
					return "", 0, 0, fmt.Errorf("%w: completed import status returned nonpositive edition ID %d", ErrRegionalAudiobookIdentityConflict, *importStatus.EditionID)
				}
				editionID := pointerInt(importStatus.EditionID)
				if editionID <= 0 && allowBookResolution && len(mappings) == 1 {
					editionID = firstPositiveInt(pointerInt(mappings[0].EditionID), regionalEditionID(mappings[0].Edition))
				}
				if editionID <= 0 {
					return "", 0, 0, fmt.Errorf("%w: completed import did not return a positive edition ID", ErrRegionalAudiobookIdentityConflict)
				}
				if upsertStatus := strings.ToLower(strings.TrimSpace(mutationStatus)); upsertStatus == "failed" {
					return "", 0, 0, fmt.Errorf("%w: upsert response reported failed status", ErrRegionalAudiobookImportFailed)
				} else if (upsertStatus == string(RegionalAudiobookLoaded) || upsertStatus == string(RegionalAudiobookCreated)) && upsertStatus != state {
					return "", 0, 0, fmt.Errorf("%w: upsert reported %s while import status reported %s", ErrRegionalAudiobookIdentityConflict, upsertStatus, state)
				}
				if mutationID := firstPositiveInt(pointerInt(mutationEditionID), regionalEditionID(mutationEdition)); mutationID > 0 && mutationID != editionID {
					return "", 0, 0, fmt.Errorf("%w: upsert returned edition %d but import status returned edition %d", ErrRegionalAudiobookIdentityConflict, mutationID, editionID)
				}
				if err := validateRegionalImportEdition(mutationEdition, bookID); err != nil {
					return "", 0, 0, err
				}
				if len(mappings) == 1 {
					if err := validateRegionalImportMapping(mappings[0], bookID, externalID, RegionalAudiobookStatus(state), editionID); err != nil {
						return "", 0, 0, err
					}
				}
				return RegionalAudiobookStatus(state), bookID, editionID, nil
			default:
				return "", 0, 0, fmt.Errorf("%w: Hardcover returned unsupported regional import status %q", ErrRegionalAudiobookIdentityConflict, importStatus.Status)
			}
		}
		if len(mappings) == 1 {
			if err := validateRegionalImportMapping(mappings[0], bookID, externalID, "", 0); err != nil {
				return "", 0, 0, err
			}
			if allowBookResolution && bookID == 0 {
				bookID = mappings[0].BookID
			}
		}
		// Keep the first checks responsive, then reduce quota usage while
		// Hardcover imports its upstream data. The deadline still bounds the wait.
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", 0, 0, fmt.Errorf("regional audiobook import polling canceled: %w", ctx.Err())
		case <-pollCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return "", 0, 0, fmt.Errorf("regional audiobook import polling canceled: %w", ctx.Err())
			}
			return "", 0, 0, fmt.Errorf("%w: regional Audible import did not reach a terminal state", ErrRegionalAudiobookImportTimeout)
		case <-timer.C:
		}
		pollInterval = min(2*pollInterval, regionalImportMaxPollInterval)
	}
}

func (c *Client) queryRegionalAudiobookImport(ctx context.Context, externalID string) ([]regionalImportStatus, []regionalImportMapping, error) {
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
	var response struct {
		Statuses []regionalImportStatus  `json:"book_import_statuses"`
		Mappings []regionalImportMapping `json:"book_mappings"`
	}
	if err := c.GraphQLQuery(ctx, query, map[string]interface{}{
		"entries": []map[string]interface{}{{
			"platform_id": audiblePlatformID,
			"external_id": externalID,
		}},
		"platformId": audiblePlatformID,
		"externalId": externalID,
	}, &response); err != nil {
		return nil, nil, err
	}
	return response.Statuses, response.Mappings, nil
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
	bookMatches := mapping.BookID > 0 && (bookID == 0 || mapping.BookID == bookID)
	if !bookMatches || mapping.PlatformID != audiblePlatformID || mapping.ExternalID != externalID {
		return fmt.Errorf("%w: mapping %d did not match requested book and Audible identifier", ErrRegionalAudiobookIdentityConflict, mapping.ID)
	}
	if err := validateRegionalEditionLink(pointerInt(mapping.EditionID), mapping.Edition, "mapping"); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(mapping.State), "failed") {
		return fmt.Errorf("%w: Hardcover marked mapping %d failed without a terminal failed import status", ErrRegionalAudiobookIdentityConflict, mapping.ID)
	}
	mappingStatus := RegionalAudiobookStatus(strings.ToLower(strings.TrimSpace(mapping.State)))
	if status != "" && (mappingStatus == RegionalAudiobookLoaded || mappingStatus == RegionalAudiobookCreated) && mappingStatus != status {
		return fmt.Errorf("%w: mapping %d reported %s while import status reported %s", ErrRegionalAudiobookIdentityConflict, mapping.ID, mappingStatus, status)
	}
	mappingEditionID := firstPositiveInt(pointerInt(mapping.EditionID), regionalEditionID(mapping.Edition))
	if editionID > 0 && mappingEditionID > 0 && mappingEditionID != editionID {
		return fmt.Errorf("%w: mapping %d returned edition %d but import status returned edition %d", ErrRegionalAudiobookIdentityConflict, mapping.ID, mappingEditionID, editionID)
	}
	return validateRegionalImportEdition(mapping.Edition, mapping.BookID)
}

func validateRegionalImportEdition(edition *regionalImportEdition, bookID int) error {
	if edition == nil {
		return nil
	}
	if edition.ID < 0 || edition.BookID < 0 || (bookID > 0 && edition.BookID != 0 && edition.BookID != bookID) {
		return fmt.Errorf("%w: expected book %d, upsert returned edition book %d", ErrRegionalAudiobookIdentityConflict, bookID, edition.BookID)
	}
	// Reading format is checked only by the fresh edition read after a terminal
	// status, so a wrong-format result is reported with its verified edition.
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
