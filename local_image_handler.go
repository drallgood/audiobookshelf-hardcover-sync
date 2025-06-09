package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// isLocalAudiobookShelfURL checks if the URL is a local AudiobookShelf server that Hardcover can't access
func isLocalAudiobookShelfURL(imageURL string) bool {
	if imageURL == "" {
		return false
	}
	
	// Check for common local/private network indicators
	localIndicators := []string{
		".local/",
		"localhost",
		"127.0.0.1",
		"192.168.",
		"10.",
		"172.16.", "172.17.", "172.18.", "172.19.", "172.20.",
		"172.21.", "172.22.", "172.23.", "172.24.", "172.25.",
		"172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
	}
	
	for _, indicator := range localIndicators {
		if strings.Contains(imageURL, indicator) {
			return true
		}
	}
	
	return false
}

// uploadLocalImageToHardcover downloads an image from local AudiobookShelf and uploads it to Hardcover
func uploadLocalImageToHardcover(bookID int, localImageURL string) (int, error) {
	debugLog("Downloading image from local AudiobookShelf: %s", localImageURL)
	
	// Download the image from AudiobookShelf
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", localImageURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create image download request: %v", err)
	}
	
	// Add AudiobookShelf authentication if available
	if token := getAudiobookShelfToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to download image from AudiobookShelf: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("failed to download image, status: %d %s", resp.StatusCode, resp.Status)
	}
	
	// Read the image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read image data: %v", err)
	}
	
	debugLog("Downloaded image: %d bytes", len(imageData))
	
	// Get content type for proper handling
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg" // Default assumption
	}
	
	// Create a data URL for the image
	var base64Data string
	switch contentType {
	case "image/jpeg", "image/jpg":
		base64Data = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(imageData)
	case "image/png":
		base64Data = "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageData)
	case "image/webp":
		base64Data = "data:image/webp;base64," + base64.StdEncoding.EncodeToString(imageData)
	default:
		// Assume JPEG if unknown
		base64Data = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(imageData)
	}
	
	debugLog("Created base64 data URL (length: %d)", len(base64Data))
	
	// Try to upload the base64 data URL to Hardcover
	imageID, err := uploadImageToHardcover(base64Data, bookID)
	if err != nil {
		// If base64 upload fails, try the original URL in case it's actually accessible
		debugLog("Base64 upload failed: %v, trying original URL as fallback", err)
		return uploadImageToHardcover(localImageURL, bookID)
	}
	
	debugLog("Successfully uploaded local image to Hardcover with ID: %d", imageID)
	return imageID, nil
}
