// +build ignore

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
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

// GoogleUploadInfo contains the signed upload credentials
type GoogleUploadInfo struct {
	URL     string            `json:"url"`     // The base upload URL (e.g., https://storage.googleapis.com/hardcover/)
	Fields  map[string]string `json:"fields"`  // The signed form fields
	FileURL string            // The final public URL where the file will be accessible
}

// downloadLocalImage downloads an image from local AudiobookShelf
func downloadLocalImage(localImageURL string) ([]byte, string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", localImageURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create download request: %v", err)
	}
	
	// Add AudiobookShelf authentication if available
	if token := getAudiobookShelfToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to download image: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, "", "", fmt.Errorf("download failed with status: %d %s", resp.StatusCode, resp.Status)
	}
	
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to read image data: %v", err)
	}
	
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg" // Default assumption
	}
	
	// Extract filename from URL or generate one
	filename := filepath.Base(localImageURL)
	if filename == "." || filename == "/" || strings.Contains(filename, "cover") {
		// Generate a filename based on content type
		switch contentType {
		case "image/jpeg", "image/jpg":
			filename = "cover.jpg"
		case "image/png":
			filename = "cover.png"
		case "image/webp":
			filename = "cover.webp"
		default:
			filename = "cover.jpg"
		}
	}
	
	return imageData, contentType, filename, nil
}

// getGoogleUploadCredentials gets signed upload credentials from Hardcover
func getGoogleUploadCredentials(filename string, editionID int) (*GoogleUploadInfo, error) {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would get upload credentials for file: %s, edition: %d", filename, editionID)
		return &GoogleUploadInfo{
			URL:     "https://storage.googleapis.com/hardcover/",
			FileURL: fmt.Sprintf("https://storage.googleapis.com/hardcover/editions/%d/%s", editionID, filename),
			Fields: map[string]string{
				"key": fmt.Sprintf("editions/%d/%s", editionID, filename),
			},
		}, nil
	}
	
	// This calls the same endpoint the frontend uses: /api/upload/google
	uploadURL := fmt.Sprintf("https://hardcover.app/api/upload/google?file=%s&path=editions/%d", filename, editionID)
	
	req, err := http.NewRequest("POST", uploadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create upload credentials request: %v", err)
	}
	
	// Add authentication headers
	if token := getHardcoverToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get upload credentials: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload credentials request failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	// Read and log the full response to understand the format
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read upload credentials response: %v", err)
	}
	
	debugLog("Upload credentials response: %s", string(bodyBytes))
	
	// Try to parse as the expected format first
	var uploadInfo GoogleUploadInfo
	if err := json.Unmarshal(bodyBytes, &uploadInfo); err != nil {
		// If that fails, try to parse as a different format or just get the raw response
		debugLog("Failed to parse as expected format: %v", err)
		
		// Try parsing as a simple object with different field names
		var rawResponse map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &rawResponse); err != nil {
			return nil, fmt.Errorf("failed to parse upload credentials response as JSON: %v", err)
		}
		
		debugLog("Raw response structure: %+v", rawResponse)
		
		// Extract what we can from the raw response
		if url, ok := rawResponse["url"].(string); ok {
			uploadInfo.URL = url
		}
		if fields, ok := rawResponse["fields"].(map[string]interface{}); ok {
			uploadInfo.Fields = make(map[string]string)
			for k, v := range fields {
				if s, ok := v.(string); ok {
					uploadInfo.Fields[k] = s
				}
			}
		}
		
		// If we still don't have what we need, return an error
		if uploadInfo.URL == "" {
			return nil, fmt.Errorf("upload credentials response missing required URL field")
		}
	}
	
	return &uploadInfo, nil
}

// uploadToGoogleCloudStorage uploads the file to Google Cloud Storage using signed form data
func uploadToGoogleCloudStorage(uploadInfo *GoogleUploadInfo, imageData []byte, filename, contentType string) error {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would upload %d bytes to Google Cloud Storage: %s", len(imageData), uploadInfo.FileURL)
		return nil
	}
	
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	
	// Add all the signed form fields first
	for key, value := range uploadInfo.Fields {
		if err := writer.WriteField(key, value); err != nil {
			return fmt.Errorf("failed to write form field %s: %v", key, err)
		}
	}
	
	// Add the file data last
	fileWriter, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("failed to create file field: %v", err)
	}
	
	if _, err := fileWriter.Write(imageData); err != nil {
		return fmt.Errorf("failed to write file data: %v", err)
	}
	
	writer.Close()
	
	// Upload to Google Cloud Storage
	req, err := http.NewRequest("POST", uploadInfo.URL, &buf)
	if err != nil {
		return fmt.Errorf("failed to create GCS request: %v", err)
	}
	
	req.Header.Set("Content-Type", writer.FormDataContentType())
	
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GCS upload request failed: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GCS upload failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	return nil
}

