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
