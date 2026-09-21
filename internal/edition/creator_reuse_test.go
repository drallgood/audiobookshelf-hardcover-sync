package edition_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// reuseClient is a Hardcover client whose lookups return canned editions and
// whose insert_edition reports a duplicate when insertErrors is set. It records
// every mutation. Embedding the interface leaves any other method nil, so an
// unexpected call fails loudly.
type reuseClient struct {
	edition.HardcoverClient

	byASIN, byISBN13, byISBN10 *models.Edition
	// afterInsert hides every lookup result until an insert_edition was
	// attempted, so an edition is found only by the lookups that follow a
	// duplicate error.
	afterInsert  bool
	insertErrors []string
	insertID     int // the ID insert_edition returns when it does not report errors

	mu        sync.Mutex
	mutations []string
	lookups   []string // "KIND:value", in call order
	// lookupFormats is the reading format each lookup's context carried ("" for none).
	lookupFormats []string
	// sent is the edition dto of each insert_edition attempt.
	sent []map[string]interface{}
}

func (c *reuseClient) noteFormat(ctx context.Context) {
	format, _ := models.ReadingFormatFromContext(ctx)
	c.lookupFormats = append(c.lookupFormats, format)
}

func (c *reuseClient) GetEditionByASIN(ctx context.Context, asin string) (*models.Edition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.noteFormat(ctx)
	c.lookups = append(c.lookups, "ASIN:"+asin)
	return c.visible(c.byASIN)
}

func (c *reuseClient) GetEditionByISBN10(ctx context.Context, isbn10 string) (*models.Edition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.noteFormat(ctx)
	c.lookups = append(c.lookups, "ISBN-10:"+isbn10)
	return c.visible(c.byISBN10)
}

func (c *reuseClient) GetEditionByISBN13(ctx context.Context, isbn13 string) (*models.Edition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.noteFormat(ctx)
	c.lookups = append(c.lookups, "ISBN-13:"+isbn13)
	return c.visible(c.byISBN13)
}

// visible returns found, or a not-found error while lookups are hidden. The
// caller holds c.mu.
func (c *reuseClient) visible(found *models.Edition) (*models.Edition, error) {
	if found == nil || (c.afterInsert && len(c.mutations) == 0) {
		return nil, errors.New("not found")
	}
	return found, nil
}

func (c *reuseClient) GraphQLMutation(_ context.Context, mutation string, variables map[string]interface{}, result interface{}) error {
	c.mu.Lock()
	c.mutations = append(c.mutations, mutation)
	if edition, ok := variables["edition"].(map[string]interface{}); ok {
		if dto, ok := edition["dto"].(map[string]interface{}); ok {
			c.sent = append(c.sent, dto)
		}
	}
	c.mu.Unlock()
	if !strings.Contains(mutation, "insert_edition") {
		return errors.New("unexpected mutation")
	}
	var id interface{}
	if c.insertID != 0 {
		id = c.insertID
	}
	raw, err := json.Marshal(map[string]interface{}{"insert_edition": map[string]interface{}{"id": id, "errors": c.insertErrors}})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in this test")
}

