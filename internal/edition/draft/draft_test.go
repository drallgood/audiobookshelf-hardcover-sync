package draft_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition/draft"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// fakeHardcover answers the read queries the draft pipeline issues. Embedding
// the interface leaves every other method nil, so an unexpected call fails
// loudly instead of silently succeeding.
//
// Person and publisher lookups are cached process-wide by the mismatch
// package, so each test uses names no other test uses.
type fakeHardcover struct {
	hardcover.HardcoverClientInterface

	authors    map[string]string // name -> Hardcover ID
	narrators  map[string]string
	publishers map[string]string
	// candidates is returned by SearchBooks, mimicking the title/author
	// enrichment that can guess a different Hardcover book.
	candidates []models.HardcoverBook
}

func (f *fakeHardcover) lookup(m map[string]string, name string) []models.Author {
	if id, ok := m[name]; ok {
		return []models.Author{{ID: id, Name: name}}
	}
	return nil
}

func (f *fakeHardcover) SearchAuthors(_ context.Context, name string, _ int) ([]models.Author, error) {
	return f.lookup(f.authors, name), nil
}

func (f *fakeHardcover) SearchNarrators(_ context.Context, name string, _ int) ([]models.Author, error) {
	return f.lookup(f.narrators, name), nil
}

func (f *fakeHardcover) SearchPublishers(_ context.Context, name string, _ int) ([]models.Publisher, error) {
	if id, ok := f.publishers[name]; ok {
		return []models.Publisher{{ID: id, Name: name}}, nil
	}
	return nil, nil
}

func (f *fakeHardcover) SearchBooks(context.Context, string, string) ([]models.HardcoverBook, error) {
	return f.candidates, nil
}

