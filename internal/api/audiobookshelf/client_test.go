package audiobookshelf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	client := NewClient("http://example.com", "test-token")
	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com", client.baseURL)
	assert.Equal(t, "test-token", client.token)
	assert.NotNil(t, client.client)
}

func TestGetLibraries(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func() *httptest.Server
		expectedResult []AudiobookshelfLibrary
		expectError    bool
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/libraries", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					response := struct {
						Libraries []AudiobookshelfLibrary `json:"libraries"`
					}{
						Libraries: []AudiobookshelfLibrary{
							{ID: "1", Name: "Library 1"},
							{ID: "2", Name: "Library 2"},
						},
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(response); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				})
				return httptest.NewServer(handler)
			},
			expectedResult: []AudiobookshelfLibrary{
				{ID: "1", Name: "Library 1"},
				{ID: "2", Name: "Library 2"},
			},
			expectError: false,
		},
		{
			name: "server error",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
				return httptest.NewServer(handler)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			libraries, err := client.GetLibraries(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, libraries)
			}
		})
	}
}

func TestGetLibraryItems(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func() *httptest.Server
		libraryID      string
		expectError    bool
		expectedLength int
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/libraries/1/items", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					// Return a minimal valid response
					response := map[string]interface{}{
						"results": []map[string]interface{}{
							{
								"id":    "book1",
								"title": "Test Book",
								"media": map[string]interface{}{
									"metadata": map[string]interface{}{
										"title":      "Test Book",
										"authorName": "Test Author",
									},
								},
							},
						},
						"total": 1,
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(response); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				})
				return httptest.NewServer(handler)
			},
			libraryID:      "1",
			expectError:    false,
			expectedLength: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			items, err := client.GetLibraryItems(context.Background(), tt.libraryID)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, items, tt.expectedLength)
			}
		})
	}
}

func TestGetLibraryItemByIDExpandedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/items/li_123", r.URL.Path)
		assert.Equal(t, "1", r.URL.Query().Get("expanded"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"li_123",
			"libraryId":"lib_1",
			"mediaType":"book",
			"media":{
				"metadata":{
					"title":"A Book",
					"subtitle":"An Expanded Record",
					"authorName":"Ada Author",
					"narratorName":"Nora Narrator",
					"publishedDate":"2024-02-03",
					"publishedYear":"2024",
					"asin":"B012345678",
					"isbn":"9781234567890",
					"language":"eng"
				},
				"duration":0,
				"audioFiles":[{"exclude":false}],
				"ebookFile":{},
				"ebookFormat":"EPUB"
			}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	item, err := client.GetLibraryItemByID(context.Background(), "li_123")
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, "li_123", item.ID)
	assert.Equal(t, "A Book", item.Media.Metadata.Title)
	assert.Equal(t, "An Expanded Record", item.Media.Metadata.Subtitle)
	assert.Equal(t, "Ada Author", item.Media.Metadata.AuthorName)
	assert.Equal(t, "Nora Narrator", item.Media.Metadata.NarratorName)
	assert.Equal(t, "2024-02-03", item.Media.Metadata.PublishedDate)
	assert.Equal(t, "2024", item.Media.Metadata.PublishedYear)
	assert.Equal(t, "B012345678", item.Media.Metadata.ASIN)
	assert.Equal(t, "9781234567890", item.Media.Metadata.ISBN)
	assert.Equal(t, "eng", item.Media.Metadata.Language)
	assert.False(t, item.IsEbook(), "expanded audio files should identify audiobook media")

	ebookJSON := `{"id":"li_ebook","mediaType":"book","media":{"metadata":{"title":"An Ebook"},"ebookFile":{},"ebookFormat":"EPUB"}}`
	ebookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ebookJSON))
	}))
	defer ebookServer.Close()
	ebook, err := NewClient(ebookServer.URL, "test-token").GetLibraryItemByID(context.Background(), "li_ebook")
	require.NoError(t, err)
	assert.True(t, ebook.IsEbook(), "expanded ebook file metadata should identify ebook-only media")
}

func TestGetLibraryItemByIDErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		itemID  string
		wantErr string
	}{
		{
			name:    "not found",
			status:  http.StatusNotFound,
			body:    `{}`,
			itemID:  "li_missing",
			wantErr: "status 404",
		},
		{
			name:    "malformed response",
			status:  http.StatusOK,
			body:    `{"id":`,
			itemID:  "li_123",
			wantErr: "failed to decode",
		},
		{
			name:    "response item differs from requested ID",
			status:  http.StatusOK,
			body:    `{"id":"li_other"}`,
			itemID:  "li_123",
			wantErr: "when \"li_123\" was requested",
		},
		{
			name:    "response has no item ID",
			status:  http.StatusOK,
			body:    `{"mediaType":"book"}`,
			itemID:  "li_123",
			wantErr: "without an ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			item, err := NewClient(server.URL, "test-token").GetLibraryItemByID(context.Background(), tt.itemID)
			require.Nil(t, item)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("empty item ID", func(t *testing.T) {
		item, err := NewClient("http://127.0.0.1", "test-token").GetLibraryItemByID(context.Background(), " \t ")
		require.Nil(t, item)
		require.EqualError(t, err, "item ID is required")
	})
}

func TestGetLibraryItemByIDCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewClient(server.URL, "test-token")
	done := make(chan error, 1)
	go func() {
		_, err := client.GetLibraryItemByID(ctx, "li_123")
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach the Audiobookshelf server")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
		require.True(t, errors.Is(err, context.Canceled), "expected wrapped cancellation error, got %v", err)
	case <-time.After(time.Second):
		t.Fatal("request did not return after its context was canceled")
	}
}

