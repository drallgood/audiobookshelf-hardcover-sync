package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAudiobookshelfBookIsEbook(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{"expanded audiobook", `{"mediaType":"book","media":{"duration":3600,"numTracks":1,"tracks":[{}],"ebookFile":null}}`, false},
		{"expanded ebook", `{"mediaType":"book","media":{"duration":0,"numTracks":0,"tracks":[],"ebookFile":{"ebookFormat":"epub"}}}`, true},
		{"minified audiobook", `{"mediaType":"book","media":{"numTracks":2,"ebookFormat":null}}`, false},
		{"minified ebook", `{"mediaType":"book","media":{"numTracks":0,"ebookFormat":"epub"}}`, true},
		{"audiobook with companion ebook", `{"mediaType":"book","media":{"duration":3600,"numTracks":1,"ebookFile":{"ebookFormat":"epub"}}}`, false},
		{"only excluded audio files plus ebook", `{"mediaType":"book","media":{"duration":0,"numTracks":0,"audioFiles":[{"exclude":true}],"tracks":[],"ebookFile":{"ebookFormat":"epub"}}}`, true},
		{"tracks reported as a count still decodes", `{"mediaType":"book","media":{"numTracks":3,"tracks":3,"ebookFormat":null}}`, false},
		{"empty ebookFile object counts as an ebook, like ABS", `{"mediaType":"book","media":{"numTracks":0,"ebookFile":{}}}`, true},
		{"no media details", `{"mediaType":"book","media":{}}`, false},
		{"legacy ebook media type", `{"mediaType":"ebook","media":{}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var book AudiobookshelfBook
			require.NoError(t, json.Unmarshal([]byte(tt.json), &book))
			require.Equal(t, tt.want, book.IsEbook())
		})
	}
}

// TestAudiobookshelfBookIsEbookWithoutJSONDecoding verifies the decision uses
// only the exported media fields, so items built in code behave like decoded ones.
func TestAudiobookshelfBookIsEbookWithoutJSONDecoding(t *testing.T) {
	ebookFile := json.RawMessage(`{}`)

	var ebook AudiobookshelfBook
	ebook.MediaType = "book"
	ebook.Media.EbookFile = &ebookFile
	require.True(t, ebook.IsEbook())

	var epubFormat AudiobookshelfBook
	epubFormat.MediaType = "book"
	epubFormat.Media.EbookFormat = "epub"
	require.True(t, epubFormat.IsEbook())

	withAudio := epubFormat
	withAudio.Media.NumTracks = 1
	require.False(t, withAudio.IsEbook())

	withDuration := epubFormat
	withDuration.Media.Duration = 3600
	require.False(t, withDuration.IsEbook())
}

func TestAudiobookshelfMetadataDecodesAbridged(t *testing.T) {
	tests := map[string]struct {
		json string
		want bool
	}{
		"abridged":                    {`{"media":{"metadata":{"abridged":true}}}`, true},
		"not abridged":                {`{"media":{"metadata":{"abridged":false}}}`, false},
		"absent, as in older servers": {`{"media":{"metadata":{}}}`, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var book AudiobookshelfBook
			require.NoError(t, json.Unmarshal([]byte(tt.json), &book))
			require.Equal(t, tt.want, book.Media.Metadata.Abridged)
		})
	}
}
