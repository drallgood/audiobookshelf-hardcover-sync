package main

import (
	"testing"
)

// TestIsHardcoverAssetURL tests the simplified image duplicate detection functionality
func TestIsHardcoverAssetURL(t *testing.T) {
	testCases := []struct {
		name               string
		imageURL           string
		expectedIsAsset    bool
		expectedSkipUpload bool
	}{
		{
			name:               "Hardcover assets URL should be detected",
			imageURL:           "https://assets.hardcover.app/external_data/60115489/6828fa93c59d4d4ef7d1933303a3cbf95b7d026e.jpeg",
			expectedIsAsset:    true,
			expectedSkipUpload: true,
		},
		{
			name:               "Audible image URL should not be detected as asset",
			imageURL:           "https://m.media-amazon.com/images/I/51example.jpg",
			expectedIsAsset:    false,
			expectedSkipUpload: false,
		},
		{
			name:               "Empty URL should return false",
			imageURL:           "",
			expectedIsAsset:    false,
			expectedSkipUpload: false,
		},
		{
			name:               "Non-Hardcover assets URL should not be detected",
			imageURL:           "https://covers.openlibrary.org/b/isbn/example.jpg",
			expectedIsAsset:    false,
			expectedSkipUpload: false,
		},
		{
			name:               "Another Hardcover assets URL variant",
			imageURL:           "https://assets.hardcover.app/covers/123456/example.png",
			expectedIsAsset:    true,
			expectedSkipUpload: true,
		},
		{
			name:               "Subdomain variant should be detected",
			imageURL:           "https://cdn.assets.hardcover.app/example.jpg",
			expectedIsAsset:    false, // Only exact domain match
			expectedSkipUpload: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			isAsset, skipUpload, err := isHardcoverAssetURL(tc.imageURL)

			if err != nil {
				t.Fatalf("isHardcoverAssetURL failed: %v", err)
			}

			if isAsset != tc.expectedIsAsset {
				t.Errorf("Expected isAsset=%v, got %v for URL: %s", tc.expectedIsAsset, isAsset, tc.imageURL)
			}

			if skipUpload != tc.expectedSkipUpload {
				t.Errorf("Expected skipUpload=%v, got %v for URL: %s", tc.expectedSkipUpload, skipUpload, tc.imageURL)
			}
		})
	}
}

// TestImageDuplicationPreventionIntegration tests the integration of simplified image duplicate prevention
// with the edition creation process
func TestImageDuplicationPreventionIntegration(t *testing.T) {
	t.Run("HardcoverAssetSkipping", func(t *testing.T) {
		// Test that Hardcover asset URLs are properly skipped
		hardcoverAssetURL := "https://assets.hardcover.app/external_data/60115489/6828fa93c59d4d4ef7d1933303a3cbf95b7d026e.jpeg"

		isAsset, skipUpload, err := isHardcoverAssetURL(hardcoverAssetURL)
		if err != nil {
			t.Fatalf("isHardcoverAssetURL failed: %v", err)
		}

		if !isAsset {
			t.Error("Expected Hardcover asset URL to be detected as asset")
		}

		if !skipUpload {
			t.Error("Expected Hardcover asset URL to trigger skip upload")
		}

		t.Logf("✅ Hardcover asset URL properly detected for skipping: %s", hardcoverAssetURL)
	})

	t.Run("ExternalURLUploading", func(t *testing.T) {
		// Test that external URLs are not skipped
		externalURL := "https://m.media-amazon.com/images/I/51example.jpg"

		isAsset, skipUpload, err := isHardcoverAssetURL(externalURL)
		if err != nil {
			t.Fatalf("isHardcoverAssetURL failed: %v", err)
		}

		if isAsset {
			t.Error("Expected external URL not to be detected as Hardcover asset")
		}

		if skipUpload {
			t.Error("Expected external URL not to trigger skip upload")
		}

		t.Logf("✅ External URL properly flagged for upload: %s", externalURL)
	})
}