func (f *fakeHardcover) GetBookByID(_ context.Context, id string) (*models.HardcoverBook, error) {
	for i := range f.candidates {
		if f.candidates[i].ID == id {
			return &f.candidates[i], nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeHardcover) SearchBookByISBN13(context.Context, string) (*models.HardcoverBook, error) {
	return nil, nil
}

func (f *fakeHardcover) SearchBookByISBN10(context.Context, string) (*models.HardcoverBook, error) {
	return nil, nil
}

func (f *fakeHardcover) SearchBookByASIN(context.Context, string) (*models.HardcoverBook, error) {
	return nil, nil
}

// absItem builds an Audiobookshelf item. It never sets an ASIN, because the
// draft pipeline would then call the live Audnex API.
func absItem(mutate func(*models.AudiobookshelfBook)) models.AudiobookshelfBook {
	var b models.AudiobookshelfBook
	b.ID = "li_draft1"
	b.LibraryID = "lib_1"
	b.Media.Duration = 3600.4
	b.Media.CoverPath = "/metadata/items/li_draft1/cover.jpg"
	b.Media.Metadata.Title = "The Draft Title"
	b.Media.Metadata.Subtitle = "A Draft Subtitle"
	b.Media.Metadata.AuthorName = "Ada Draftwright"
	b.Media.Metadata.NarratorName = "Nora Voicer"
	b.Media.Metadata.Publisher = "Draftwright House"
	b.Media.Metadata.PublishedYear = "2020"
	b.Media.Metadata.ISBN = "9781234567897"
	if mutate != nil {
		mutate(&b)
	}
	return b
}

func TestNew_CarriesResolvedFields(t *testing.T) {
	hc := &fakeHardcover{
		authors:    map[string]string{"Ada Draftwright": "101"},
		narrators:  map[string]string{"Nora Voicer": "202"},
		publishers: map[string]string{"Draftwright House": "303"},
		// Enrichment finds a plausible-looking candidate on a different book.
		candidates: []models.HardcoverBook{{ID: "999", Title: "The Draft Title"}},
	}

	d, err := draft.New(context.Background(), absItem(nil), 42, "https://abs.example.com/", hc, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if d.HardcoverBookID != 42 {
		t.Errorf("HardcoverBookID = %d, want 42 (the argument, not the enrichment guess)", d.HardcoverBookID)
	}
	if !reflect.DeepEqual(d.AuthorIDs, []int{101}) || !reflect.DeepEqual(d.NarratorIDs, []int{202}) || d.PublisherID != 303 {
		t.Errorf("IDs = authors %v narrators %v publisher %d, want [101] [202] 303", d.AuthorIDs, d.NarratorIDs, d.PublisherID)
	}
	checks := map[string][2]string{
		"title":               {d.Title, "The Draft Title"},
		"subtitle":            {d.Subtitle, "A Draft Subtitle"},
		"isbn_13":             {d.ISBN13, "9781234567897"},
		"isbn_10":             {d.ISBN10, "123456789X"}, // derived from the ISBN-13
		"release_date":        {d.ReleaseDate, "2020-01-01"},
		"edition_information": {d.EditionInformation, "Unabridged"},
		"author_names":        {d.AuthorNames, "Ada Draftwright"},
		"narrator_names":      {d.NarratorNames, "Nora Voicer"},
		"publisher_name":      {d.PublisherName, "Draftwright House"},
		"cover_url":           {d.CoverURL, "https://abs.example.com/api/items/li_draft1/cover"},
	}
	for field, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", field, c[0], c[1])
		}
	}
	if d.AudioSeconds != 3600 {
		t.Errorf("AudioSeconds = %d, want 3600", d.AudioSeconds)
	}
	if d.LanguageID == 0 || d.CountryID == 0 {
		t.Errorf("language/country defaults missing: %d/%d", d.LanguageID, d.CountryID)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for a fully resolved draft", d.Warnings)
	}
}

func TestNew_Warnings(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*models.AudiobookshelfBook)
		hc      *fakeHardcover
		want    []string // substrings, one per expected warning, in order
		wantIDs bool     // authors resolved
	}{
		{
			name: "nothing resolves",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.AuthorName = "Unknown Author Q"
				b.Media.Metadata.NarratorName = "Unknown Narrator Q"
				b.Media.Metadata.Publisher = "Unknown Publisher Q"
				b.Media.Metadata.PublishedYear = ""
			},
			hc:   &fakeHardcover{},
			want: []string{"No Hardcover author matched", "No release date", "Publisher \"Unknown Publisher Q\" was not found", "No Hardcover narrator matched"},
		},
		{
			name: "item has no author or narrator or publisher",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.AuthorName = ""
				b.Media.Metadata.NarratorName = ""
				b.Media.Metadata.Publisher = ""
			},
			hc:   &fakeHardcover{},
			want: []string{"has no author", "lists no narrator"},
		},
		{
			name: "only the narrator is unresolved",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.AuthorName = "Known Author R"
				b.Media.Metadata.NarratorName = "Unknown Narrator R"
				b.Media.Metadata.Publisher = "Known Publisher R"
			},
			hc: &fakeHardcover{
				authors:    map[string]string{"Known Author R": "11"},
				publishers: map[string]string{"Known Publisher R": "33"},
			},
			want:    []string{"No Hardcover narrator matched"},
			wantIDs: true,
		},
		{
			name: "bad-checksum ISBN-13 warns but is still exported",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.ISBN = "9780306406158" // shape-valid, wrong check digit
			},
			hc: &fakeHardcover{
				authors:    map[string]string{"Ada Draftwright": "101"},
				narrators:  map[string]string{"Nora Voicer": "202"},
				publishers: map[string]string{"Draftwright House": "303"},
			},
			want:    []string{"ISBN-13 \"9780306406158\" has an incorrect check digit"},
			wantIDs: true,
		},
		{
			name: "non-English language warns",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.Language = "German"
			},
			hc: &fakeHardcover{
				authors:    map[string]string{"Ada Draftwright": "101"},
				narrators:  map[string]string{"Nora Voicer": "202"},
				publishers: map[string]string{"Draftwright House": "303"},
			},
			want:    []string{"tagged \"German\""},
			wantIDs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := draft.New(context.Background(), absItem(tt.mutate), 7, "https://abs.example.com", tt.hc, "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if len(d.Warnings) != len(tt.want) {
				t.Fatalf("Warnings = %q, want %d entries matching %q", d.Warnings, len(tt.want), tt.want)
			}
			for i, sub := range tt.want {
				if !strings.Contains(d.Warnings[i], sub) {
					t.Errorf("Warnings[%d] = %q, want it to contain %q", i, d.Warnings[i], sub)
				}
			}
			if got := len(d.AuthorIDs) > 0; got != tt.wantIDs {
				t.Errorf("author resolved = %v, want %v", got, tt.wantIDs)
			}
			// An unresolved publisher must stay unset rather than defaulting.
			if tt.name == "nothing resolves" && d.PublisherID != 0 {
				t.Errorf("PublisherID = %d, want 0 when unresolved", d.PublisherID)
			}
		})
	}
}

