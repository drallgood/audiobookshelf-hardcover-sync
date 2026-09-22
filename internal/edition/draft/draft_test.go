package draft_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition/draft"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// absItem builds an Audiobookshelf item without an ASIN, avoiding external
// Audnex calls in these mapping tests.
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

func TestNew_CarriesMappedFields(t *testing.T) {
	d, err := draft.New(context.Background(), absItem(nil), 42, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if d.HardcoverBookID != 42 {
		t.Errorf("HardcoverBookID = %d, want 42 from the run record", d.HardcoverBookID)
	}
	checks := map[string][2]string{
		"title":               {d.Title, "The Draft Title"},
		"subtitle":            {d.Subtitle, "A Draft Subtitle"},
		"isbn_13":             {d.ISBN13, "9781234567897"},
		"isbn_10":             {d.ISBN10, "123456789X"},
		"release_date":        {d.ReleaseDate, "2020-01-01"},
		"edition_information": {d.EditionInformation, "Unabridged"},
		"author_names":        {d.AuthorNames, "Ada Draftwright"},
		"narrator_names":      {d.NarratorNames, "Nora Voicer"},
		"publisher_name":      {d.PublisherName, "Draftwright House"},
	}
	for field, values := range checks {
		if values[0] != values[1] {
			t.Errorf("%s = %q, want %q", field, values[0], values[1])
		}
	}
	if d.AudioSeconds != 3600 {
		t.Errorf("AudioSeconds = %d, want 3600", d.AudioSeconds)
	}
	if d.LanguageID != 1 || d.CountryID != 1 {
		t.Errorf("language/country defaults = %d/%d, want 1/1", d.LanguageID, d.CountryID)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for complete source metadata", d.Warnings)
	}
}

func TestNew_UsesExactExpandedPeopleNames(t *testing.T) {
	item := absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Metadata.AuthorName = "Joined fallback author"
		b.Media.Metadata.NarratorName = "Joined fallback narrator"
		b.Media.Metadata.Authors = []models.AudiobookshelfPerson{{Name: "Mara Author, Jr."}, {Name: "Avery Exact Author"}}
		b.Media.Metadata.Narrators = []string{"Rae Reader, PhD", "Sky Exact Narrator"}
	})

	d, err := draft.New(context.Background(), item, 42, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if d.AuthorNames != "Mara Author, Jr., Avery Exact Author" || d.NarratorNames != "Rae Reader, PhD, Sky Exact Narrator" {
		t.Errorf("names = authors %q narrators %q, want trimmed expanded ABS names joined for display", d.AuthorNames, d.NarratorNames)
	}
}

