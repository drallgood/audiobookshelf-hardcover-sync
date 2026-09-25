package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

type asinLookupBook struct {
	ID           interface{}         `json:"id"`
	Title        string              `json:"title"`
	BookStatusID int                 `json:"book_status_id"`
	CanonicalID  interface{}         `json:"canonical_id"`
	Editions     []asinLookupEdition `json:"editions"`
}

type asinLookupEdition struct {
	ID              interface{}         `json:"id"`
	ASIN            *string             `json:"asin"`
	ISBN13          *string             `json:"isbn_13"`
	ISBN10          *string             `json:"isbn_10"`
	ReadingFormatID *int                `json:"reading_format_id"`
	BookMappings    []asinLookupMapping `json:"book_mappings"`
}

type asinLookupMapping struct {
	ExternalID string `json:"external_id"`
	Platform   struct {
		Name string `json:"name"`
	} `json:"platform"`
}

type asinLookupCandidate struct {
	bookID   string
	book     asinLookupBook
	edition  asinLookupBookEdition
	identity string
}

// asinLookupBookEdition is kept separate so candidate values remain simple to
// compare while the raw response types above stay local to this API package.
type asinLookupBookEdition struct {
	id           string
	asin         string
	isbn13       string
	isbn10       string
	bookMappings []asinLookupMapping
}

// SearchBookByASINResult finds an edition by exact ASIN criteria and reports
// whether the result came from editions.asin or a regional Audible mapping.
// Audiobook Audible mappings take precedence over editions.asin fallbacks.
func (c *Client) SearchBookByASINResult(ctx context.Context, asin string) (*ASINLookupResult, error) {
	if asin == "" {
		return nil, fmt.Errorf("ASIN cannot be empty")
	}
	if c.logger == nil {
		c.logger = logger.Get()
	}

	formatID := readingFormatIDFromCtx(ctx)
	query, variables := asinLookupQuery(asin, formatID)
	var response struct {
		Books json.RawMessage `json:"books"`
	}
	if err := c.GraphQLQuery(ctx, query, variables, &response); err != nil {
		return nil, fmt.Errorf("failed to search book by ASIN: %w", err)
	}
	if len(response.Books) == 0 || string(response.Books) == "null" {
		return nil, fmt.Errorf("invalid ASIN response: books field is missing")
	}
	var books []asinLookupBook
	if err := json.Unmarshal(response.Books, &books); err != nil {
		return nil, fmt.Errorf("invalid ASIN response: books field: %w", err)
	}

	var mappingCandidates, fallbackCandidates []asinLookupCandidate
	for bookIndex, book := range books {
		bookID := asinScalarID(book.ID)
		if bookID == "" || book.Title == "" {
			return nil, fmt.Errorf("invalid ASIN response: book %d is missing an ID or title", bookIndex)
		}
		if len(book.Editions) == 0 {
			return nil, fmt.Errorf("invalid ASIN response: book %s has no editions", bookID)
		}
		for _, rawEdition := range book.Editions {
			editionID := asinScalarID(rawEdition.ID)
			if editionID == "" {
				return nil, fmt.Errorf("invalid ASIN response: book %s has an edition without an ID", bookID)
			}
			if rawEdition.ReadingFormatID == nil || *rawEdition.ReadingFormatID != formatID {
				continue
			}

			edition := asinLookupBookEdition{
				id:     editionID,
				isbn13: asinOptionalString(rawEdition.ISBN13), isbn10: asinOptionalString(rawEdition.ISBN10),
				bookMappings: rawEdition.BookMappings,
			}
			if rawEdition.ASIN != nil {
				edition.asin = *rawEdition.ASIN
			}
			candidate := asinLookupCandidate{
				bookID: bookID, book: book, edition: edition,
				identity: bookID + "/" + editionID,
			}
			if formatID == models.ReadingFormatID("audiobook") && hasExactAudibleMapping(asin, edition.bookMappings) {
				mappingCandidates = append(mappingCandidates, candidate)
			}
			if edition.asin == asin {
				fallbackCandidates = append(fallbackCandidates, candidate)
			}
		}
	}

	selected := fallbackCandidates
	matchKind := ASINMatchEditionASIN
	if formatID == models.ReadingFormatID("audiobook") && len(mappingCandidates) > 0 {
		selected = mappingCandidates
		matchKind = ASINMatchAudibleMapping
	}
	candidate, err := uniqueASINLookupCandidate(asin, selected)
	if err != nil || candidate == nil {
		return nil, err
	}
	regionalExternalID := ""
	if matchKind == ASINMatchAudibleMapping {
		regionalExternalID = exactAudibleMappingID(asin, candidate.edition.bookMappings)
	}
	return &ASINLookupResult{
		Book:               asinLookupHardcoverBook(candidate),
		MatchKind:          matchKind,
		RegionalExternalID: regionalExternalID,
	}, nil
}

