package edition

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// EditionInput represents the input data for creating or updating an edition
type EditionInput struct {
	BookID        int    `json:"book_id"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle,omitempty"`
	ImageURL      string `json:"image_url,omitempty"`
	ISBN10        string `json:"isbn_10,omitempty"`
	ISBN13        string `json:"isbn_13,omitempty"`
	ASIN          string `json:"asin,omitempty"`
	PublishedDate string `json:"published_date,omitempty"`
	PublisherID   int    `json:"publisher_id,omitempty"`
	LanguageID    int    `json:"language_id,omitempty"`
	CountryID     int    `json:"country_id,omitempty"`
	AuthorIDs     []int  `json:"author_ids,omitempty"`
	NarratorIDs   []int  `json:"narrator_ids,omitempty"`
	AudioLength   int    `json:"audio_seconds,omitempty"`
	ReleaseDate   string `json:"release_date,omitempty"`
	EditionInfo   string `json:"edition_information,omitempty"`
	EditionFormat string `json:"edition_format,omitempty"`
}

// EditionResult represents the result of an edition creation or update
type EditionResult struct {
	Success   bool `json:"success"`
	EditionID int  `json:"edition_id"`
	ImageID   int  `json:"image_id"`
	// Existing is true when the edition was already on Hardcover for the same
	// book and was reused. A reused edition is returned untouched: no cover or
	// metadata is sent for it.
	Existing bool `json:"existing,omitempty"`
}

// ErrEditionBelongsToOtherBook reports that an existing edition found by ASIN,
// ISBN-13 or ISBN-10 belongs to a different Hardcover book than the requested one, or
// that its book could not be determined. The edition is never adopted.
var ErrEditionBelongsToOtherBook = errors.New("existing edition belongs to a different book")

// GoogleUploadInfo contains the signed upload credentials for Google Cloud Storage
type GoogleUploadInfo struct {
	URL     string            `json:"url"`    // The base upload URL (e.g., https://storage.googleapis.com/hardcover/)
	Fields  map[string]string `json:"fields"` // The signed form fields
	FileURL string            `json:"-"`      // The final public URL where the file will be accessible (not part of JSON)
}

// HardcoverClient defines the interface for the Hardcover client
// that is used by the Creator
//
//go:generate mockery --name=HardcoverClient --output=../mocks --case=underscore --with-expecter=true
type HardcoverClient interface {
	// GetEdition gets an edition by ID
	GetEdition(ctx context.Context, id string) (*models.Edition, error)
	// GetEditionByASIN gets an edition by ASIN
	GetEditionByASIN(ctx context.Context, asin string) (*models.Edition, error)
	// GetEditionByISBN13 gets an edition by ISBN-13
	GetEditionByISBN13(ctx context.Context, isbn13 string) (*models.Edition, error)
	// GetEditionByISBN10 gets an edition by ISBN-10
	GetEditionByISBN10(ctx context.Context, isbn10 string) (*models.Edition, error)
	// GraphQLQuery executes a GraphQL query
	GraphQLQuery(ctx context.Context, query string, variables map[string]interface{}, result interface{}) error
	// GraphQLMutation executes a GraphQL mutation
	GraphQLMutation(ctx context.Context, mutation string, variables map[string]interface{}, result interface{}) error
	// GetGoogleUploadCredentials gets signed upload credentials for Google Cloud Storage
	GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*GoogleUploadInfo, error)
	// GetAuthHeader gets the authentication header for the client
	GetAuthHeader() string
}

// Creator handles the creation of audiobook editions in Hardcover
type Creator struct {
	client              HardcoverClient
	log                 *logger.Logger
	dryRun              bool
	audiobookshelfToken string // Token for authenticating with Audiobookshelf
	// audiobookshelfBaseURL, when set, limits the Audiobookshelf token to
	// image URLs under this base URL. When empty the legacy heuristic applies.
	audiobookshelfBaseURL string
	httpClient            *http.Client // Custom HTTP client for testing
}

// SetAudiobookshelfBaseURL restricts sending the Audiobookshelf token to image
// URLs hosted under baseURL. Without it, the token is sent to any URL that
// contains "audiobookshelf", which misses self-hosted names such as abs.home.
func (c *Creator) SetAudiobookshelfBaseURL(baseURL string) {
	c.audiobookshelfBaseURL = strings.TrimSpace(baseURL)
}

