package edition_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/require"
)

// Only edition insertion is supported while the undocumented upload flow is off.
type coverFlowClient struct{ edition.HardcoverClient }

func (c *coverFlowClient) GraphQLMutation(_ context.Context, mutation string, _ map[string]interface{}, result interface{}) error {
	if !strings.Contains(mutation, "insert_edition") {
		return errors.New("unexpected mutation")
	}
	return json.Unmarshal([]byte(`{"insert_edition":{"id":789,"errors":[]}}`), result)
}

// Any HTTP request violates the disabled-upload contract.
type coverFlowTransport struct{ requests []string }

func (rt *coverFlowTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.requests = append(rt.requests, req.URL.String())
	return nil, errors.New("unexpected cover request")
}

func TestCreateEditionDoesNotAttemptACoverWhileUploadIsOff(t *testing.T) {
	client := &coverFlowClient{}
	transport := &coverFlowTransport{}
	// The creator is not passed EnableCoverUpload, as in every production caller.
	creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "abs-secret",
		&http.Client{Transport: transport})

	result, err := creator.CreateEdition(context.Background(), &edition.EditionInput{
		ReadingFormat: "ebook",
		BookID:        123, Title: "T", AuthorIDs: []int{1}, ImageURL: "https://covers.example.test/cover.jpg",
	})

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 789, result.EditionID, "the edition is still created")
	require.Zero(t, result.ImageID)
	require.NotEmpty(t, result.ImageError, "a requested cover is reported as not uploaded")
	require.Empty(t, transport.requests, "no request may reach the image host, Hardcover's upload endpoint or storage")
}

func TestUploadEditionImageIsRefusedWhileUploadIsOff(t *testing.T) {
	transport := &coverFlowTransport{}
	creator := edition.NewCreatorWithHTTPClient(&coverFlowClient{}, logger.Get(), false, "abs-secret",
		&http.Client{Transport: transport})

	err := creator.UploadEditionImage(context.Background(), 789, "https://covers.example.test/cover.jpg", "")

	require.ErrorIs(t, err, edition.ErrCoverUploadDisabled)
	require.Empty(t, transport.requests, "no request may be made")
}
