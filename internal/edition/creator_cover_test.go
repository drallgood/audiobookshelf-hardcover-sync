package edition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// coverFlowClient is a Hardcover client fake for the cover upload flow. It
// embeds the interface so only the methods this flow uses need implementing.
type coverFlowClient struct {
	edition.HardcoverClient

	failImageRecord bool
	failAttach      bool
}

func (c *coverFlowClient) GetAuthHeader() string { return "Bearer hardcover-token" }

func (c *coverFlowClient) GraphQLMutation(_ context.Context, mutation string, _ map[string]interface{}, result interface{}) error {
	respond := func(payload string) error { return json.Unmarshal([]byte(payload), result) }
	switch {
	case strings.Contains(mutation, "insert_edition"):
		return respond(`{"insert_edition":{"id":789,"errors":[]}}`)
	case strings.Contains(mutation, "insert_image"):
		if c.failImageRecord {
			return errors.New("image record rejected")
		}
		return respond(`{"insert_image":{"id":55}}`)
	case strings.Contains(mutation, "update_edition"):
		if c.failAttach {
			return errors.New("attach rejected")
		}
		return respond(`{"update_edition":{"id":789,"errors":[]}}`)
	}
	return errors.New("unexpected mutation")
}

// coverFlowTransport fakes the three HTTP peers of a cover upload: the image
// host, Hardcover's upload-credential endpoint, and the storage bucket. A peer
// listed in failHosts answers 500; nothing reaches the real network.
type coverFlowTransport struct {
	failHosts map[string]bool
}

func (rt *coverFlowTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reply := func(status int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Request:    req,
		}, nil
	}
	host := req.URL.Hostname()
	if rt.failHosts[host] {
		return reply(http.StatusInternalServerError, "boom")
	}
	switch host {
	case "covers.example.test":
		return reply(http.StatusOK, "image-bytes")
	case "hardcover.app":
		return reply(http.StatusOK, `{"url":"https://storage.example.test/upload","fields":{"key":"editions/789/cover.jpg"}}`)
	case "storage.example.test":
		return reply(http.StatusNoContent, "")
	}
	return nil, errors.New("unexpected host " + host)
}

func TestCreateEditionReportsAFailedCoverWithoutFailingTheEdition(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	tests := []struct {
		name          string
		imageURL      string
		failHosts     map[string]bool
		failImageRec  bool
		failAttach    bool
		wantImageID   int
		wantImageFail bool
	}{
		{name: "cover attached", imageURL: "https://covers.example.test/cover.jpg", wantImageID: 55},
		{name: "no cover requested", imageURL: ""},
		{name: "cover download fails", imageURL: "https://covers.example.test/cover.jpg", failHosts: map[string]bool{"covers.example.test": true}, wantImageFail: true},
		{name: "upload credentials fail", imageURL: "https://covers.example.test/cover.jpg", failHosts: map[string]bool{"hardcover.app": true}, wantImageFail: true},
		{name: "storage upload fails", imageURL: "https://covers.example.test/cover.jpg", failHosts: map[string]bool{"storage.example.test": true}, wantImageFail: true},
		{name: "image record fails", imageURL: "https://covers.example.test/cover.jpg", failImageRec: true, wantImageFail: true},
		{name: "attaching the image fails after it exists", imageURL: "https://covers.example.test/cover.jpg", failAttach: true, wantImageID: 55, wantImageFail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &coverFlowClient{failImageRecord: tt.failImageRec, failAttach: tt.failAttach}
			creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "",
				&http.Client{Transport: &coverFlowTransport{failHosts: tt.failHosts}})

			result, err := creator.CreateEdition(context.Background(), &edition.EditionInput{
				BookID: 123, Title: "T", AuthorIDs: []int{1}, ImageURL: tt.imageURL,
			})

			require.NoError(t, err, "a cover problem must not fail the edition")
			require.True(t, result.Success)
			require.Equal(t, 789, result.EditionID)
			require.Equal(t, tt.wantImageID, result.ImageID)
			if tt.wantImageFail {
				require.NotEmpty(t, result.ImageError)
				for _, leak := range []string{"boom", "rejected", "example.test", "hardcover-token", "http"} {
					require.NotContains(t, result.ImageError, leak, "ImageError must not carry remote details")
				}
			} else {
				require.Empty(t, result.ImageError)
			}
		})
	}
}