// TestCreateEdition_ReusesOnlyAnEditionOfTheSameBook covers every path that
// adopts an existing edition. The input carries a cover, so any image step
// would show up as an extra mutation, an image error, or a missing Existing.
func TestCreateEdition_ReusesOnlyAnEditionOfTheSameBook(t *testing.T) {
	const bookID = 123
	sameBook := &models.Edition{ID: "555", BookID: "123"}
	otherBook := &models.Edition{ID: "555", BookID: "999"}
	duplicate := []string{"already exists"}
	tests := []struct {
		name         string
		asin, isbn13 string
		client       *reuseClient
		wantExisting bool
		wantConflict bool
		wantInserts  int // insert_edition attempts; no other mutation may ever be sent
	}{
		{name: "ASIN match on the same book", asin: "B0EXISTING1", client: &reuseClient{byASIN: sameBook}, wantExisting: true},
		{name: "ASIN match on another book", asin: "B0EXISTING1", client: &reuseClient{byASIN: otherBook}, wantConflict: true},
		{name: "ASIN match without a book ID", asin: "B0EXISTING1", client: &reuseClient{byASIN: &models.Edition{ID: "555"}}, wantConflict: true},
		{name: "ASIN match with an unknown book", asin: "B0EXISTING1", client: &reuseClient{byASIN: &models.Edition{ID: "555", BookID: "0"}}, wantConflict: true},
		{
			name: "duplicate reported, ISBN-13 match on the same book", isbn13: "9781234567890",
			client: &reuseClient{insertErrors: duplicate, byISBN13: sameBook, afterInsert: true}, wantExisting: true, wantInserts: 1,
		},
		{
			name: "duplicate reported, ISBN-13 match on another book", isbn13: "9781234567890",
			client: &reuseClient{insertErrors: duplicate, byISBN13: otherBook, afterInsert: true}, wantConflict: true, wantInserts: 1,
		},
		{
			name: "duplicate reported, ASIN match on the same book", asin: "B0EXISTING1",
			client: &reuseClient{insertErrors: duplicate, byASIN: sameBook, afterInsert: true}, wantExisting: true, wantInserts: 1,
		},
		{
			name: "duplicate reported, ASIN match on another book", asin: "B0EXISTING1",
			client: &reuseClient{insertErrors: duplicate, byASIN: otherBook, afterInsert: true}, wantConflict: true, wantInserts: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &edition.EditionInput{
				BookID: bookID, Title: "A Title", ASIN: tt.asin, ISBN13: tt.isbn13, AuthorIDs: []int{1},
				ImageURL: "https://audiobookshelf.example.test/api/items/x/cover",
			}
			creator := edition.NewCreatorWithHTTPClient(tt.client, logger.Get(), false, "token", &http.Client{Transport: failingTransport{}})

			result, err := creator.CreateEdition(context.Background(), input)

			switch {
			case tt.wantConflict:
				if !errors.Is(err, edition.ErrEditionBelongsToOtherBook) {
					t.Fatalf("CreateEdition() error = %v, want ErrEditionBelongsToOtherBook", err)
				}
			case err != nil:
				t.Fatalf("CreateEdition() error = %v", err)
			default:
				if result.EditionID != 555 || result.Existing != tt.wantExisting || result.ImageError != "" || result.ImageID != 0 {
					t.Errorf("CreateEdition() = %+v, want the untouched existing edition 555", result)
				}
			}
			if got := len(tt.client.mutations); got != tt.wantInserts {
				t.Errorf("mutations sent = %d (%v), want only %d insert_edition", got, tt.client.mutations, tt.wantInserts)
			}
		})
	}
}

