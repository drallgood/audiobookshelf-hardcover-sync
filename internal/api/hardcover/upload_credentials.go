package hardcover

import (
	"context"
	"fmt"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// GetGoogleUploadCredentials gets signed upload credentials for Google Cloud Storage
func (c *Client) GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*edition.GoogleUploadInfo, error) {
	// Ensure logger is initialized
	if c.logger == nil {
		c.logger = logger.Get()
	}

	log := c.logger.With(map[string]interface{}{
		"filename":   filename,
		"edition_id": editionID,
	})

	log.Debug("Getting Google Cloud Storage upload credentials", nil)

	query := `
	query GetGoogleUploadCredentials($input: GoogleUploadCredentialsInput!) {
	  google_upload_credentials(input: $input) {
	    url
	    fields
	  }
	}`

	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"filename":       filename,
			"imageable_type": "Edition",
			"imageable_id":   editionID,
		},
	}

	var response struct {
		GoogleUploadCredentials struct {
			URL    string            `json:"url"`
			Fields map[string]string `json:"fields"`
		} `json:"google_upload_credentials"`
	}

	if err := c.GraphQLQuery(ctx, query, variables, &response); err != nil {
		log.Error("Failed to get upload credentials", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to get upload credentials: %w", err)
	}

	if response.GoogleUploadCredentials.URL == "" || response.GoogleUploadCredentials.Fields == nil {
		log.Error("Invalid upload credentials response", map[string]interface{}{
			"response": response,
		})
		return nil, fmt.Errorf("invalid upload credentials response")
	}

	// Construct the final file URL based on the bucket and key from the response
	bucket := response.GoogleUploadCredentials.Fields["bucket"]
	key := response.GoogleUploadCredentials.Fields["key"]
	fileURL := ""
	if bucket != "" && key != "" {
		fileURL = fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, key)
	} else if key != "" {
		// If only key is present, assume it's a path relative to the base URL
		// Don't use path.Join as it normalizes URLs and removes protocol
		baseURL := response.GoogleUploadCredentials.URL
		// Ensure the base URL has a trailing slash for proper joining
		if !strings.HasSuffix(baseURL, "/") {
			baseURL = baseURL + "/"
		}
		fileURL = baseURL + key
	}

	log.Debug("Got upload credentials", map[string]interface{}{
		"url":      response.GoogleUploadCredentials.URL,
		"fields":   response.GoogleUploadCredentials.Fields,
		"file_url": fileURL,
	})

	return &edition.GoogleUploadInfo{
		URL:     response.GoogleUploadCredentials.URL,
		Fields:  response.GoogleUploadCredentials.Fields,
		FileURL: fileURL,
	}, nil
}
