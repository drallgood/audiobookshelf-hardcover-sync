package edition

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

func TestCompareAudnexusRetainsCoverSourcesWithoutComparingThem(t *testing.T) {
	const (
		absCoverPath = "/metadata/items/item-1/cover.jpg"
		audnexusURL  = "https://images.example/cover.jpg"
	)
	var abs models.AudiobookshelfBook
	abs.Media.CoverPath = absCoverPath

	record := BuildAudnexusRecord(&audnex.Book{Image: audnexusURL})
	if record.CoverURL != audnexusURL {
		t.Fatalf("Audnexus source cover was not retained: got %q, want %q", record.CoverURL, audnexusURL)
	}
	if abs.Media.CoverPath != absCoverPath {
		t.Fatalf("Audiobookshelf source cover path was not retained: got %q, want %q", abs.Media.CoverPath, absCoverPath)
	}

	comparison := CompareAudnexus(&abs, record)
	if comparison.CoverURL != "" {
		t.Fatalf("cover comparison should be omitted because the source values use different representations, got %q", comparison.CoverURL)
	}

	serialized, err := json.Marshal(comparison)
	if err != nil {
		t.Fatalf("marshal comparison: %v", err)
	}
	if strings.Contains(string(serialized), `"cover_url"`) {
		t.Fatalf("comparison JSON should omit the uncomparable cover status: %s", serialized)
	}
}

func TestCompareAudnexusSeriesProjection(t *testing.T) {
	tests := []struct {
		name               string
		series             []models.AudiobookshelfSeries
		seriesName         string
		recordSeries       []string
		recordPosition     string
		wantSeries         AudnexusFieldStatus
		wantSeriesPosition AudnexusFieldStatus
	}{
		{
			name: "named structured series retain order and joined positions",
			series: []models.AudiobookshelfSeries{
				{Name: " Series A ", Sequence: " 1 "},
				{Name: "  ", Sequence: "ignored"},
				{Name: "Series B", Sequence: "2"},
			},
			recordSeries:       []string{"Series A", "Series B"},
			recordPosition:     "1, 2",
			wantSeries:         AudnexusMatch,
			wantSeriesPosition: AudnexusMatch,
		},
		{
			name: "unnamed structured entries are ignored",
			series: []models.AudiobookshelfSeries{
				{Name: " ", Sequence: "8"},
				{Name: "", Sequence: "9"},
			},
			wantSeries:         AudnexusMissing,
			wantSeriesPosition: AudnexusMissing,
		},
		{
			name:               "legacy series name is used when structured names are absent",
			series:             []models.AudiobookshelfSeries{{Name: "", Sequence: "8"}},
			seriesName:         " Legacy Series ",
			recordSeries:       []string{"Legacy Series"},
			wantSeries:         AudnexusMatch,
			wantSeriesPosition: AudnexusMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var abs models.AudiobookshelfBook
			abs.Media.Metadata.Series = tt.series
			abs.Media.Metadata.SeriesName = tt.seriesName
			got := CompareAudnexus(&abs, AudnexusRecord{
				Series: tt.recordSeries, SeriesPosition: tt.recordPosition,
			})
			if got.Series != tt.wantSeries {
				t.Errorf("series comparison = %q, want %q", got.Series, tt.wantSeries)
			}
			if got.SeriesPosition != tt.wantSeriesPosition {
				t.Errorf("series position comparison = %q, want %q", got.SeriesPosition, tt.wantSeriesPosition)
			}
		})
	}
}