// shouldSendAudiobookshelfToken reports whether imageURL is an Audiobookshelf
// URL that may receive the Audiobookshelf bearer token.
func (c *Creator) shouldSendAudiobookshelfToken(imageURL string) bool {
	if c.audiobookshelfToken == "" {
		return false
	}
	if c.audiobookshelfBaseURL == "" {
		return strings.Contains(imageURL, "audiobookshelf")
	}
	base, err := url.Parse(c.audiobookshelfBaseURL)
	if err != nil || base.Host == "" {
		return false
	}
	target, err := url.Parse(imageURL)
	if err != nil {
		return false
	}
	if !strings.EqualFold(base.Scheme, target.Scheme) || !strings.EqualFold(base.Host, target.Host) {
		return false
	}
	basePath := strings.TrimRight(base.Path, "/")
	return basePath == "" || target.Path == basePath || strings.HasPrefix(target.Path, basePath+"/")
}

// NewCreator creates a new instance of the edition creator
func NewCreator(client HardcoverClient, log *logger.Logger, dryRun bool, audiobookshelfToken string) *Creator {
	// Create a default HTTP client with reasonable timeouts
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Only for development, consider making this configurable
		},
		MaxIdleConns:           10,
		MaxIdleConnsPerHost:    10,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		DisableCompression:     false,
		DisableKeepAlives:      false,
		MaxResponseHeaderBytes: 10 * 1024 * 1024, // 10MB max header size
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   300 * time.Second, // 5 minute timeout for large uploads
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if len(via) > 0 && via[0].Header.Get("Authorization") != "" {
				req.Header.Set("Authorization", via[0].Header.Get("Authorization"))
			}
			return nil
		},
	}

	return &Creator{
		client:              client,
		log:                 log,
		dryRun:              dryRun,
		audiobookshelfToken: audiobookshelfToken,
		httpClient:          httpClient,
	}
}

// NewCreatorWithHTTPClient creates a new instance of the edition creator with a custom HTTP client
// This is primarily for testing purposes
func NewCreatorWithHTTPClient(client HardcoverClient, log *logger.Logger, dryRun bool, audiobookshelfToken string, httpClient *http.Client) *Creator {
	return &Creator{
		client:              client,
		log:                 log,
		dryRun:              dryRun,
		audiobookshelfToken: audiobookshelfToken,
		httpClient:          httpClient,
	}
}

// CreateEdition creates a new audiobook edition in Hardcover
func (c *Creator) CreateEdition(ctx context.Context, input *EditionInput) (*EditionResult, error) {
	// Validate input
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	c.log.Debug("Creating new audiobook edition", map[string]interface{}{
		"book_id": input.BookID,
		"title":   input.Title,
		"dry_run": c.dryRun,
	})

	if c.dryRun {
		c.log.Info("Dry run enabled - no changes will be made", nil)
		return &EditionResult{
			Success:   true,
			EditionID: 0,
			ImageID:   0,
		}, nil
	}

	// Step 1: Create the edition first (without image)
	editionID, existing, err := c.createEdition(ctx, input, 0) // Pass 0 as imageID initially
	if err != nil {
		return nil, fmt.Errorf("failed to create edition: %w", err)
	}
	if existing {
		// An edition of this book was already on Hardcover. Leave it as it is.
		return &EditionResult{Success: true, EditionID: editionID, Existing: true}, nil
	}

	// Step 2: If we have an image URL, upload it and update the edition
	var imageID int
	if input.ImageURL != "" {
		// First upload the image to Google Cloud Storage
		imageURL, uploadErr := c.uploadImageToGCS(ctx, editionID, input.ImageURL)
		if uploadErr != nil {
			c.log.Error("Failed to upload image to GCS, continuing without it",
				map[string]interface{}{"error": uploadErr.Error()})
		} else {
			// Then create the image record with the edition ID
			imageID, err = c.CreateImageRecord(ctx, editionID, imageURL)
			if err != nil {
				c.log.Error("Failed to create image record, continuing without it",
					map[string]interface{}{"error": err.Error()})
			} else {
				// Finally, update the edition with the new image ID
				updateErr := c.updateEditionImage(ctx, editionID, imageID)
				if updateErr != nil {
					c.log.Error("Failed to update edition with image ID, but continuing",
						map[string]interface{}{"error": updateErr.Error()})
				}
			}
		}
	}

	return &EditionResult{
		Success:   true,
		EditionID: editionID,
		ImageID:   imageID,
	}, nil
}

