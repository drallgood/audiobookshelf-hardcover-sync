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

func TestFindBookInHardcoverByTitleAuthor(t *testing.T) {
	searchErr := errors.New("search API error")
	tests := []struct {
		name          string
		title         string
		author        string
		query         string
		searchResults []*TestHardcoverBook
		searchErr     error
		wantErr       error
		wantCause     error
		wantCandidate bool
	}{
		{
			name:  "candidate from title and author needs review",
			title: "Test Book", author: "Test Author", query: "Test Book Test Author",
			searchResults: []*TestHardcoverBook{{ID: "hc-book-1", Title: "Test Book"}},
			wantErr:       errHardcoverTitleOnly, wantCandidate: true,
		},
		{
			name:  "candidate search without author uses title only",
			title: "No Author Book", query: "No Author Book",
			searchResults: []*TestHardcoverBook{{ID: "hc-book-2", Title: "No Author Book"}},
			wantErr:       errHardcoverTitleOnly, wantCandidate: true,
		},
		{
			name:  "search API failure preserves its cause",
			title: "Error Book", author: "Error Author", query: "Error Book Error Author",
			searchErr: searchErr, wantErr: errHardcoverLookupFailed, wantCause: searchErr,
		},
		{
			name:  "no search results is not a candidate",
			title: "Missing Book", author: "Missing Author", query: "Missing Book Missing Author",
			searchResults: []*TestHardcoverBook{}, wantErr: errHardcoverBookNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			mockClient.On("SearchBooks", mock.Anything, tt.query, "").Return(tt.searchResults, tt.searchErr).Once()
			if len(tt.searchResults) > 0 {
				first := tt.searchResults[0]
				mockClient.On("GetBookByID", mock.Anything, first.ID).
					Return(&models.HardcoverBook{ID: first.ID, Title: first.Title}, nil).Once()
			}

			book := toAudiobookshelfBook(createTestBook("title-author", tt.title, tt.author, "", ""))
			got, err := svc.findBookInHardcoverByTitleAuthor(context.Background(), *book)

			require.ErrorIs(t, err, tt.wantErr)
			if tt.wantCause != nil {
				require.ErrorIs(t, err, tt.wantCause)
			}
			if tt.wantCandidate {
				require.NotNil(t, got)
				assert.Equal(t, tt.searchResults[0].ID, got.ID)
				assert.Equal(t, tt.searchResults[0].Title, got.Title)
			} else {
				assert.Nil(t, got)
			}
			mockClient.AssertExpectations(t)
		})
	}
}