func TestNewTrimsASINForAudnexAndPreview(t *testing.T) {
	previousTransport := http.DefaultTransport
	var gotPath string
	http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath = request.URL.Path
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"asin":"B0TRIMMED1","releaseDate":"2024-04-05"}`)),
			Request:    request,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	item := absItem(func(book *models.AudiobookshelfBook) {
		book.Media.Metadata.ASIN = " \tB0TRIMMED1\n"
	})
	d, err := draft.New(context.Background(), item, 42, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if gotPath != "/books/B0TRIMMED1" {
		t.Errorf("Audnex request path = %q, want trimmed ASIN", gotPath)
	}
	if d.ASIN != "B0TRIMMED1" {
		t.Errorf("draft ASIN = %q, want trimmed ASIN", d.ASIN)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNew_FallsBackToLegacyJoinedPeopleNames(t *testing.T) {
	item := absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Metadata.Authors = nil
		b.Media.Metadata.AuthorName = "Legacy First Author, Legacy Second Author"
		b.Media.Metadata.Narrators = nil
		b.Media.Metadata.NarratorName = "Legacy First Narrator, Legacy Second Narrator"
	})

	d, err := draft.New(context.Background(), item, 42, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if d.AuthorNames != "Legacy First Author, Legacy Second Author" || d.NarratorNames != "Legacy First Narrator, Legacy Second Narrator" {
		t.Errorf("legacy names = authors %q narrators %q, want the joined ABS fallback strings", d.AuthorNames, d.NarratorNames)
	}
}

func TestNew_WarningsAreBasedOnSourceMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*models.AudiobookshelfBook)
		want   []string
	}{
		{
			name: "missing author narrator and date",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.AuthorName = ""
				b.Media.Metadata.NarratorName = ""
				b.Media.Metadata.PublishedYear = ""
			},
			want: []string{"has no author", "No release date", "lists no narrator"},
		},
		{
			name: "checksum-invalid ISBN remains draftable",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.ISBN = "9780306406158"
			},
			want: []string{"ISBN-13 \"9780306406158\" has an incorrect check digit"},
		},
		{
			name: "non-English language warns",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.Language = "German"
			},
			want: []string{"tagged \"German\""},
		},
		{
			name: "mixed English label warns",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.Language = "English/French"
			},
			want: []string{"tagged \"English/French\""},
		},
		{
			name: "English label does not warn",
			mutate: func(b *models.AudiobookshelfBook) {
				b.Media.Metadata.Language = "English (US)"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := draft.New(context.Background(), absItem(tt.mutate), 7, "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if len(d.Warnings) != len(tt.want) {
				t.Fatalf("Warnings = %q, want entries matching %q", d.Warnings, tt.want)
			}
			for i, sub := range tt.want {
				if !strings.Contains(d.Warnings[i], sub) {
					t.Errorf("Warnings[%d] = %q, want it to contain %q", i, d.Warnings[i], sub)
				}
			}
		})
	}
}

func TestNewDoesNotReportHardcoverMatchWarnings(t *testing.T) {
	d, err := draft.New(context.Background(), absItem(nil), 7, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("Warnings = %v, want no match warnings when source names are present", d.Warnings)
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
			d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) { b.Media.Metadata.ISBN = tt.isbn }), 9, "")
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
	tests := []struct {
		name   string
		item   models.AudiobookshelfBook
		bookID int
	}{
		{"no Hardcover book ID", absItem(nil), 0},
		{"no Audiobookshelf item ID", absItem(func(b *models.AudiobookshelfBook) { b.ID = "" }), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := draft.New(context.Background(), tt.item, tt.bookID, ""); err == nil {
				t.Fatal("New() error = nil, want an error")
			}
		})
	}
}

func TestDraft_JSONContract(t *testing.T) {
	d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Metadata.NarratorName = ""
		b.Media.Metadata.Publisher = ""
	}), 9, "")
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
	for key := range got {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{
		"asin", "audio_seconds", "author_names", "country_id", "dry_run", "edition_format",
		"edition_information", "hardcover_book_id", "isbn_10", "isbn_10_valid", "isbn_13",
		"isbn_13_valid", "language_id", "narrator_names", "publisher_name", "reading_format",
		"release_date", "subtitle", "title", "warnings",
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("JSON keys = %v, want %v", keys, want)
	}
	for _, key := range []string{"author_ids", "narrator_ids", "publisher_id"} {
		if _, exists := got[key]; exists {
			t.Errorf("draft serialized %q; previews must not contain Hardcover-resolved IDs", key)
		}
	}
	if strings.TrimSpace(string(got["warnings"])) == "null" {
		t.Error("warnings serialized as null, want an array")
	}
}

func TestNew_EbookDraft(t *testing.T) {
	ebook := absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Duration = 0
		b.Media.EbookFormat = "epub"
	})
	d, err := draft.New(context.Background(), ebook, 42, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if d.ReadingFormat != "ebook" || d.EditionFormat != "Ebook" {
		t.Errorf("reading format %q, edition format %q, want ebook and Ebook", d.ReadingFormat, d.EditionFormat)
	}
	if d.NarratorNames != "" || d.AudioSeconds != 0 {
		t.Errorf("narrator names %q, audio seconds %d, want none", d.NarratorNames, d.AudioSeconds)
	}
	if d.EditionInformation != "" {
		t.Errorf("edition information = %q, want none for an ebook", d.EditionInformation)
	}
	for _, warning := range d.Warnings {
		if strings.Contains(warning, "narrator") {
			t.Errorf("an ebook draft warned about a narrator: %q", warning)
		}
	}
}

func TestNew_AudiobookDraftReportsItsReadingFormat(t *testing.T) {
	d, err := draft.New(context.Background(), absItem(nil), 42, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if d.ReadingFormat != "audiobook" {
		t.Errorf("ReadingFormat = %q, want audiobook", d.ReadingFormat)
	}
}

func TestNew_CarriesAbridgedFlag(t *testing.T) {
	tests := map[bool]string{true: "Abridged", false: "Unabridged"}
	for abridged, want := range tests {
		d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) {
			b.Media.Metadata.Abridged = abridged
		}), 42, "us")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if d.EditionInformation != want {
			t.Errorf("abridged=%v: EditionInformation = %q, want %q", abridged, d.EditionInformation, want)
		}
	}
}

func TestNew_PrefersPublishedDateOverYear(t *testing.T) {
	d, err := draft.New(context.Background(), absItem(func(b *models.AudiobookshelfBook) {
		b.Media.Metadata.PublishedYear = "2020"
		b.Media.Metadata.PublishedDate = "2020-06-15"
	}), 42, "us")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if d.ReleaseDate != "2020-06-15" {
		t.Errorf("ReleaseDate = %q, want 2020-06-15 over the year-only fallback", d.ReleaseDate)
	}
}