func TestCompareAudnexusAllowsRuntimeDifferencesUpToOneMinute(t *testing.T) {
	tests := []struct {
		name            string
		absSeconds      float64
		audnexusMinutes float64
		wantRecord      int
		want            AudnexusFieldStatus
	}{
		{
			name:            "same ASIN chapter duration differs by 53 seconds",
			absSeconds:      58253,
			audnexusMinutes: 970,
			wantRecord:      58200,
			want:            AudnexusMatch,
		},
		{
			name:            "difference at tolerance boundary",
			absSeconds:      58260,
			audnexusMinutes: 970,
			wantRecord:      58200,
			want:            AudnexusMatch,
		},
		{
			name:            "difference beyond tolerance boundary",
			absSeconds:      58261,
			audnexusMinutes: 970,
			wantRecord:      58200,
			want:            AudnexusDiffers,
		},
		{
			name:            "Audiobookshelf runtime missing",
			audnexusMinutes: 970,
			wantRecord:      58200,
			want:            AudnexusMissing,
		},
		{
			name:       "Audnexus runtime missing",
			absSeconds: 58253,
			want:       AudnexusMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var abs models.AudiobookshelfBook
			abs.Media.Duration = tt.absSeconds
			record := BuildAudnexusRecord(&audnex.Book{RuntimeLengthMin: tt.audnexusMinutes})
			if record.RuntimeSeconds != tt.wantRecord {
				t.Fatalf("Audnexus source runtime = %d seconds, want %d", record.RuntimeSeconds, tt.wantRecord)
			}

			got := CompareAudnexus(&abs, record).Runtime
			if got != tt.want {
				t.Fatalf("runtime comparison = %q, want %q", got, tt.want)
			}
			if abs.Media.Duration != tt.absSeconds {
				t.Fatalf("Audiobookshelf source runtime changed during comparison: got %v, want %v", abs.Media.Duration, tt.absSeconds)
			}
		})
	}
}

func TestCompareAudnexusUsesAvailableDatePrecision(t *testing.T) {
	tests := []struct {
		name                  string
		absPublishedDate      string
		absPublishedYear      string
		audnexusReleaseDate   string
		want                  AudnexusFieldStatus
		wantNormalizedRelease string
	}{
		{
			name:                  "year-only Audnexus date does not invent a month or day",
			absPublishedDate:      "2014-08-05",
			audnexusReleaseDate:   "2014",
			want:                  AudnexusMatch,
			wantNormalizedRelease: "2014",
		},
		{
			name:                  "month-only Audnexus date does not invent a day",
			absPublishedDate:      "2014-08-05",
			audnexusReleaseDate:   "August 2014",
			want:                  AudnexusMatch,
			wantNormalizedRelease: "2014-08",
		},
		{
			name:                  "year-only Audiobookshelf date matches a full Audnexus date in that year",
			absPublishedYear:      "2014",
			audnexusReleaseDate:   "2014-08-05T00:00:00.000Z",
			want:                  AudnexusMatch,
			wantNormalizedRelease: "2014-08-05",
		},
		{
			name:                  "same calendar date matches across date and timestamp formats",
			absPublishedDate:      "August 5, 2014",
			audnexusReleaseDate:   "2014-08-05T00:00:00.000Z",
			want:                  AudnexusMatch,
			wantNormalizedRelease: "2014-08-05",
		},
		{
			name:                "different full dates in the same year remain different",
			absPublishedDate:    "2014-08-04",
			audnexusReleaseDate: "2014-08-05T00:00:00.000Z",
			want:                AudnexusDiffers,
		},
		{
			name:                "different years remain different",
			absPublishedYear:    "2015",
			audnexusReleaseDate: "2014-08-05",
			want:                AudnexusDiffers,
		},
		{
			name:                  "month-only Audiobookshelf date matches a full date in that month",
			absPublishedDate:      "August 2014",
			audnexusReleaseDate:   "2014-08-05",
			want:                  AudnexusMatch,
			wantNormalizedRelease: "2014-08-05",
		},
		{
			name:                "month-only Audiobookshelf date differs from another month",
			absPublishedDate:    "September 2014",
			audnexusReleaseDate: "2014-08-05",
			want:                AudnexusDiffers,
		},
		{
			name:                "ambiguous date without year remains missing",
			absPublishedDate:    "03/04/2014",
			audnexusReleaseDate: "2014-08-05",
			want:                AudnexusMissing,
		},
		{
			name:             "missing Audnexus date remains missing",
			absPublishedYear: "2014",
			want:             AudnexusMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var abs models.AudiobookshelfBook
			abs.Media.Metadata.PublishedDate = tt.absPublishedDate
			abs.Media.Metadata.PublishedYear = tt.absPublishedYear
			record := BuildAudnexusRecord(&audnex.Book{ReleaseDate: tt.audnexusReleaseDate})

			got := CompareAudnexus(&abs, record).ReleaseDate
			if got != tt.want {
				t.Fatalf("release date comparison = %q, want %q", got, tt.want)
			}
			if tt.wantNormalizedRelease != "" && record.ReleaseDate != tt.wantNormalizedRelease {
				t.Fatalf("normalized Audnexus display date = %q, want %q", record.ReleaseDate, tt.wantNormalizedRelease)
			}
		})
	}
}