func TestGetUserProgress(t *testing.T) {
	tests := []struct {
		name          string
		setupServer   func() *httptest.Server
		expectError   bool
		expectedItems int
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/me", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					now := time.Now()
					progress := models.AudiobookshelfUserProgress{
						ID:       "user1",
						Username: "testuser",
						MediaProgress: []struct {
							ID            string  `json:"id"`
							LibraryItemID string  `json:"libraryItemId"`
							UserID        string  `json:"userId"`
							IsFinished    bool    `json:"isFinished"`
							Progress      float64 `json:"progress"`
							CurrentTime   float64 `json:"currentTime"`
							Duration      float64 `json:"duration"`
							StartedAt     int64   `json:"startedAt"`
							FinishedAt    int64   `json:"finishedAt"`
							LastUpdate    int64   `json:"lastUpdate"`
							TimeListening float64 `json:"timeListening"`
						}{
							{
								ID:            "progress1",
								LibraryItemID: "item1",
								UserID:        "user1",
								IsFinished:    false,
								Progress:      0.5,
								CurrentTime:   1800,
								Duration:      3600,
								StartedAt:     now.Add(-time.Hour).Unix(),
								LastUpdate:    now.Unix(),
								TimeListening: 1800,
							},
						},
						ListeningSessions: []struct {
							ID            string `json:"id"`
							UserID        string `json:"userId"`
							LibraryItemID string `json:"libraryItemId"`
							MediaType     string `json:"mediaType"`
							MediaMetadata struct {
								Title  string `json:"title"`
								Author string `json:"author"`
							} `json:"mediaMetadata"`
							Duration    float64 `json:"duration"`
							CurrentTime float64 `json:"currentTime"`
							Progress    float64 `json:"progress"`
							IsFinished  bool    `json:"isFinished"`
							StartedAt   int64   `json:"startedAt"`
							UpdatedAt   int64   `json:"updatedAt"`
						}{
							{
								ID:            "session1",
								UserID:        "user1",
								LibraryItemID: "item1",
								MediaType:     "book",
								MediaMetadata: struct {
									Title  string `json:"title"`
									Author string `json:"author"`
								}{
									Title:  "Test Book",
									Author: "Test Author",
								},
								Duration:    3600,
								CurrentTime: 1800,
								Progress:    0.5,
								IsFinished:  false,
								StartedAt:   now.Add(-time.Hour).Unix(),
								UpdatedAt:   now.Unix(),
							},
						},
					}

					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(progress); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				})
				return httptest.NewServer(handler)
			},
			expectError:   false,
			expectedItems: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			progress, err := client.GetUserProgress(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, progress)
				assert.Len(t, progress.MediaProgress, tt.expectedItems)
				assert.Len(t, progress.ListeningSessions, tt.expectedItems)
			}
		})
	}
}

func TestGetListeningSessions(t *testing.T) {
	tests := []struct {
		name          string
		setupServer   func() *httptest.Server
		since         time.Time
		expectError   bool
		expectedItems int
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/me/listening-sessions", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					sessions := []models.AudiobookshelfBook{
						{
							ID: "book1",
							Media: struct {
								ID          string                              `json:"id"`
								Metadata    models.AudiobookshelfMetadataStruct `json:"metadata"`
								CoverPath   string                              `json:"coverPath"`
								Duration    float64                             `json:"duration"`
								NumTracks   int                                 `json:"numTracks"`
								AudioFiles  []models.AudiobookshelfAudioFile    `json:"audioFiles,omitempty"`
								EbookFile   *json.RawMessage                    `json:"ebookFile"`
								EbookFormat string                              `json:"ebookFormat"`
							}{
								Metadata: models.AudiobookshelfMetadataStruct{
									Title: "Test Book",
								},
							},
						},
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(sessions); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				})
				return httptest.NewServer(handler)
			},
			since:         time.Now().Add(-24 * time.Hour),
			expectError:   false,
			expectedItems: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			sessions, err := client.GetListeningSessions(context.Background(), tt.since)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, sessions, tt.expectedItems)
			}
		})
	}
}
