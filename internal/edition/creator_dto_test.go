package edition_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// sentDTO creates an edition for input and returns the edition dto that was
// sent to Hardcover's insert_edition mutation.
func sentDTO(t *testing.T, input *edition.EditionInput) map[string]interface{} {
	t.Helper()
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	client := &reuseClient{insertID: 789}
	creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "",
		&http.Client{Transport: failingTransport{}})

	result, err := creator.CreateEdition(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 789, result.EditionID)
	require.Len(t, client.sent, 1, "exactly one insert_edition must be sent")
	return client.sent[0]
}

func TestCreateEdition_DTOMinimalInputSendsOnlyRequiredKeys(t *testing.T) {
	dto := sentDTO(t, &edition.EditionInput{BookID: 123, Title: "Only Title", AuthorIDs: []int{7}})

	assert.Equal(t, map[string]interface{}{
		"title":             "Only Title",
		"edition_format":    "Audiobook",
		"reading_format_id": 2,
		"contributions": []map[string]interface{}{
			{"author_id": 7, "contribution": nil},
		},
	}, dto)
	for _, key := range []string{"subtitle", "asin", "isbn_10", "isbn_13", "publisher_id", "language_id",
		"country_id", "audio_seconds", "release_date", "edition_information", "image_id"} {
		assert.NotContains(t, dto, key)
	}
}

func TestCreateEdition_DTOOptionalStringFields(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		set   func(in *edition.EditionInput, v string)
		value string
	}{
		{"subtitle", "subtitle", func(in *edition.EditionInput, v string) { in.Subtitle = v }, "A Subtitle"},
		{"asin", "asin", func(in *edition.EditionInput, v string) { in.ASIN = v }, "B0EXAMPLE1"},
		{"isbn_10", "isbn_10", func(in *edition.EditionInput, v string) { in.ISBN10 = v }, "0306406152"},
		{"isbn_13", "isbn_13", func(in *edition.EditionInput, v string) { in.ISBN13 = v }, "9780306406157"},
		{"edition_information", "edition_information", func(in *edition.EditionInput, v string) { in.EditionInfo = v }, "Unabridged"},
		{"release_date", "release_date", func(in *edition.EditionInput, v string) { in.ReleaseDate = v }, "2024-03-05"},
	}
	for _, tt := range tests {
		t.Run(tt.name+" omitted when empty", func(t *testing.T) {
			in := &edition.EditionInput{BookID: 123, Title: "T", AuthorIDs: []int{1}}
			tt.set(in, "")
			assert.NotContains(t, sentDTO(t, in), tt.key)
		})
		t.Run(tt.name+" sent when set", func(t *testing.T) {
			in := &edition.EditionInput{BookID: 123, Title: "T", AuthorIDs: []int{1}}
			tt.set(in, tt.value)
			assert.Equal(t, tt.value, sentDTO(t, in)[tt.key])
		})
	}
}

func TestCreateEdition_DTOTitleIsSent(t *testing.T) {
	dto := sentDTO(t, &edition.EditionInput{BookID: 123, Title: "The Title", AuthorIDs: []int{1}})
	assert.Equal(t, "The Title", dto["title"])
}

func TestCreateEdition_DTOContributionsListAuthorsThenNarrators(t *testing.T) {
	tests := []struct {
		name      string
		authors   []int
		narrators []int
		want      []map[string]interface{}
	}{
		{
			name:      "authors first then narrators",
			authors:   []int{1, 2},
			narrators: []int{8, 9},
			want: []map[string]interface{}{
				{"author_id": 1, "contribution": nil},
				{"author_id": 2, "contribution": nil},
				{"author_id": 8, "contribution": "Narrator"},
				{"author_id": 9, "contribution": "Narrator"},
			},
		},
		{
			name:    "no narrators leaves only authors",
			authors: []int{1, 2},
			want: []map[string]interface{}{
				{"author_id": 1, "contribution": nil},
				{"author_id": 2, "contribution": nil},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dto := sentDTO(t, &edition.EditionInput{BookID: 123, Title: "T", AuthorIDs: tt.authors, NarratorIDs: tt.narrators})
			assert.Equal(t, tt.want, dto["contributions"])
		})
	}
}

func TestCreateEdition_DTOPositiveOnlyIDsAndAudioSeconds(t *testing.T) {
	tests := []struct {
		name string
		key  string
		set  func(in *edition.EditionInput, v int)
	}{
		{"publisher_id", "publisher_id", func(in *edition.EditionInput, v int) { in.PublisherID = v }},
		{"language_id", "language_id", func(in *edition.EditionInput, v int) { in.LanguageID = v }},
		{"country_id", "country_id", func(in *edition.EditionInput, v int) { in.CountryID = v }},
		{"audio_seconds", "audio_seconds", func(in *edition.EditionInput, v int) { in.AudioLength = v }},
	}
	for _, tt := range tests {
		for _, v := range []int{0, -3} {
			t.Run(tt.name+" omitted when not positive", func(t *testing.T) {
				in := &edition.EditionInput{BookID: 123, Title: "T", AuthorIDs: []int{1}}
				tt.set(in, v)
				assert.NotContains(t, sentDTO(t, in), tt.key)
			})
		}
		t.Run(tt.name+" sent when positive", func(t *testing.T) {
			in := &edition.EditionInput{BookID: 123, Title: "T", AuthorIDs: []int{1}}
			tt.set(in, 42)
			assert.Equal(t, 42, sentDTO(t, in)[tt.key])
		})
	}
}

func TestEditionInput_ValidateReleaseDate(t *testing.T) {
	tests := []struct {
		date    string
		wantErr bool
	}{
		{"", false},
		{"2024-03-05", false},
		{"2024/03/05", true},
		{"03-05-2024", true},
		{"2024-3-5", true},
		{"2024-13-01", true},
		{"20240305", true},
	}
	for _, tt := range tests {
		t.Run("date "+tt.date, func(t *testing.T) {
			err := (&edition.EditionInput{BookID: 123, Title: "T", AuthorIDs: []int{1}, ReleaseDate: tt.date}).Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
