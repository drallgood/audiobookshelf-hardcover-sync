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
	// imageContentType is the Content-Type the image host answers with; it
	// defaults to image/jpeg. omitImageContentType sends no Content-Type at all.
	imageContentType     string
	omitImageContentType bool
	// uploadedFilename is the file name of the multipart upload to the storage host.
	uploadedFilename string
	// authByHost records the Authorization header each peer was sent.
	authByHost map[string]string
	// requests counts the requests each peer received.
	requests map[string]int
	// imageBody is what the image host serves; it defaults to a minimal JPEG.
	// imageStream, when set, is served instead with an unknown length, and
	// imageContentLength, when non-zero, is the length the response announces.
	imageBody          []byte
	imageStream        io.Reader
	imageContentLength int64
}

// Minimal magic-byte bodies: enough for http.DetectContentType to classify them.
var (
	jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00")
	webpBytes = []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
	htmlBytes = []byte("<html><body>not an image</body></html>")
)

// countingReader reports how many bytes were read from it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// endlessJPEG is a JPEG header followed by zero bytes forever.
func endlessJPEG() io.Reader {
	return io.MultiReader(bytes.NewReader(jpegBytes), zeroReader{})
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func (rt *coverFlowTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reply := func(status int, body io.Reader) (*http.Response, error) {
		header := http.Header{"Content-Type": []string{"image/jpeg"}}
		if req.URL.Hostname() == "covers.example.test" {
			if rt.imageContentType != "" {
				header.Set("Content-Type", rt.imageContentType)
			}
			if rt.omitImageContentType {
				header.Del("Content-Type")
			}
		}
		return &http.Response{
			StatusCode: status,
			Header:     header,
			Body:       io.NopCloser(body),
			Request:    req,
			// Not set means "unknown" (-1), like a chunked response.
			ContentLength: -1,
		}, nil
	}
	host := req.URL.Hostname()
	if rt.authByHost == nil {
		rt.authByHost = map[string]string{}
		rt.requests = map[string]int{}
	}
	rt.authByHost[host] = req.Header.Get("Authorization")
	rt.requests[host]++
	if rt.failHosts[host] {
		return reply(http.StatusInternalServerError, strings.NewReader("boom"))
	}
	switch host {
	case "covers.example.test":
		var body io.Reader = rt.imageStream
		if body == nil {
			data := rt.imageBody
			if data == nil {
				data = jpegBytes
			}
			body = bytes.NewReader(data)
		}
		resp, err := reply(http.StatusOK, body)
		if err == nil && rt.imageContentLength != 0 {
			resp.ContentLength = rt.imageContentLength
		}
		return resp, err
	case "hardcover.app":
		return reply(http.StatusOK, strings.NewReader(`{"url":"https://storage.example.test/upload","fields":{"key":"editions/789/cover.jpg"}}`))
	case "storage.example.test":
		if reader, err := req.MultipartReader(); err == nil {
			for part, err := reader.NextPart(); err == nil; part, err = reader.NextPart() {
				if part.FormName() == "file" {
					rt.uploadedFilename = part.FileName()
				}
			}
		}
		return reply(http.StatusNoContent, strings.NewReader(""))
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
			creator.EnableCoverUpload()

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

func TestCreateEditionCoverRequestAuthorization(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	client := &coverFlowClient{}
	require.NotEmpty(t, client.GetAuthHeader(), "the assertion below would pass vacuously")
	transport := &coverFlowTransport{}
	creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "abs-secret",
		&http.Client{Transport: transport})
	creator.EnableCoverUpload()

	result, err := creator.CreateEdition(context.Background(), &edition.EditionInput{
		BookID: 123, Title: "T", AuthorIDs: []int{1}, ImageURL: "https://covers.example.test/cover.jpg",
	})

	require.NoError(t, err)
	require.Empty(t, result.ImageError)
	require.Equal(t, client.GetAuthHeader(), transport.authByHost["hardcover.app"],
		"the upload-credentials request must carry the Hardcover Authorization header")
	require.Empty(t, transport.authByHost["covers.example.test"], "the Audiobookshelf token must not go to a non-Audiobookshelf image host")
	require.Empty(t, transport.authByHost["storage.example.test"], "the storage upload must carry no Authorization header")
}

func TestCreateEditionCoverFileExtensionFollowsTheImageBytes(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	tests := []struct {
		name        string
		body        []byte
		contentType string
		omit        bool
		wantExt     string
	}{
		{name: "jpeg bytes and header", body: jpegBytes, contentType: "image/jpeg", wantExt: ".jpg"},
		{name: "png bytes and header", body: pngBytes, contentType: "image/png", wantExt: ".png"},
		{name: "png header on jpeg bytes", body: jpegBytes, contentType: "image/png", wantExt: ".jpg"},
		{name: "jpeg header on png bytes", body: pngBytes, contentType: "image/jpeg", wantExt: ".png"},
		{name: "png bytes without a content type", body: pngBytes, omit: true, wantExt: ".png"},
		{name: "png bytes with an octet-stream content type", body: pngBytes, contentType: "application/octet-stream", wantExt: ".png"},
		{name: "jpeg bytes with an octet-stream content type", body: jpegBytes, contentType: "application/octet-stream", wantExt: ".jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &coverFlowTransport{imageBody: tt.body, imageContentType: tt.contentType, omitImageContentType: tt.omit}
			creator := edition.NewCreatorWithHTTPClient(&coverFlowClient{}, logger.Get(), false, "",
				&http.Client{Transport: transport})
			creator.EnableCoverUpload()

			result, err := creator.CreateEdition(context.Background(), &edition.EditionInput{
				BookID: 123, Title: "T", AuthorIDs: []int{1}, ImageURL: "https://covers.example.test/cover",
			})

			require.NoError(t, err)
			require.Empty(t, result.ImageError)
			require.True(t, strings.HasPrefix(transport.uploadedFilename, "cover-"), transport.uploadedFilename)
			require.True(t, strings.HasSuffix(transport.uploadedFilename, tt.wantExt), transport.uploadedFilename)
		})
	}
}

const (
	coverFormatLabel = "cover image format not supported (Hardcover accepts PNG and JPEG)"
	coverSizeLabel   = "cover image is larger than 15 MB"
	maxCoverBytes    = 15 << 20
)

// requireCoverRejected asserts the edition was still created without a cover,
// that ImageError is exactly the fixed label with no remote detail, and that no
// upload-credentials or storage request was made.
func requireCoverRejected(t *testing.T, transport *coverFlowTransport, run func() (*edition.EditionResult, error), wantLabel string) {
	t.Helper()
	result, err := run()
	require.NoError(t, err, "a rejected cover must not fail the edition")
	require.True(t, result.Success)
	require.Equal(t, 789, result.EditionID)
	require.Zero(t, result.ImageID)
	require.Equal(t, wantLabel, result.ImageError)
	for _, leak := range []string{"example.test", "hardcover-token", "abs-secret", "http", "html", "RIFF", "GIF"} {
		require.NotContains(t, result.ImageError, leak, "ImageError must not carry remote details")
	}
	require.Zero(t, transport.requests["hardcover.app"], "no upload credentials may be requested for a rejected cover")
	require.Zero(t, transport.requests["storage.example.test"], "nothing may be uploaded for a rejected cover")
}

func TestCreateEditionRejectsACoverThatIsNotPNGOrJPEG(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	tests := []struct {
		name string
		body []byte
	}{
		{name: "webp", body: webpBytes},
		{name: "gif", body: gifBytes},
		{name: "html served as image/jpeg", body: htmlBytes},
		{name: "empty body", body: []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &coverFlowTransport{imageBody: tt.body, imageContentType: "image/jpeg"}
			creator := edition.NewCreatorWithHTTPClient(&coverFlowClient{}, logger.Get(), false, "abs-secret",
				&http.Client{Transport: transport})
			creator.EnableCoverUpload()

			requireCoverRejected(t, transport, func() (*edition.EditionResult, error) {
				return creator.CreateEdition(context.Background(), &edition.EditionInput{
					BookID: 123, Title: "T", AuthorIDs: []int{1}, ImageURL: "https://covers.example.test/cover.jpg",
				})
			}, coverFormatLabel)
		})
	}
}