// TestCreateEdition_DetectsAnExistingEditionByEveryIdentifierBeforeInserting
// covers the proactive lookups: ASIN, ISBN-13, ISBN-10 and the converted ISBN
// forms are all checked before insert_edition is attempted.
func TestCreateEdition_DetectsAnExistingEditionByEveryIdentifierBeforeInserting(t *testing.T) {
	const bookID = 123
	sameBook := &models.Edition{ID: "555", BookID: "123"}
	otherBook := &models.Edition{ID: "555", BookID: "999"}
	tests := []struct {
		name         string
		isbn10       string
		isbn13       string
		client       *reuseClient
		wantConflict bool
		wantCreated  bool
	}{
		{name: "ISBN-13 match on the same book", isbn13: "978-0-306-40615-7", client: &reuseClient{byISBN13: sameBook}},
		{name: "ISBN-10 match on the same book", isbn10: "0-306-40615-2", client: &reuseClient{byISBN10: sameBook}},
		{name: "ISBN-13 found only through the converted ISBN-10", isbn10: "0306406152", client: &reuseClient{byISBN13: sameBook}},
		{name: "ISBN-10 found only through the converted ISBN-13", isbn13: "9780306406157", client: &reuseClient{byISBN10: sameBook}},
		{name: "ISBN-10 match on another book", isbn10: "0306406152", client: &reuseClient{byISBN10: otherBook}, wantConflict: true},
		{name: "ISBN-13 match on another book", isbn13: "9780306406157", client: &reuseClient{byISBN13: otherBook}, wantConflict: true},
		{name: "ISBN-13 match with an unknown book", isbn13: "9780306406157", client: &reuseClient{byISBN13: &models.Edition{ID: "555"}}, wantConflict: true},
		{name: "nothing found creates the edition", isbn13: "9780306406157", client: &reuseClient{insertID: 777}, wantCreated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &edition.EditionInput{
				BookID: bookID, Title: "A Title", ISBN10: tt.isbn10, ISBN13: tt.isbn13, AuthorIDs: []int{1},
				ImageURL: "https://audiobookshelf.example.test/api/items/x/cover",
			}
			creator := edition.NewCreatorWithHTTPClient(tt.client, logger.Get(), false, "token", &http.Client{Transport: failingTransport{}})

			result, err := creator.CreateEdition(context.Background(), input)

			wantInserts := 0
			switch {
			case tt.wantConflict:
				if !errors.Is(err, edition.ErrEditionBelongsToOtherBook) {
					t.Fatalf("CreateEdition() error = %v, want ErrEditionBelongsToOtherBook", err)
				}
			case err != nil:
				t.Fatalf("CreateEdition() error = %v", err)
			case tt.wantCreated:
				wantInserts = 1
				if result.EditionID != 777 || result.Existing {
					t.Errorf("CreateEdition() = %+v, want the newly created edition 777", result)
				}
			default:
				if result.EditionID != 555 || !result.Existing || result.ImageError != "" || result.ImageID != 0 {
					t.Errorf("CreateEdition() = %+v, want the untouched existing edition 555", result)
				}
			}
			if got := len(tt.client.mutations); got != wantInserts {
				t.Errorf("mutations sent = %d (%v), want %d insert_edition and nothing else", got, tt.client.mutations, wantInserts)
			}
		})
	}
}

func TestCreateEdition_LooksUpEachIdentifierOnceInOrder(t *testing.T) {
	client := &reuseClient{insertID: 777}
	creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "token", &http.Client{Transport: failingTransport{}})
	input := &edition.EditionInput{
		BookID: 123, Title: "A Title", AuthorIDs: []int{1},
		ASIN: "B0EXISTING1", ISBN13: "978-0-306-40615-7", ISBN10: "0306406152",
	}

	if _, err := creator.CreateEdition(context.Background(), input); err != nil {
		t.Fatalf("CreateEdition() error = %v", err)
	}

	// The ISBN-10 and ISBN-13 are each other's converted form, so neither is looked up twice.
	want := []string{"ASIN:B0EXISTING1", "ISBN-13:9780306406157", "ISBN-10:0306406152"}
	if strings.Join(client.lookups, ",") != strings.Join(want, ",") {
		t.Errorf("lookups = %v, want %v", client.lookups, want)
	}
}

func TestCreateEdition_DryRunLooksNothingUp(t *testing.T) {
	client := &reuseClient{byISBN13: &models.Edition{ID: "555", BookID: "123"}}
	creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), true, "token", &http.Client{Transport: failingTransport{}})

	result, err := creator.CreateEdition(context.Background(), &edition.EditionInput{
		BookID: 123, Title: "A Title", AuthorIDs: []int{1}, ASIN: "B0EXISTING1", ISBN13: "9780306406157",
	})

	if err != nil || result.EditionID != 0 || result.Existing {
		t.Fatalf("CreateEdition() = %+v, %v, want a dry-run result with edition 0", result, err)
	}
	if len(client.lookups) != 0 || len(client.mutations) != 0 {
		t.Errorf("dry run made lookups %v and mutations %v", client.lookups, client.mutations)
	}
}