func TestNew_CoverURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		mutate  func(*models.AudiobookshelfBook)
		want    string
	}{
		{"forced to the base URL", "https://abs.example.com", nil, "https://abs.example.com/api/items/li_draft1/cover"},
		{"trailing slash trimmed", "https://abs.example.com/", nil, "https://abs.example.com/api/items/li_draft1/cover"},
		{"credentials and query stripped", "https://user:pw@abs.example.com/?token=x#frag", nil, "https://abs.example.com/api/items/li_draft1/cover"},
		{"unparseable base URL leaves it empty", "not a url", nil, ""},
		{"path prefix preserved", "https://example.com/abs/", nil, "https://example.com/abs/api/items/li_draft1/cover"},
		{"no cover path leaves it empty", "https://abs.example.com", func(b *models.AudiobookshelfBook) { b.Media.CoverPath = "" }, ""},
		{"no base URL leaves it empty", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A Hardcover candidate with a cover must never leak into the draft.
			hc := &fakeHardcover{
				authors:    map[string]string{"Ada Draftwright": "101"},
				candidates: []models.HardcoverBook{{ID: "5", Title: "The Draft Title", CoverImageURL: "https://assets.hardcover.app/cover.jpg"}},
			}
			d, err := draft.New(context.Background(), absItem(tt.mutate), 42, tt.baseURL, hc, "us")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if d.CoverURL != tt.want {
				t.Errorf("CoverURL = %q, want %q", d.CoverURL, tt.want)
			}
		})
	}
}

func TestNew_ISBNForms(t *testing.T) {
	tests := []struct {
		name   string
		isbn   string
		want10 string
		want13 string
	}{
		{name: "hyphenated ISBN-13 fills its ISBN-10", isbn: "978-0-306-40615-7", want10: "0306406152", want13: "9780306406157"},
		{name: "hyphenated ISBN-10 fills its ISBN-13", isbn: "0-306-40615-2", want10: "0306406152", want13: "9780306406157"},
		{name: "979 ISBN-13 has no ISBN-10", isbn: "979-10-90636-07-1", want13: "9791090636071"},
		{name: "invalid checksum keeps only the given form", isbn: "9780306406158", want13: "9780306406158"},
		{name: "no ISBN", isbn: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) { b.Media.Metadata.ISBN = tt.isbn }), 9, "https://abs.example.com", &fakeHardcover{}, "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if d.ISBN10 != tt.want10 || d.ISBN13 != tt.want13 {
				t.Errorf("ISBN10/ISBN13 = %q/%q, want %q/%q", d.ISBN10, d.ISBN13, tt.want10, tt.want13)
			}
		})
	}
}

func TestNew_RejectsInvalidArguments(t *testing.T) {
	hc := &fakeHardcover{}
	tests := []struct {
		name   string
		item   models.AudiobookshelfBook
		bookID int
		hc     hardcover.HardcoverClientInterface
	}{
		{"nil client", absItem(nil), 1, nil},
		{"no hardcover book", absItem(nil), 0, hc},
		{"no item id", absItem(func(b *models.AudiobookshelfBook) { b.ID = "" }), 1, hc},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := draft.New(context.Background(), tt.item, tt.bookID, "https://abs.example.com", tt.hc, ""); err == nil {
				t.Fatal("New() error = nil, want an error")
			}
		})
	}
}