func TestCreateEditionCoverSizeLimit(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	create := func(transport *coverFlowTransport) func() (*edition.EditionResult, error) {
		creator := edition.NewCreatorWithHTTPClient(&coverFlowClient{}, logger.Get(), false, "abs-secret",
			&http.Client{Transport: transport})
		creator.EnableCoverUpload()
		return func() (*edition.EditionResult, error) {
			return creator.CreateEdition(context.Background(), &edition.EditionInput{
				BookID: 123, Title: "T", AuthorIDs: []int{1}, ImageURL: "https://covers.example.test/cover.jpg",
			})
		}
	}

	t.Run("announced length over the limit is refused without reading the body", func(t *testing.T) {
		body := &countingReader{r: endlessJPEG()}
		transport := &coverFlowTransport{imageStream: body, imageContentLength: maxCoverBytes + 1}
		requireCoverRejected(t, transport, create(transport), coverSizeLabel)
		require.Zero(t, body.n, "the body must not be read when the announced size is too large")
	})

	t.Run("unannounced length over the limit stops reading just past the limit", func(t *testing.T) {
		body := &countingReader{r: endlessJPEG()}
		transport := &coverFlowTransport{imageStream: body}
		requireCoverRejected(t, transport, create(transport), coverSizeLabel)
		require.LessOrEqual(t, body.n, int64(maxCoverBytes+1), "an endless body must not be read past the limit")
	})

	t.Run("exactly 15 MiB is accepted", func(t *testing.T) {
		body := make([]byte, maxCoverBytes)
		copy(body, jpegBytes)
		transport := &coverFlowTransport{imageBody: body, imageContentLength: maxCoverBytes}
		result, err := create(transport)()
		require.NoError(t, err)
		require.Empty(t, result.ImageError)
		require.Equal(t, 55, result.ImageID)
		require.True(t, strings.HasSuffix(transport.uploadedFilename, ".jpg"), transport.uploadedFilename)
	})
}

func TestCreateEditionDoesNotAttemptACoverWhileUploadIsOff(t *testing.T) {
	client := &coverFlowClient{}
	transport := &coverFlowTransport{}
	// The creator is not passed EnableCoverUpload, as in every production caller.
	creator := edition.NewCreatorWithHTTPClient(client, logger.Get(), false, "abs-secret",
		&http.Client{Transport: transport})

	result, err := creator.CreateEdition(context.Background(), &edition.EditionInput{
		BookID: 123, Title: "T", AuthorIDs: []int{1}, ImageURL: "https://covers.example.test/cover.jpg",
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
