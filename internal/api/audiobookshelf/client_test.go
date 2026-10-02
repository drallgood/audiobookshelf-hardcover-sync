package audiobookshelf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t testing.TB, baseURL, token string) *Client {
	t.Helper()
	client, err := NewClientWithNetworkTrust(baseURL, token, NetworkTrustAllowPrivate)
	require.NoError(t, err)
	return client
}

func TestGetLibraries(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       []AudiobookshelfLibrary
		wantErr    bool
	}{
		{
			name:       "successful response",
			statusCode: http.StatusOK,
			body:       `{"libraries":[{"id":"1","name":"Library 1"},{"id":"2","name":"Library 2"}]}`,
			want: []AudiobookshelfLibrary{
				{ID: "1", Name: "Library 1"},
				{ID: "2", Name: "Library 2"},
			},
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			body:       `{}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/libraries", r.URL.Path)
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := newTestClient(t, server.URL, "test-token")
			libraries, err := client.GetLibraries(context.Background())

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, libraries)
		})
	}
}

func TestGetLibraryItems(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/libraries/1/items", r.URL.Path)
		assert.Equal(t, "progress", r.URL.Query().Get("include"))
		assert.Equal(t, "0", r.URL.Query().Get("minified"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"id":"book1","media":{"metadata":{"title":"Test Book","authorName":"Test Author"}}}],"total":1}`))
	}))
	defer server.Close()

	items, err := newTestClient(t, server.URL, "test-token").GetLibraryItems(context.Background(), "1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "book1", items[0].ID)
	assert.Equal(t, "Test Book", items[0].Media.Metadata.Title)
	assert.Equal(t, "Test Author", items[0].Media.Metadata.AuthorName)
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

	client := newTestClient(t, server.URL, "test-token")
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
	ebook, err := newTestClient(t, ebookServer.URL, "test-token").GetLibraryItemByID(context.Background(), "li_ebook")
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

			item, err := newTestClient(t, server.URL, "test-token").GetLibraryItemByID(context.Background(), tt.itemID)
			require.Nil(t, item)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("empty item ID", func(t *testing.T) {
		item, err := newTestClient(t, "http://127.0.0.1", "test-token").GetLibraryItemByID(context.Background(), " \t ")
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
	client := newTestClient(t, server.URL, "test-token")
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/me", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"user1","username":"testuser","mediaProgress":[{"id":"progress1","libraryItemId":"item1","userId":"user1","isFinished":false,"progress":0.5,"currentTime":1800,"duration":3600,"startedAt":1699996400,"lastUpdate":1700000000,"timeListening":1800}],"listeningSessions":[{"id":"session1","userId":"user1","libraryItemId":"item1","mediaType":"book","mediaMetadata":{"title":"Test Book","author":"Test Author"},"duration":3600,"currentTime":1800,"progress":0.5,"isFinished":false,"startedAt":1699996400,"updatedAt":1700000000}]}`))
	}))
	defer server.Close()

	progress, err := newTestClient(t, server.URL, "test-token").GetUserProgress(context.Background())
	require.NoError(t, err)
	require.NotNil(t, progress)
	assert.Equal(t, "user1", progress.ID)
	require.Len(t, progress.MediaProgress, 1)
	assert.Equal(t, "progress1", progress.MediaProgress[0].ID)
	assert.Equal(t, 0.5, progress.MediaProgress[0].Progress)
	require.Len(t, progress.ListeningSessions, 1)
	assert.Equal(t, "session1", progress.ListeningSessions[0].ID)
	assert.Equal(t, "Test Book", progress.ListeningSessions[0].MediaMetadata.Title)
}

func TestGetListeningSessions(t *testing.T) {
	since := time.Unix(1_700_000_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/me/listening-sessions", r.URL.Path)
		assert.Equal(t, "1700000000000", r.URL.Query().Get("since"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"book1","media":{"metadata":{"title":"Test Book"}}}]`))
	}))
	defer server.Close()

	sessions, err := newTestClient(t, server.URL, "test-token").GetListeningSessions(context.Background(), since)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "book1", sessions[0].ID)
	assert.Equal(t, "Test Book", sessions[0].Media.Metadata.Title)
}