// TestCreateEdition_EbookInputCreatesAnEbookEdition checks what an ebook edition
// sends to Hardcover and that duplicates are looked up among ebook editions only.
func TestCreateEdition_EbookInputCreatesAnEbookEdition(t *testing.T) {
	base := edition.EditionInput{
		BookID: 123, Title: "A Title", ASIN: "B0EBOOK001", ISBN13: "9780306406157", AuthorIDs: []int{1},
		NarratorIDs: []int{7}, AudioLength: 3600,
	}
	tests := []struct {
		name         string
		format       string
		wantFormat   string
		wantReading  int
		wantAudio    bool
		wantLookupIn string
	}{
		{"ebook", "ebook", "Ebook", 4, false, "ebook"},
		{"ebook is case-insensitive", " Ebook ", "Ebook", 4, false, "ebook"},
		{"audiobook", "audiobook", "Audiobook", 2, true, "audiobook"},
		{"unset stays an audiobook", "", "Audiobook", 2, true, "audiobook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &reuseClient{insertID: 777}
			input := base
			input.ReadingFormat = tt.format
			creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "token", &http.Client{Transport: failingTransport{}})

			result, err := creator.CreateEdition(context.Background(), &input)
			if err != nil {
				t.Fatalf("CreateEdition() error = %v", err)
			}
			if result.EditionID != 777 || result.Existing {
				t.Fatalf("CreateEdition() = %+v, want the new edition 777", result)
			}
			if len(client.sent) != 1 {
				t.Fatalf("insert_edition attempts = %d, want 1", len(client.sent))
			}
			dto := client.sent[0]
			if dto["edition_format"] != tt.wantFormat || dto["reading_format_id"] != tt.wantReading {
				t.Errorf("dto edition_format=%v reading_format_id=%v, want %q and %d", dto["edition_format"], dto["reading_format_id"], tt.wantFormat, tt.wantReading)
			}
			if _, hasAudio := dto["audio_seconds"]; hasAudio != tt.wantAudio {
				t.Errorf("audio_seconds sent = %v, want %v", hasAudio, tt.wantAudio)
			}
			_, hasContributions := dto["contributions"]
			hasNarrator := false
			if list, ok := dto["contributions"].([]map[string]interface{}); ok {
				for _, c := range list {
					if c["contribution"] == "Narrator" {
						hasNarrator = true
					}
				}
			}
			if hasNarrator != tt.wantAudio || !hasContributions {
				t.Errorf("narrator contribution sent = %v, want %v (authors are always sent)", hasNarrator, tt.wantAudio)
			}
			if len(client.lookupFormats) == 0 {
				t.Fatal("no duplicate lookup was made")
			}
			for _, got := range client.lookupFormats {
				if got != tt.wantLookupIn {
					t.Errorf("lookup reading format = %q, want %q", got, tt.wantLookupIn)
				}
			}
		})
	}
}

func TestEditionInputValidate_RejectsAnUnknownReadingFormat(t *testing.T) {
	input := edition.EditionInput{BookID: 1, Title: "T", AuthorIDs: []int{1}, ReadingFormat: "paperback"}
	if err := input.Validate(); err == nil {
		t.Fatal("Validate() accepted reading_format \"paperback\"")
	}
	for _, ok := range []string{"", "audiobook", "Ebook"} {
		input.ReadingFormat = ok
		if err := input.Validate(); err != nil {
			t.Errorf("Validate() rejected reading_format %q: %v", ok, err)
		}
	}
}

// TestCreateEdition_DuplicateErrorFallbackFindsAnEditionByISBN10 covers the race
// where the edition only appears after insert_edition reports a duplicate: the
// fallback searches every identifier, including the ISBN-10.
func TestCreateEdition_DuplicateErrorFallbackFindsAnEditionByISBN10(t *testing.T) {
	client := &reuseClient{
		insertErrors: []string{"already exists"},
		byISBN10:     &models.Edition{ID: "555", BookID: "123"},
		afterInsert:  true,
	}
	input := &edition.EditionInput{BookID: 123, Title: "A Title", ISBN10: "0306406152", AuthorIDs: []int{1}}
	creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "token", &http.Client{Transport: failingTransport{}})

	result, err := creator.CreateEdition(context.Background(), input)

	if err != nil {
		t.Fatalf("CreateEdition() error = %v", err)
	}
	if result.EditionID != 555 || !result.Existing {
		t.Errorf("CreateEdition() = %+v, want the existing edition 555", result)
	}
	if len(client.mutations) != 1 {
		t.Errorf("mutations sent = %d, want a single insert_edition attempt", len(client.mutations))
	}
}
