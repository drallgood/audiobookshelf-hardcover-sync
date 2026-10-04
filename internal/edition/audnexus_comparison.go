package edition

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// AudnexusRecord is the normalized, displayable Audnexus record used to
// compare an audiobook with its Audiobookshelf source.
type AudnexusRecord struct {
	ASIN                 string   `json:"asin"`
	Title                string   `json:"title"`
	Subtitle             string   `json:"subtitle,omitempty"`
	Authors              []string `json:"authors,omitempty"`
	Narrators            []string `json:"narrators,omitempty"`
	Series               []string `json:"series,omitempty"`
	SeriesPosition       string   `json:"series_position,omitempty"`
	Publisher            string   `json:"publisher,omitempty"`
	ReleaseDate          string   `json:"release_date,omitempty"`
	RuntimeSeconds       int      `json:"runtime_seconds,omitempty"`
	Language             string   `json:"language,omitempty"`
	CoverURL             string   `json:"cover_url,omitempty"`
	releaseDatePrecision datePrecision
}

type datePrecision uint8

const (
	datePrecisionUnknown datePrecision = iota
	datePrecisionYear
	datePrecisionMonth
	datePrecisionDay
)

// AudnexusFieldStatus describes one informational source comparison.
type AudnexusFieldStatus string

const (
	AudnexusMatch   AudnexusFieldStatus = "match"
	AudnexusDiffers AudnexusFieldStatus = "differs"
	AudnexusMissing AudnexusFieldStatus = "missing"

	audnexusRuntimeToleranceSeconds = 60
)

// AudnexusComparison contains an informational comparison for each field
// shown in the Audible import review.
type AudnexusComparison struct {
	Title          AudnexusFieldStatus `json:"title"`
	Subtitle       AudnexusFieldStatus `json:"subtitle"`
	Authors        AudnexusFieldStatus `json:"authors"`
	Narrators      AudnexusFieldStatus `json:"narrators"`
	Series         AudnexusFieldStatus `json:"series"`
	SeriesPosition AudnexusFieldStatus `json:"series_position"`
	Publisher      AudnexusFieldStatus `json:"publisher"`
	ReleaseDate    AudnexusFieldStatus `json:"release_date"`
	Runtime        AudnexusFieldStatus `json:"runtime"`
	Language       AudnexusFieldStatus `json:"language"`
	CoverURL       AudnexusFieldStatus `json:"cover_url,omitempty"`
}

// BuildAudnexusRecord projects the supported comparison fields from Audnexus.
func BuildAudnexusRecord(book *audnex.Book) AudnexusRecord {
	if book == nil {
		return AudnexusRecord{}
	}
	releaseDate, releaseDatePrecision, _ := normalizeComparisonDate(book.ReleaseDate)
	switch releaseDatePrecision {
	case datePrecisionYear:
		releaseDate = releaseDate[:4]
	case datePrecisionMonth:
		releaseDate = releaseDate[:7]
	}
	record := AudnexusRecord{
		ASIN: strings.TrimSpace(book.ASIN), Title: strings.TrimSpace(book.Title),
		Subtitle: strings.TrimSpace(book.Subtitle), Authors: cleanNames(book.GetAuthorsAsStrings()),
		Narrators: cleanNames(book.GetNarratorsAsStrings()), Publisher: strings.TrimSpace(book.PublisherName),
		ReleaseDate:          releaseDate,
		releaseDatePrecision: releaseDatePrecision,
		RuntimeSeconds:       int(math.Round(book.RuntimeLengthMin * 60)),
		Language:             strings.TrimSpace(book.Language), CoverURL: strings.TrimSpace(book.Image),
	}
	for _, series := range []*audnex.Series{book.SeriesPrimary, book.SeriesSecondary} {
		if series == nil {
			continue
		}
		name := strings.TrimSpace(series.Name)
		if name != "" {
			record.Series = append(record.Series, name)
		}
		if record.SeriesPosition == "" && name != "" {
			record.SeriesPosition = strings.TrimSpace(series.Position)
		}
	}
	return record
}

// CompareAudnexus compares a normalized Audnexus record with an ABS item.
// Differences are informational; callers decide whether to require user
// confirmation before using the regional identifier.
func CompareAudnexus(abs *models.AudiobookshelfBook, record AudnexusRecord) AudnexusComparison {
	if abs == nil {
		return AudnexusComparison{
			Title: AudnexusMissing, Subtitle: AudnexusMissing, Authors: AudnexusMissing,
			Narrators: AudnexusMissing, Series: AudnexusMissing, SeriesPosition: AudnexusMissing,
			Publisher: AudnexusMissing, ReleaseDate: AudnexusMissing, Runtime: AudnexusMissing,
			Language: AudnexusMissing,
		}
	}
	metadata := abs.Media.Metadata
	absSeries, absPosition := AudiobookshelfSeriesFields(metadata)
	absDate, absDatePrecision, dateOK := normalizeComparisonDate(metadata.PublishedDate)
	if !dateOK {
		absDate, absDatePrecision, dateOK = normalizeComparisonDate(metadata.PublishedYear)
	}
	audnexusDate, audnexusDatePrecision, audnexusDateOK := normalizeComparisonDate(record.ReleaseDate)
	if record.releaseDatePrecision != datePrecisionUnknown {
		audnexusDatePrecision = record.releaseDatePrecision
	}
	return AudnexusComparison{
		Title:          compareValues(metadata.Title, record.Title),
		Subtitle:       compareValues(metadata.Subtitle, record.Subtitle),
		Authors:        compareValues(metadata.AuthorName, strings.Join(record.Authors, ", ")),
		Narrators:      compareValues(metadata.NarratorName, strings.Join(record.Narrators, ", ")),
		Series:         compareValues(absSeries, strings.Join(record.Series, ", ")),
		SeriesPosition: compareValues(absPosition, record.SeriesPosition),
		Publisher:      compareValues(metadata.Publisher, record.Publisher),
		ReleaseDate:    compareDatePrecision(absDate, absDatePrecision, dateOK, audnexusDate, audnexusDatePrecision, audnexusDateOK),
		Runtime:        compareRuntime(abs.Media.Duration, record.RuntimeSeconds),
		Language:       compareValues(metadata.Language, record.Language),
	}
}