func TestDraft_JSONContract(t *testing.T) {
	// A draft with nothing resolved must still serialize lists as arrays.
	d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Metadata.AuthorName = "Nobody Matches S"
		b.Media.Metadata.NarratorName = ""
		b.Media.Metadata.Publisher = ""
	}), 9, "https://abs.example.com", &fakeHardcover{}, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{
		"asin", "audio_seconds", "author_ids", "author_names", "country_id", "cover_url", "dry_run",
		"edition_format", "edition_information", "hardcover_book_id", "isbn_10", "isbn_10_valid", "isbn_13",
		"isbn_13_valid", "language_id", "narrator_ids", "narrator_names", "publisher_id", "publisher_name",
		"reading_format", "release_date", "subtitle", "title", "warnings",
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("JSON keys = %v, want %v", keys, want)
	}
	for _, k := range []string{"author_ids", "narrator_ids", "warnings"} {
		if strings.TrimSpace(string(got[k])) == "null" {
			t.Errorf("%s serialized as null, want an array", k)
		}
	}
}

// TestNew_EbookDraft checks that an ebook-only item drafts an ebook edition: it
// is labeled as one and carries no narrators, audio length or narrator warning.
func TestNew_EbookDraft(t *testing.T) {
	hc := &fakeHardcover{
		authors:   map[string]string{"Ada Draftwright": "101"},
		narrators: map[string]string{"Nora Voicer": "202"},
	}
	ebook := absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Duration = 0
		b.Media.EbookFormat = "epub"
	})

	d, err := draft.New(context.Background(), ebook, 42, "https://abs.example.com/", hc, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if d.ReadingFormat != "ebook" || d.EditionFormat != "Ebook" {
		t.Errorf("reading format %q, edition format %q, want ebook and Ebook", d.ReadingFormat, d.EditionFormat)
	}
	if len(d.NarratorIDs) != 0 || d.NarratorNames != "" || d.AudioSeconds != 0 {
		t.Errorf("narrators %v (%q), audio seconds %d, want none", d.NarratorIDs, d.NarratorNames, d.AudioSeconds)
	}
	if d.EditionInformation != "" {
		t.Errorf("edition information = %q, want none for an ebook", d.EditionInformation)
	}
	for _, w := range d.Warnings {
		if strings.Contains(w, "narrator") {
			t.Errorf("an ebook draft warned about a narrator: %q", w)
		}
	}
}

// TestNew_AudiobookDraftReportsItsReadingFormat guards the audiobook default.
func TestNew_AudiobookDraftReportsItsReadingFormat(t *testing.T) {
	hc := &fakeHardcover{authors: map[string]string{"Ada Draftwright": "101"}}
	d, err := draft.New(context.Background(), absItem(nil), 42, "https://abs.example.com/", hc, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if d.ReadingFormat != "audiobook" {
		t.Errorf("ReadingFormat = %q, want audiobook", d.ReadingFormat)
	}
}

// TestNew_CarriesAbridgedFlag guards crosswalk row R14: an audiobook item
// Audiobookshelf marks abridged must draft as "Abridged", not the default
// "Unabridged" (the draft previously never forwarded the abridged flag).
func TestNew_CarriesAbridgedFlag(t *testing.T) {
	hc := &fakeHardcover{authors: map[string]string{"Ada Draftwright": "101"}}
	tests := map[bool]string{true: "Abridged", false: "Unabridged"}
	for abridged, want := range tests {
		d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) {
			b.Media.Metadata.Abridged = abridged
		}), 42, "https://abs.example.com/", hc, "us")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if d.EditionInformation != want {
			t.Errorf("abridged=%v: EditionInformation = %q, want %q", abridged, d.EditionInformation, want)
		}
	}
}

// TestNew_PrefersPublishedDateOverYear guards crosswalk finding 4: a full
// publishedDate, when Audiobookshelf provides one, is used before the
// year-only fallback.
func TestNew_PrefersPublishedDateOverYear(t *testing.T) {
	hc := &fakeHardcover{authors: map[string]string{"Ada Draftwright": "101"}}
	d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Metadata.PublishedYear = "2020"
		b.Media.Metadata.PublishedDate = "2020-06-15"
	}), 42, "https://abs.example.com/", hc, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if d.ReleaseDate != "2020-06-15" {
		t.Errorf("ReleaseDate = %q, want the full publishedDate 2020-06-15 over the year-only fallback", d.ReleaseDate)
	}
}
