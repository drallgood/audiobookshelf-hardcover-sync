package models

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadingFormatID(t *testing.T) {
	tests := map[string]int{
		"ebook": 4, "Ebook": 4, " EBOOK ": 4,
		"audiobook": 2, "": 2, "paperback": 2,
	}
	for format, want := range tests {
		require.Equal(t, want, ReadingFormatID(format), "format %q", format)
	}
}

func TestAudiobookshelfBookReadingFormat(t *testing.T) {
	var audiobook AudiobookshelfBook
	audiobook.Media.Duration = 3600
	audiobook.Media.EbookFormat = "epub" // audio wins over a supplementary ebook
	require.Equal(t, ReadingFormatAudiobook, audiobook.ReadingFormat())

	var ebook AudiobookshelfBook
	ebook.Media.EbookFormat = "epub"
	require.Equal(t, ReadingFormatEbook, ebook.ReadingFormat())
}

func TestReadingFormatContext(t *testing.T) {
	_, ok := ReadingFormatFromContext(context.Background())
	require.False(t, ok, "a context without a format reports none")

	got, ok := ReadingFormatFromContext(WithReadingFormat(context.Background(), " Ebook "))
	require.True(t, ok)
	require.Equal(t, ReadingFormatEbook, got)
}
