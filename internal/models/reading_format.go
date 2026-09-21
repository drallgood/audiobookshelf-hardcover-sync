package models

import (
	"context"
	"strings"
)

// Reading formats an Audiobookshelf item, and the Hardcover edition matching it,
// can have.
const (
	ReadingFormatAudiobook = "audiobook"
	ReadingFormatEbook     = "ebook"
)

// Hardcover reading_format ids for the two reading formats.
const (
	readingFormatIDAudiobook = 2
	readingFormatIDEbook     = 4
)

// ReadingFormat returns the reading format the item's Hardcover edition must
// have: ebook for an ebook-only item, otherwise audiobook.
func (b *AudiobookshelfBook) ReadingFormat() string {
	if b.IsEbook() {
		return ReadingFormatEbook
	}
	return ReadingFormatAudiobook
}

// ReadingFormatID maps a reading format to Hardcover's reading_format id,
// defaulting to audiobook for an empty or unknown format.
func ReadingFormatID(format string) int {
	if strings.EqualFold(strings.TrimSpace(format), ReadingFormatEbook) {
		return readingFormatIDEbook
	}
	return readingFormatIDAudiobook
}

type readingFormatKey struct{}

// WithReadingFormat returns a context that carries the desired reading format
// ("audiobook" or "ebook", case-insensitive). Hardcover lookups made with it
// only consider editions of that format; without one they default to audiobook.
func WithReadingFormat(ctx context.Context, format string) context.Context {
	return context.WithValue(ctx, readingFormatKey{}, strings.ToLower(strings.TrimSpace(format)))
}

// ReadingFormatFromContext returns the reading format carried by ctx, if any.
func ReadingFormatFromContext(ctx context.Context) (string, bool) {
	if s, ok := ctx.Value(readingFormatKey{}).(string); ok && s != "" {
		return s, true
	}
	return "", false
}