// uploadImageToGCS uploads an image to Google Cloud Storage and returns the public URL
func (c *Creator) uploadImageToGCS(ctx context.Context, editionID int, imageURL string) (string, error) {
	log := c.log.With(map[string]interface{}{
		"edition_id": editionID,
		"image_url":  imageURL,
	})

	log.Debug("Starting image upload process")

	// Step 1: Download the image
	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		log.Error("Failed to create download request", map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	// Set headers for the download request
	downloadReq.Header.Set("User-Agent", "Audiobookshelf-Hardcover-Sync/1.0")
	downloadReq.Header.Set("Accept", "image/*")

	// Add Audiobookshelf token if available and the URL is from Audiobookshelf
	if c.shouldSendAudiobookshelfToken(imageURL) {
		downloadReq.Header.Set("Authorization", "Bearer "+c.audiobookshelfToken)
		log.Debug("Added Audiobookshelf token to download request")
	}

	// Download the image
	log.Debug("Downloading image")

	resp, err := c.httpClient.Do(downloadReq)
	if err != nil {
		log.Error("Image download failed", map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("image download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("image download failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Read the image data
	imgData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Determine file extension from content type
	extension := "jpg"
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "png") {
		extension = "png"
	} else if strings.Contains(contentType, "webp") {
		extension = "webp"
	}

	// Generate a unique filename
	filename := fmt.Sprintf("cover-%d.%s", time.Now().Unix(), extension)

	// Step 2: Get upload token directly from Hardcover API
	log.Debug("Getting upload credentials from Hardcover", map[string]interface{}{
		"filename":   filename,
		"edition_id": editionID,
	})

	// Construct the API URL for getting upload credentials
	uploadURL := "https://hardcover.app/api/upload/google"

	// Create the request with query parameters
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, nil) // Use POST method as per docs
	if err != nil {
		log.Error("Failed to create request", map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("file", filename)
	q.Add("path", fmt.Sprintf("editions/%d", editionID))
	req.URL.RawQuery = q.Encode()

	// Set headers to look like a legitimate browser request
	req.Header.Set("Content-Length", "0") // Important for POST with empty body
	req.Header.Set("Authorization", c.client.GetAuthHeader())
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Origin", "https://hardcover.app")
	req.Header.Set("Referer", "https://hardcover.app/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("sec-ch-ua", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"macOS"`)

	// Send the request
	respCreds, err := c.httpClient.Do(req)
	if err != nil {
		log.Error("Failed to send request", map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer respCreds.Body.Close()

	// Check the response
	if respCreds.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respCreds.Body)
		log.Error("Failed to get upload credentials", map[string]interface{}{
			"status": respCreds.StatusCode,
			"body":   string(body),
		})
		return "", fmt.Errorf("failed to get upload credentials: HTTP %d: %s", respCreds.StatusCode, string(body))
	}

	// Parse the response - handle gzip compression if present
	var reader io.Reader = respCreds.Body
	if strings.Contains(respCreds.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, err := gzip.NewReader(respCreds.Body)
		if err != nil {
			log.Error("Failed to create gzip reader", map[string]interface{}{"error": err.Error()})
			return "", fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	var uploadInfo GoogleUploadInfo
	if err := json.NewDecoder(reader).Decode(&uploadInfo); err != nil {
		log.Error("Failed to parse upload credentials", map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("failed to parse upload credentials: %w", err)
	}

	log.Debug("Got upload credentials", map[string]interface{}{
		"url":    uploadInfo.URL,
		"fields": uploadInfo.Fields,
	})

	// Step 3: Upload to Google Cloud Storage
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add form fields
	for key, value := range uploadInfo.Fields {
		if key != "file" { // Skip the file field as we'll add it separately
			_ = writer.WriteField(key, value)
		}
	}

	// Add the file
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}

	// Copy the image data to the form
	if _, err = io.Copy(part, bytes.NewReader(imgData)); err != nil {
		return "", fmt.Errorf("failed to copy image data: %w", err)
	}

	// Close the writer to finalize the multipart message
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create the upload request
	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadInfo.URL, &requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to create upload request: %w", err)
	}

	// Set the content type with the boundary
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("Origin", "https://hardcover.app")
	uploadReq.Header.Set("Referer", "https://hardcover.app/")

	// Execute the upload request
	uploadResp, err := c.httpClient.Do(uploadReq)
	if err != nil {
		return "", fmt.Errorf("failed to execute upload request: %w", err)
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusNoContent && uploadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(uploadResp.Body)
		return "", fmt.Errorf("upload failed: HTTP %d: %s", uploadResp.StatusCode, string(body))
	}

	// Step 4: Get the file path from the upload info
	filePath, ok := uploadInfo.Fields["key"]
	if !ok {
		return "", fmt.Errorf("missing file path in upload info")
	}

	// Return the public URL of the uploaded image
	// Use the assets.hardcover.app URL format as shown in the documentation
	uploadedImageURL := fmt.Sprintf("https://assets.hardcover.app/%s", filePath)
	log.Debug("Successfully uploaded image to GCS", map[string]interface{}{
		"url": uploadedImageURL,
	})
	return uploadedImageURL, nil
}

// CreateImageRecord creates an image record in Hardcover for an uploaded image
func (c *Creator) CreateImageRecord(ctx context.Context, editionID int, imageURL string) (int, error) {
	c.log.Debug("Creating image record in Hardcover", map[string]interface{}{
		"edition_id": editionID,
		"image_url":  imageURL,
	})

	// The GraphQL mutation to create an image record
	mutation := `
	mutation CreateImage($image: ImageInput!) {
	  insert_image(image: $image) {
	    id
	  }
	}`

	// Prepare the variables for the mutation
	variables := map[string]interface{}{
		"image": map[string]interface{}{
			"imageable_type": "Edition",
			"imageable_id":   editionID,
			"url":            imageURL,
		},
	}

	// Define a proper response structure that matches the GraphQL response
	type responseStruct struct {
		InsertImage struct {
			ID int `json:"id"`
		} `json:"insert_image"`
	}

	var response responseStruct

	// Debug: Log the raw GraphQL request
	c.log.Debug("Sending GraphQL mutation", map[string]interface{}{
		"mutation": mutation,
		"variables": map[string]interface{}{
			"image": map[string]interface{}{
				"imageable_id":   editionID,
				"imageable_type": "Edition",
				"url":            imageURL,
			},
		},
	})

	// Execute the mutation
	err := c.client.GraphQLMutation(ctx, mutation, variables, &response)

	if err != nil {
		c.log.Error("GraphQL mutation failed", map[string]interface{}{
			"edition_id": editionID,
			"error":      err.Error(),
		})
		return 0, fmt.Errorf("graphql mutation failed: %w", err)
	}

	// Debug log the response
	c.log.Debug("GraphQL response", map[string]interface{}{
		"edition_id": editionID,
		"response":   response,
	})

	// Get the image ID from the response
	imageID := response.InsertImage.ID

	if imageID == 0 {
		c.log.Error("Failed to get image ID from response", map[string]interface{}{
			"edition_id": editionID,
			"response":   response,
		})
		return 0, fmt.Errorf("API response did not contain a valid image ID")
	}

	c.log.Debug("Successfully parsed image ID from response", map[string]interface{}{
		"edition_id": editionID,
		"image_id":   imageID,
	})

	c.log.Debug("Image record created", map[string]interface{}{
		"edition_id": editionID,
		"image_id":   imageID,
	})

	c.log.Debug("Successfully created image record", map[string]interface{}{
		"edition_id": editionID,
		"image_id":   imageID,
	})

	return imageID, nil
}

// UploadEditionImage handles the entire flow of uploading an image to an edition
func (c *Creator) UploadEditionImage(ctx context.Context, editionID int, imageURL, description string) error {
	// Upload the image to GCS
	uploadedImageURL, err := c.uploadImageToGCS(ctx, editionID, imageURL)
	if err != nil {
		return fmt.Errorf("failed to upload image to GCS: %w", err)
	}

	// Create an image record in Hardcover
	imageID, err := c.CreateImageRecord(ctx, editionID, uploadedImageURL)
	if err != nil {
		return fmt.Errorf("failed to create image record: %w", err)
	}

	// Update the edition with the new image
	if err := c.updateEditionImage(ctx, editionID, imageID); err != nil {
		return fmt.Errorf("failed to update edition with new image: %w", err)
	}

	return nil
}

func (c *Creator) updateEditionImage(ctx context.Context, editionID, imageID int) error {
	if editionID == 0 || imageID == 0 {
		return fmt.Errorf("invalid edition ID or image ID (edition: %d, image: %d)", editionID, imageID)
	}

	c.log.Debug("Starting edition image update", map[string]interface{}{
		"edition_id": editionID,
		"image_id":   imageID,
	})

	mutation := `
	mutation UpdateEdition($id: Int!, $edition: EditionInput!) {
  update_edition(id: $id, edition: $edition) {
    id
    errors
  }
}`

	editionInput := map[string]interface{}{
		"dto": map[string]interface{}{
			"image_id": imageID,
		},
	}

	variables := map[string]interface{}{
		"id":      editionID,
		"edition": editionInput,
	}

	c.log.Debug("Executing update_edition mutation", map[string]interface{}{
		"edition_id": editionID,
		"image_id":   imageID,
		"variables":  variables,
	})

	// Define the expected response structure
	var response struct {
		UpdateEdition struct {
			ID     interface{} `json:"id"`
			Errors []string    `json:"errors"`
		} `json:"update_edition"`
	}

	// Execute the mutation
	if err := c.client.GraphQLMutation(ctx, mutation, variables, &response); err != nil {
		c.log.Error("Failed to update edition with new image", map[string]interface{}{
			"edition_id": editionID,
			"image_id":   imageID,
			"error":      err.Error(),
		})
		return fmt.Errorf("graphql mutation failed: %w", err)
	}

	// Check for errors in the response
	if len(response.UpdateEdition.Errors) > 0 {
		errMsg := strings.Join(response.UpdateEdition.Errors, "; ")
		c.log.Error("Edition update failed with errors", map[string]interface{}{
			"edition_id": editionID,
			"image_id":   imageID,
			"errors":     response.UpdateEdition.Errors,
		})
		return fmt.Errorf("edition update failed: %s", errMsg)
	}

	// Verify the response contains a valid ID
	if response.UpdateEdition.ID == nil {
		c.log.Error("Missing edition ID in update response", map[string]interface{}{
			"edition_id": editionID,
			"image_id":   imageID,
			"response":   response,
		})
		return fmt.Errorf("missing edition ID in update response")
	}

	// Log success
	c.log.Debug("Successfully updated edition with new image", map[string]interface{}{
		"edition_id": editionID,
		"image_id":   imageID,
	})

	return nil
}

// CreateEditionInput represents the input for creating a new edition
type CreateEditionInput struct {
	BookID          int      `json:"bookId"`
	Title           string   `json:"title"`
	Subtitle        *string  `json:"subtitle,omitempty"`
	ISBN10          *string  `json:"isbn10,omitempty"`
	ISBN13          *string  `json:"isbn13,omitempty"`
	ASIN            *string  `json:"asin,omitempty"`
	AuthorIDs       []int    `json:"authorIds"`
	NarratorIDs     []int    `json:"narratorIds,omitempty"`
	PublisherID     *int     `json:"publisherId,omitempty"`
	PublicationDate *string  `json:"publicationDate,omitempty"`
	EditionFormat   *string  `json:"editionFormat,omitempty"`
	LanguageID      *int     `json:"languageId,omitempty"`
	CountryID       *int     `json:"countryId,omitempty"`
	ImageID         *int     `json:"imageId,omitempty"`
	AudioLength     *int     `json:"audioLength,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

// adoptExistingEdition returns the ID of an edition found on Hardcover by ASIN
// or ISBN so it can be reused instead of duplicated. The lookups are global,
// so the edition is adopted only when it belongs to the requested book; any
// other or unknown book fails closed with ErrEditionBelongsToOtherBook.
func adoptExistingEdition(found *models.Edition, input *EditionInput) (int, error) {
	if found.BookID != strconv.Itoa(input.BookID) {
		return 0, ErrEditionBelongsToOtherBook
	}
	editionID, err := strconv.Atoi(found.ID)
	if err != nil || editionID <= 0 {
		return 0, fmt.Errorf("existing edition has an invalid ID %q", found.ID)
	}
	return editionID, nil
}

// editionLookup is one identifier to look an existing edition up by.
type editionLookup struct{ kind, value string }

// existingEditionLookups lists, in order, the identifiers that may already
// identify an edition: the ASIN, the given ISBN-13 and ISBN-10, then the forms
// derived from them (an ISBN-10's ISBN-13 and the reverse), without duplicates.
func existingEditionLookups(input *EditionInput) []editionLookup {
	var lookups []editionLookup
	seen := map[editionLookup]struct{}{}
	add := func(kind, value string) {
		l := editionLookup{kind: kind, value: value}
		if value == "" {
			return
		}
		if _, dup := seen[l]; !dup {
			seen[l] = struct{}{}
			lookups = append(lookups, l)
		}
	}
	add("ASIN", strings.TrimSpace(input.ASIN))
	given13, ok13 := isbn.Parse(input.ISBN13)
	given10, ok10 := isbn.Parse(input.ISBN10)
	if ok13 {
		add("ISBN-13", given13.ISBN13())
	}
	if ok10 {
		add("ISBN-10", given10.ISBN10())
	}
	if ok10 {
		add("ISBN-13", given10.ISBN13())
	}
	if ok13 {
		add("ISBN-10", given13.ISBN10())
	}
	return lookups
}

// findExistingEdition looks up each identifier of the input on Hardcover and
// returns the first edition found. A failed lookup counts as not found, so a
// transient error never blocks creation on its own.
func (c *Creator) findExistingEdition(ctx context.Context, input *EditionInput) (*models.Edition, editionLookup) {
	for _, l := range existingEditionLookups(input) {
		var (
			found *models.Edition
			err   error
		)
		switch l.kind {
		case "ASIN":
			found, err = c.client.GetEditionByASIN(ctx, l.value)
		case "ISBN-13":
			found, err = c.client.GetEditionByISBN13(ctx, l.value)
		default:
			found, err = c.client.GetEditionByISBN10(ctx, l.value)
		}
		if err == nil && found != nil && found.ID != "" {
			return found, l
		}
	}
	return nil, editionLookup{}
}

// createEdition creates a new edition with the given metadata. Before inserting
// it looks for an existing edition with the same ASIN, ISBN-13 or ISBN-10 (or a
// converted ISBN form). One that belongs to the same book is returned with true
// and left untouched; one of another book is an error.
func (c *Creator) createEdition(ctx context.Context, input *EditionInput, imageID int) (int, bool, error) {
	if found, by := c.findExistingEdition(ctx, input); found != nil {
		editionID, adoptErr := adoptExistingEdition(found, input)
		if adoptErr != nil {
			return 0, false, adoptErr
		}
		c.log.Debug("Edition already exists", map[string]interface{}{
			"edition_id": editionID,
			"matched_by": by.kind,
			"identifier": by.value,
		})
		return editionID, true, nil
	}

	// Prepare the GraphQL mutation with errors field
	mutation := `
	mutation CreateEdition($bookId: Int!, $edition: EditionInput!) {
	  insert_edition(book_id: $bookId, edition: $edition) {
	    id
	    errors
	  }
	}`

	// The format label is free text; reading_format_id stays Audiobook.
	editionFormat := strings.TrimSpace(input.EditionFormat)
	if editionFormat == "" {
		editionFormat = "Audiobook"
	}

	// Initialize edition data with required fields
	editionData := map[string]interface{}{
		"dto": map[string]interface{}{
			"title":             input.Title,
			"edition_format":    editionFormat,
			"reading_format_id": 2, // 2 is the ID for Audiobook format
		},
	}

	// Get the dto object, create it if it doesn't exist
	dto, ok := editionData["dto"].(map[string]interface{})
	if !ok {
		dto = make(map[string]interface{})
		editionData["dto"] = dto
	}

	// Add optional fields to dto if they exist
	if input.Subtitle != "" {
		dto["subtitle"] = input.Subtitle
	}

	// Add ASIN to dto if provided
	if input.ASIN != "" {
		dto["asin"] = input.ASIN
	}

	// Add ISBNs to dto if provided
	if input.ISBN10 != "" {
		dto["isbn_10"] = input.ISBN10
	}

	if input.ISBN13 != "" {
		dto["isbn_13"] = input.ISBN13
	}

	// Add authors and narrators as contributions
	var contributions []map[string]interface{}
	for _, authorID := range input.AuthorIDs {
		contributions = append(contributions, map[string]interface{}{
			"author_id":    authorID,
			"contribution": nil,
		})
	}

	for _, narratorID := range input.NarratorIDs {
		contributions = append(contributions, map[string]interface{}{
			"author_id":    narratorID,
			"contribution": "Narrator",
		})
	}

	if len(contributions) > 0 {
		dto["contributions"] = contributions
	}

	// Set publisher if provided
	if input.PublisherID > 0 {
		dto["publisher_id"] = input.PublisherID
	}

	if input.LanguageID > 0 {
		dto["language_id"] = input.LanguageID
	}

	if input.CountryID > 0 {
		dto["country_id"] = input.CountryID
	}

	// Set audio length if provided
	if input.AudioLength > 0 {
		dto["audio_seconds"] = input.AudioLength
	}

	// Set release date if provided
	if input.ReleaseDate != "" {
		dto["release_date"] = input.ReleaseDate
	}

	// Set edition information if provided
	if input.EditionInfo != "" {
		dto["edition_information"] = input.EditionInfo
	}

	if imageID > 0 {
		dto["image_id"] = imageID
	}

	// Prepare variables for the mutation
	editionInput := editionData // Use the edition data directly as the input

	variables := map[string]interface{}{
		"bookId":  input.BookID,
		"edition": editionInput,
	}

	// The client handles the top-level GraphQL response, we just need to define the data structure
	var response struct {
		InsertEdition struct {
			ID     interface{} `json:"id"`
			Errors []string    `json:"errors"`
		} `json:"insert_edition"`
	}

	c.log.Debug("Executing GraphQL mutation", map[string]interface{}{
		"mutation":  mutation,
		"variables": variables,
	})

	// Execute the GraphQL mutation
	if err := c.client.GraphQLMutation(ctx, mutation, variables, &response); err != nil {
		return 0, false, fmt.Errorf("GraphQL mutation failed: %w", err)
	}

	// Check for errors in the response
	if len(response.InsertEdition.Errors) > 0 {
		errMsg := strings.Join(response.InsertEdition.Errors, "; ")
		c.log.Error("Edition creation failed with errors", map[string]interface{}{
			"errors": response.InsertEdition.Errors,
		})

		// A duplicate error means an edition with these identifiers appeared after
		// the proactive lookup (a race); look it up again to reuse it.
		if strings.Contains(errMsg, "already exists") {
			if found, _ := c.findExistingEdition(ctx, input); found != nil {
				editionID, adoptErr := adoptExistingEdition(found, input)
				if adoptErr != nil {
					return 0, false, adoptErr
				}
				return editionID, true, nil
			}
			return 0, false, fmt.Errorf("edition already exists but could not find existing edition: %s", errMsg)
		}

		// For other errors, return the error message
		return 0, false, fmt.Errorf("edition creation failed: %s", errMsg)
	}

	// Handle different ID types (int, float64, or string)
	var editionID int
	switch id := response.InsertEdition.ID.(type) {
	case float64:
		editionID = int(id)
	case int:
		editionID = id
	case string:
		// Try to parse string ID as int
		parsedID, err := strconv.Atoi(id)
		if err != nil {
			c.log.Error("Failed to parse edition ID as int", map[string]interface{}{
				"id":    id,
				"error": err.Error(),
			})
			return 0, false, fmt.Errorf("invalid edition ID format: %v", id)
		}
		editionID = parsedID
	case nil:
		c.log.Error("Missing edition ID in response", map[string]interface{}{
			"response": response,
		})
		return 0, false, fmt.Errorf("missing edition ID in response")
	default:
		c.log.Error("Unexpected ID type in response", map[string]interface{}{
			"id":   response.InsertEdition.ID,
			"type": fmt.Sprintf("%T", response.InsertEdition.ID),
		})
		return 0, false, fmt.Errorf("unexpected ID type in response: %T", response.InsertEdition.ID)
	}

	if editionID <= 0 {
		c.log.Error("Invalid edition ID in response", map[string]interface{}{
			"edition_id": editionID,
			"response":   response,
		})
		return 0, false, fmt.Errorf("invalid edition ID in response: %d", editionID)
	}

	// Success! Return the new edition ID
	c.log.Debug("Successfully created new edition", map[string]interface{}{
		"edition_id": editionID,
	})

	return editionID, false, nil
}
func (c *Creator) PrepopulateFromBook(ctx context.Context, bookID int) (*EditionInput, error) {
	c.log.Debug("Prepopulating edition data from book", map[string]interface{}{
		"book_id": bookID,
	})

	// First, get the edition details to ensure the book exists
	_, err := c.client.GetEdition(ctx, strconv.Itoa(bookID))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch edition details: %w", err)
	}

	// Get the book details using GraphQL query
	query := `
	query GetBook($id: ID!) {
	  book(id: $id) {
	    id
	    title
	    subtitle
	    authors {
	      id
	      name
	    }
	    narrators {
	      id
	      name
	    }
	    publisher {
	      id
	      name
	    }
	    coverImageUrl
	    isbn10
	    isbn13
	    asin
	    publishedDate
	    language {
	      id
	      name
	    }
	    country {
	      id
	      name
	    }
	  }
	}`

	var response struct {
		Book struct {
			ID            int    `json:"id"`
			Title         string `json:"title"`
			Subtitle      string `json:"subtitle"`
			CoverImageURL string `json:"coverImageUrl"`
			ISBN10        string `json:"isbn10"`
			ISBN13        string `json:"isbn13"`
			ASIN          string `json:"asin"`
			PublishedDate string `json:"publishedDate"`
			Authors       []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"authors"`
			Narrators []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"narrators"`
			Publisher *struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"publisher"`
			Language *struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"language"`
			Country *struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"country"`
		} `json:"book"`
	}

	// Execute the query using GraphQLQuery
	if err := c.client.GraphQLQuery(ctx, query, map[string]interface{}{"id": bookID}, &response); err != nil {
		return nil, fmt.Errorf("failed to fetch book details: %w", err)
	}

	// Map the response to our input struct
	book := response.Book
	input := &EditionInput{
		BookID:      bookID,
		Title:       book.Title,
		Subtitle:    book.Subtitle,
		ImageURL:    book.CoverImageURL,
		ISBN10:      book.ISBN10,
		ISBN13:      book.ISBN13,
		ASIN:        book.ASIN,
		ReleaseDate: book.PublishedDate,
		// Set the edition format to Audiobook by default
		EditionFormat: "Audiobook",
	}

	// Add authors
	for _, author := range book.Authors {
		input.AuthorIDs = append(input.AuthorIDs, author.ID)
	}

	// Add narrators
	for _, narrator := range book.Narrators {
		input.NarratorIDs = append(input.NarratorIDs, narrator.ID)
	}

	// Add publisher if available
	if book.Publisher != nil {
		input.PublisherID = book.Publisher.ID
	}

	// Add language if available (default to English)
	if book.Language != nil {
		input.LanguageID = book.Language.ID
	} else {
		input.LanguageID = 1 // Default to English
	}

	// Add country if available (default to USA)
	if book.Country != nil {
		input.CountryID = book.Country.ID
	} else {
		input.CountryID = 1 // Default to USA
	}

	// Set default values
	if input.EditionFormat == "" {
		input.EditionFormat = "Audiobook"
	}

	return input, nil
}

// EditionInput represents the input data for creating a new edition

// Validate validates the edition input
func (e *EditionInput) Validate() error {
	if e.BookID == 0 {
		return errors.New("book_id is required")
	}
	if e.Title == "" {
		return errors.New("title is required")
	}
	if len(e.AuthorIDs) == 0 {
		return errors.New("at least one author is required")
	}
	if e.ReleaseDate != "" {
		if _, err := time.Parse("2006-01-02", e.ReleaseDate); err != nil {
			return fmt.Errorf("invalid release_date format, expected YYYY-MM-DD: %w", err)
		}
	}
	return nil
}
