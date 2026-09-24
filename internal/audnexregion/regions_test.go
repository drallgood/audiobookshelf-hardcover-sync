package audnexregion

import (
	"testing"
)

func TestRegions(t *testing.T) {
	regions := Regions()
	expected := []string{"us", "ca", "uk", "au", "de", "fr", "es", "in", "it", "jp"}

	if len(regions) != len(expected) {
		t.Errorf("Regions() returned %d regions, expected %d", len(regions), len(expected))
	}

	for i, region := range regions {
		if region != expected[i] {
			t.Errorf("Regions()[%d] = %q, expected %q", i, region, expected[i])
		}
	}
}

func TestIsRegion(t *testing.T) {
	tests := []struct {
		region   string
		expected bool
	}{
		{"us", true},
		{"ca", true},
		{"uk", true},
		{"au", true},
		{"de", true},
		{"fr", true},
		{"es", true},
		{"in", true},
		{"it", true},
		{"jp", true},
		{"", false},
		{"xx", false},
		{"US", false},
		{"usA", false},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.region, func(t *testing.T) {
			if result := IsRegion(tt.region); result != tt.expected {
				t.Errorf("IsRegion(%q) = %v, expected %v", tt.region, result, tt.expected)
			}
		})
	}
}

func TestRegionsReturnsACopy(t *testing.T) {
	regions1 := Regions()
	regions2 := Regions()

	if len(regions1) != len(regions2) {
		t.Fatal("Two calls to Regions() returned different lengths")
	}

	for i, r := range regions1 {
		if r != regions2[i] {
			t.Fatalf("Two calls to Regions() returned different content at index %d: %q vs %q", i, r, regions2[i])
		}
	}

	regions1[0] = "modified"
	if regions2[0] == "modified" {
		t.Error("Regions() did not return a copy; modifying one affected the other")
	}
}