func asinLookupQuery(asin string, formatID int) (string, map[string]interface{}) {
	variables := map[string]interface{}{"asin": asin, "format_id": formatID}
	editionOR := "{asin: {_eq: $asin}}"
	if formatID == models.ReadingFormatID("audiobook") {
		regions := audnexregion.Regions()
		for _, region := range regions {
			variable := "asin_" + region
			variables[variable] = asin + ":" + region
		}
		mappingPredicates := strings.Join(asinLookupMappingPredicates(), ", ")
		editionOR = fmt.Sprintf(`{_or: [{asin: {_eq: $asin}}, {book_mappings: {_or: [%s]}}]}`, mappingPredicates)
	}
	query := fmt.Sprintf(`
query BookByASIN($asin: String!, $format_id: Int!%s) {
  books(where: {editions: {_and: [{reading_format_id: {_eq: $format_id}}, %s]}}) {
    id
    title
    book_status_id
    canonical_id
    editions(where: {_and: [{reading_format_id: {_eq: $format_id}}, %s]}) {
      id
      asin
      isbn_13
      isbn_10
      reading_format_id
      %s
    }
  }
}`, asinLookupVariableDeclarations(formatID), editionOR, editionOR, asinLookupMappingSelection(formatID))
	return query, variables
}

func asinLookupVariableDeclarations(formatID int) string {
	if formatID != models.ReadingFormatID("audiobook") {
		return ""
	}
	regions := audnexregion.Regions()
	declarations := make([]string, 0, len(regions))
	for _, region := range regions {
		declarations = append(declarations, "$asin_"+region+": String!")
	}
	return ", " + strings.Join(declarations, ", ")
}

func asinLookupMappingSelection(formatID int) string {
	if formatID != models.ReadingFormatID("audiobook") {
		return ""
	}
	return `book_mappings(where: {_or: [` + strings.Join(asinLookupMappingPredicates(), ", ") + `]}) {
        external_id
        platform { name }
      }`
}

func asinLookupMappingPredicates() []string {
	regions := audnexregion.Regions()
	predicates := make([]string, 0, len(regions))
	for _, region := range regions {
		predicates = append(predicates, fmt.Sprintf(
			`{external_id: {_eq: $asin_%s}, platform: {name: {_eq: "Audible"}}}`,
			region,
		))
	}
	return predicates
}

func uniqueASINLookupCandidate(asin string, candidates []asinLookupCandidate) (*asinLookupCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	first := candidates[0]
	identities := map[string]struct{}{first.identity: {}}
	for _, candidate := range candidates[1:] {
		identities[candidate.identity] = struct{}{}
	}
	if len(identities) > 1 {
		conflicts := make([]string, 0, len(identities))
		for identity := range identities {
			conflicts = append(conflicts, identity)
		}
		sort.Strings(conflicts)
		return nil, fmt.Errorf("%w: ASIN %q matched %s", ErrASINLookupConflict, asin, strings.Join(conflicts, ", "))
	}
	return &first, nil
}

func hasExactAudibleMapping(asin string, mappings []asinLookupMapping) bool {
	return exactAudibleMappingID(asin, mappings) != ""
}

func exactAudibleMappingID(asin string, mappings []asinLookupMapping) string {
	for _, region := range audnexregion.Regions() {
		for _, mapping := range mappings {
			if !strings.EqualFold(mapping.Platform.Name, "Audible") {
				continue
			}
			if mapping.ExternalID == asin+":"+region {
				return mapping.ExternalID
			}
		}
	}
	return ""
}

func asinLookupHardcoverBook(candidate *asinLookupCandidate) *models.HardcoverBook {
	book := &models.HardcoverBook{
		ID: candidate.bookID, Title: candidate.book.Title,
		BookStatusID:  candidate.book.BookStatusID,
		EditionID:     candidate.edition.id,
		EditionASIN:   candidate.edition.asin,
		EditionISBN13: candidate.edition.isbn13,
		EditionISBN10: candidate.edition.isbn10,
	}
	if canonicalID := asinScalarID(candidate.book.CanonicalID); canonicalID != "" {
		if id, err := strconv.Atoi(canonicalID); err == nil {
			book.CanonicalID = &id
		}
	}
	if book.EditionASIN == "" {
		for _, mapping := range candidate.edition.bookMappings {
			if !strings.EqualFold(mapping.Platform.Name, "Audible") && !strings.EqualFold(mapping.Platform.Name, "Amazon") {
				continue
			}
			if idx := strings.LastIndex(mapping.ExternalID, ":"); idx > 0 {
				book.EditionASIN = mapping.ExternalID[:idx]
				break
			}
		}
	}
	return book
}

func asinScalarID(value interface{}) string {
	switch v := value.(type) {
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return v
	default:
		return ""
	}
}

func asinOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