// uploadLocalImageToExistingEdition uploads a local image to an existing edition
// This is used after the edition has been created and we have the edition ID
func uploadLocalImageToExistingEdition(editionID int, localImageURL string) (int, error) {
	debugLog("Starting Google Cloud Storage upload for existing edition %d: %s", editionID, localImageURL)
	
	// Step 1: Download the image from AudiobookShelf
	imageData, contentType, filename, err := downloadLocalImage(localImageURL)
	if err != nil {
		return 0, fmt.Errorf("failed to download local image: %v", err)
	}
	
	debugLog("Downloaded image: %d bytes, content-type: %s, filename: %s", len(imageData), contentType, filename)
	
	// Step 2: Get signed upload credentials from Hardcover
	uploadInfo, err := getGoogleUploadCredentials(filename, editionID)
	if err != nil {
		return 0, fmt.Errorf("failed to get upload credentials: %v", err)
	}
	
	debugLog("Got upload credentials for URL: %s", uploadInfo.URL)
	
	// The final URL where the file will be accessible needs to be constructed
	// from the upload info. Looking at the key field in the form data:
	uploadInfo.FileURL = fmt.Sprintf("https://storage.googleapis.com/hardcover/%s", uploadInfo.Fields["key"])
	debugLog("Constructed file URL: %s", uploadInfo.FileURL)
	
	// Step 3: Upload the actual file to Google Cloud Storage FIRST
	err = uploadToGoogleCloudStorage(uploadInfo, imageData, filename, contentType)
	if err != nil {
		return 0, fmt.Errorf("file upload to GCS failed: %v", err)
	}
	
	debugLog("Successfully uploaded file to Google Cloud Storage")
	
	// Step 4: Create the image record in GraphQL AFTER the file is uploaded
	debugLog("Creating image record with URL: %s for edition ID: %d", uploadInfo.FileURL, editionID)
	imageID, err := createImageRecordForEdition(editionID, uploadInfo.FileURL)
	if err != nil {
		return 0, fmt.Errorf("failed to create image record: %v", err)
	}
	
	debugLog("Created image record with ID: %d", imageID)
	return imageID, nil
}

// createImageRecordForEdition creates the image record in GraphQL for an existing edition
func createImageRecordForEdition(editionID int, imageURL string) (int, error) {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would create image record for edition %d with URL: %s", editionID, imageURL)
		return 888888, nil // Return fake image ID
	}
	
	mutation := `
	mutation InsertImage($image: ImageInput!) {
		insertResponse: insert_image(image: $image) {
			image {
				id
				url
				imageableId: imageable_id
				imageableType: imageable_type
			}
		}
	}`

	variables := map[string]interface{}{
		"image": map[string]interface{}{
			"imageable_type": "Edition",
			"imageable_id":   editionID,
			"url":           imageURL,
		},
	}

	resp, err := makeHardcoverRequest(mutation, variables)
	if err != nil {
		return 0, fmt.Errorf("GraphQL request failed: %v", err)
	}

	var result struct {
		Data struct {
			InsertResponse struct {
				Image struct {
					ID            int    `json:"id"`
					URL           string `json:"url"`
					ImageableID   int    `json:"imageableId"`
					ImageableType string `json:"imageableType"`
				} `json:"image"`
			} `json:"insertResponse"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, fmt.Errorf("failed to parse GraphQL response: %v", err)
	}

	if len(result.Errors) > 0 {
		return 0, fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if result.Data.InsertResponse.Image.ID == 0 {
		return 0, fmt.Errorf("image creation failed: no ID returned")
	}

	return result.Data.InsertResponse.Image.ID, nil
}

// updateEditionWithImage updates an existing edition to reference an uploaded image
func updateEditionWithImage(editionID int, imageID int) error {
	// Check if we're in dry run mode
	if getDryRun() {
		debugLog("DRY RUN: Would update edition %d with image %d", editionID, imageID)
		return nil
	}
	
	mutation := `
	mutation UpdateEdition($id: Int!, $edition: EditionInput!) {
		update_edition(id: $id, edition: $edition) {
			errors
			id
			edition {
				id
				title
			}
		}
	}`

	variables := map[string]interface{}{
		"id": editionID,
		"edition": map[string]interface{}{
			"dto": map[string]interface{}{
				"image_id": imageID,
			},
		},
	}

	resp, err := makeHardcoverRequest(mutation, variables)
	if err != nil {
		return fmt.Errorf("GraphQL request failed: %v", err)
	}

	var result struct {
		Data struct {
			UpdateEdition struct {
				Errors []string `json:"errors"`
				ID     int      `json:"id"`
				Edition struct {
					ID int `json:"id"`
				} `json:"edition"`
			} `json:"update_edition"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("failed to parse GraphQL response: %v", err)
	}

	if len(result.Errors) > 0 {
		return fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if len(result.Data.UpdateEdition.Errors) > 0 {
		return fmt.Errorf("edition update errors: %v", result.Data.UpdateEdition.Errors)
	}

	if result.Data.UpdateEdition.ID == 0 {
		return fmt.Errorf("edition update failed: no ID returned")
	}

	return nil
}
