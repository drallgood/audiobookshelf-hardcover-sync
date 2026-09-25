package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// 978-0-306-40615-7 and 0-306-40615-2 are the same book.
	testISBN13 = "9780306406157"
	testISBN10 = "0306406152"
	// A 979 ISBN-13 has no ISBN-10 form.
	testISBN13NoTen = "9791032305690"
)

// isbnSearchBook builds an Audiobookshelf item that has only an ISBN (no ASIN,
// no title or author), so a miss ends in not-found rather than a title search.
func isbnSearchBook(isbn string) models.AudiobookshelfBook {
	return *toAudiobookshelfBook(createTestBook("isbn-match", "", "", "", isbn))
}

func hardcoverHit() *models.HardcoverBook {
	return &models.HardcoverBook{ID: "901", EditionID: "902"}
}

func expectUserBook(hc *MockHardcoverClient) {
	hc.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901"}, nil).Maybe()
	hc.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Maybe()
}

func TestFindBookInHardcoverSearchesBothISBNForms(t *testing.T) {
	miss := (*models.HardcoverBook)(nil)

	tests := []struct {
		name   string
		abs    string
		format string
		// search13 and search10 are the values expected in each field, in the
		// order the fields are tried; empty means that field is not searched.
		search13 string
		search10 string
		// hit names the field whose search finds the edition ("" for none).
		hit         string
		wantFound   bool
		wantFirst13 bool
	}{
		{
			name: "ISBN-10 finds an edition stored only as ISBN-13",
			abs:  "0-306-40615-2", search10: testISBN10, search13: testISBN13,
			hit: "13", wantFound: true,
		},
		{
			name: "hyphenated ISBN-13 finds an edition stored only as ISBN-10",
			abs:  "978-0-306-40615-7", search13: testISBN13, search10: testISBN10,
			hit: "10", wantFound: true, wantFirst13: true,
		},
		{
			name: "ebook finds an ebook edition stored under the ISBN-10 counterpart",
			abs:  testISBN13, format: models.ReadingFormatEbook,
			search13: testISBN13, search10: testISBN10,
			hit: "10", wantFound: true, wantFirst13: true,
		},
		{
			name: "lowercase x ISBN-10 is normalized and converted",
			abs:  "0-8044-2957-x", search10: "080442957X", search13: "9780804429573",
			hit: "13", wantFound: true,
		},
		{
			name: "ISBN-10 hit on the given form stops before the counterpart",
			abs:  testISBN10, search10: testISBN10,
			hit: "10", wantFound: true,
		},
		{
			name: "ISBN-13 hit on the given form stops before the counterpart",
			abs:  testISBN13, search13: testISBN13,
			hit: "13", wantFound: true, wantFirst13: true,
		},
		{
			name: "979 ISBN-13 is searched only as ISBN-13",
			abs:  testISBN13NoTen, search13: testISBN13NoTen,
			wantFirst13: true,
		},
		{
			name: "ISBN-13 with an invalid checksum is searched as given only",
			abs:  "9781234567890", search13: "9781234567890",
			wantFirst13: true,
		},
		{
			name: "unparseable value keeps searching the normalized string in both fields",
			abs:  "not-an-isbn", search13: "notanisbn", search10: "notanisbn",
			wantFirst13: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = false
			expectUserBook(hc)
			book := isbnSearchBook(tt.abs)
			ctx := context.Background()
			if tt.format == models.ReadingFormatEbook {
				book.MediaType = "ebook"
				ctx = models.WithReadingFormat(ctx, book.ReadingFormat())
			}
			searchContext := mock.MatchedBy(func(ctx context.Context) bool {
				format, _ := models.ReadingFormatFromContext(ctx)
				return format == tt.format
			})

			var order []string
			result := func(field string) *models.HardcoverBook {
				if tt.hit == field {
					return hardcoverHit()
				}
				return miss
			}
			if tt.search13 != "" {
				hc.On("SearchBookByISBN13", searchContext, tt.search13).Return(result("13"), nil).
					Run(func(mock.Arguments) { order = append(order, "13") }).Once()
			}
			if tt.search10 != "" {
				hc.On("SearchBookByISBN10", searchContext, tt.search10).Return(result("10"), nil).
					Run(func(mock.Arguments) { order = append(order, "10") }).Once()
			}

			got, err := svc.findBookInHardcover(ctx, book)

			if tt.wantFound {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "901", got.ID)
			} else {
				require.ErrorIs(t, err, errHardcoverBookNotFound)
			}
			hc.AssertExpectations(t)
			if tt.search13 == "" {
				hc.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
			}
			if tt.search10 == "" {
				hc.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
			}
			if len(order) > 0 {
				assert.Equal(t, tt.wantFirst13, order[0] == "13", "the form the item has is tried first")
			}
			// The chain stops at the first hit.
			if tt.hit != "" {
				assert.Equal(t, tt.hit, order[len(order)-1])
			}
		})
	}
}

func TestFindBookInHardcoverISBNSearchErrors(t *testing.T) {
	searchErr := errors.New("temporary API failure")
	miss := (*models.HardcoverBook)(nil)

	t.Run("failure with no later hit is a lookup failure", func(t *testing.T) {
		svc, hc := createTestService()
		hc.On("SearchBookByISBN10", mock.Anything, testISBN10).Return(miss, searchErr).Once()
		hc.On("SearchBookByISBN13", mock.Anything, testISBN13).Return(miss, nil).Once()

		got, err := svc.findBookInHardcover(context.Background(), isbnSearchBook(testISBN10))

		assert.Nil(t, got)
		require.ErrorIs(t, err, errHardcoverLookupFailed)
		assert.ErrorIs(t, err, searchErr)
		hc.AssertExpectations(t)
	})

	t.Run("a hit on the counterpart wins over an earlier failure", func(t *testing.T) {
		svc, hc := createTestService()
		svc.config.Sync.SyncOwned = false
		expectUserBook(hc)
		hc.On("SearchBookByISBN13", mock.Anything, testISBN13).Return(miss, searchErr).Once()
		hc.On("SearchBookByISBN10", mock.Anything, testISBN10).Return(hardcoverHit(), nil).Once()

		got, err := svc.findBookInHardcover(context.Background(), isbnSearchBook(testISBN13))

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "901", got.ID)
		hc.AssertExpectations(t)
	})
}

func TestFindBookInHardcoverIgnoresBlankIdentifiers(t *testing.T) {
	svc, hc := createTestService()

	book := *toAudiobookshelfBook(createTestBook("blank-isbn", "", "", "", " - "))
	_, err := svc.findBookInHardcover(context.Background(), book)

	require.ErrorIs(t, err, errHardcoverBookNotFound)
	assertNoHardcoverBookSearches(t, hc)
}