// NormalizeNameOrTitle applies the same trim-and-case normalization used by
// edition-create snapshot checks.
func NormalizeNameOrTitle(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func compareValues(absValue, audnexusValue string) AudnexusFieldStatus {
	absNormalized := NormalizeNameOrTitle(absValue)
	audnexusNormalized := NormalizeNameOrTitle(audnexusValue)
	if absNormalized == "" || audnexusNormalized == "" {
		return AudnexusMissing
	}
	if absNormalized == audnexusNormalized {
		return AudnexusMatch
	}
	return AudnexusDiffers
}

func compareRuntime(absSeconds float64, audnexusSeconds int) AudnexusFieldStatus {
	if absSeconds <= 0 || audnexusSeconds <= 0 {
		return AudnexusMissing
	}
	if math.Abs(math.Round(absSeconds)-float64(audnexusSeconds)) <= audnexusRuntimeToleranceSeconds {
		return AudnexusMatch
	}
	return AudnexusDiffers
}

// AudiobookshelfSeriesFields joins named series and their positions for display.
func AudiobookshelfSeriesFields(metadata models.AudiobookshelfMetadataStruct) (string, string) {
	names := make([]string, 0, len(metadata.Series)+1)
	positions := make([]string, 0, len(metadata.Series))
	for _, series := range metadata.Series {
		if name := strings.TrimSpace(series.Name); name != "" {
			names = append(names, name)
			if sequence := strings.TrimSpace(series.Sequence); sequence != "" {
				positions = append(positions, sequence)
			}
		}
	}
	if len(names) == 0 && strings.TrimSpace(metadata.SeriesName) != "" {
		names = append(names, strings.TrimSpace(metadata.SeriesName))
	}
	return strings.Join(names, ", "), strings.Join(positions, ", ")
}

func cleanNames(values []string) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		if name := strings.TrimSpace(value); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// NormalizeAudnexusDate normalizes supported year and date values to ISO 8601.
// Ambiguous slash dates and unrecognized values return false so callers can
// display them as missing and let the user review the source explicitly.
func NormalizeAudnexusDate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	if isAmbiguousComparisonSlashDate(value) {
		return "", false
	}
	if len(value) == 4 {
		if _, err := time.Parse("2006", value); err == nil {
			return value + "-01-01", true
		}
	}
	for _, layout := range []string{
		"2006-01-02", time.RFC3339, "2006-01-02T15:04:05.000Z07:00",
		"2006-01-02 15:04:05", "2006/01/02", "01/02/2006", "02/01/2006",
		"Jan 2, 2006", "January 2, 2006", "2 Jan 2006", "2 January 2006",
		"2006-01", "January 2006", "Jan 2006",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("2006-01-02"), true
		}
	}
	return "", false
}

func normalizeComparisonDate(raw string) (string, datePrecision, bool) {
	date, ok := NormalizeAudnexusDate(raw)
	if !ok {
		return "", datePrecisionUnknown, false
	}
	value := strings.TrimSpace(raw)
	if len(value) == 4 {
		return date, datePrecisionYear, true
	}
	for _, layout := range []string{"2006-01", "January 2006", "Jan 2006"} {
		if _, err := time.Parse(layout, value); err == nil {
			return date, datePrecisionMonth, true
		}
	}
	return date, datePrecisionDay, true
}

func compareDatePrecision(absDate string, absPrecision datePrecision, absOK bool, audnexusDate string, audnexusPrecision datePrecision, audnexusOK bool) AudnexusFieldStatus {
	if !absOK || !audnexusOK {
		return AudnexusMissing
	}
	precision := absPrecision
	if audnexusPrecision < precision {
		precision = audnexusPrecision
	}
	switch precision {
	case datePrecisionYear:
		if absDate[:4] == audnexusDate[:4] {
			return AudnexusMatch
		}
	case datePrecisionMonth:
		if absDate[:7] == audnexusDate[:7] {
			return AudnexusMatch
		}
	case datePrecisionDay:
		if absDate == audnexusDate {
			return AudnexusMatch
		}
	}
	return AudnexusDiffers
}

func isAmbiguousComparisonSlashDate(raw string) bool {
	value := strings.TrimSpace(raw)
	if !strings.Contains(value, "/") {
		return false
	}
	monthFirst, monthFirstErr := time.Parse("01/02/2006", value)
	dayFirst, dayFirstErr := time.Parse("02/01/2006", value)
	return monthFirstErr == nil && dayFirstErr == nil && !monthFirst.Equal(dayFirst)
}

// String returns the stable text representation used in human-readable CLI output.
func (r AudnexusRecord) String() string {
	return fmt.Sprintf("%s — %s", r.Title, strings.Join(r.Authors, ", "))
}
