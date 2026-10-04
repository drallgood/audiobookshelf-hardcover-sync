package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	absync "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

const (
	boundaryBookID    = 21
	boundaryEditionID = 900
	boundaryASIN      = "B0ABCDE123"
	boundaryRegional  = boundaryASIN + ":uk"
	boundaryABSItem   = "li_boundary"
	boundaryABSItemJS = `{"id":"li_boundary","mediaType":"book","media":{"metadata":{"asin":"B0ABCDE123","isbn":"9780306406157"},"duration":100}}`
)

// fakeHardcover serves the GraphQL operations the edition command sends and
// records which catalogue mutations reached it.
type fakeHardcover struct {
	t *testing.T

	upsertResponse   string
	upsertHTTPStatus int
	insertResponse   string
	insertHTTPStatus int
	importStatus     string
	readbackBook     int
	readbackFormat   int
	readbackEdition  int
	bookResponse     string
	missingEditionID int
	// emptyLookups answers unrecognized read queries, such as ebook duplicate
	// lookups, with no matches.
	emptyLookups bool
	// onPoll runs when the regional import status is polled.
	onPoll func()

	mu                  sync.Mutex
	upsertCalls         int
	insertEditions      int
	upsertExternalID    string
	upsertBookIDPresent bool
}

func newFakeHardcover(t *testing.T) *fakeHardcover {
	return &fakeHardcover{
		t:               t,
		upsertResponse:  fmt.Sprintf(`{"data":{"upsert_book":{"id":77,"status":"fetching","book":{"id":%d},"edition":{"id":%d,"book_id":%d,"reading_format_id":2},"edition_id":%d,"errors":[]}}}`, boundaryBookID, boundaryEditionID, boundaryBookID, boundaryEditionID),
		importStatus:    "created",
		readbackBook:    boundaryBookID,
		readbackFormat:  2,
		readbackEdition: boundaryEditionID,
	}
}

func (f *fakeHardcover) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		f.t.Errorf("decode Hardcover request: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.Contains(request.Query, "insert_edition"):
		f.insertEditions++
		if f.insertResponse == "" && f.insertHTTPStatus == 0 {
			http.Error(w, "unexpected insert_edition", http.StatusBadRequest)
			return
		}
		writeFakeResponse(w, f.insertHTTPStatus, f.insertResponse)
	case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
		f.upsertCalls++
		if book, ok := request.Variables["book"].(map[string]interface{}); ok {
			f.upsertExternalID, _ = book["external_id"].(string)
			_, f.upsertBookIDPresent = book["book_id"]
		}
		writeFakeResponse(w, f.upsertHTTPStatus, f.upsertResponse)
	case strings.Contains(request.Query, "RegionalAudibleImport"):
		if f.onPoll != nil {
			f.onPoll()
		}
		statuses := []map[string]interface{}{{
			"status": f.importStatus, "book_id": boundaryBookID, "edition_id": boundaryEditionID,
			"external_id": boundaryRegional, "platform_id": 32,
		}}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
			"book_import_statuses": statuses,
			"book_mappings":        []interface{}{},
		}})
	case strings.Contains(request.Query, "GetEdition"):
		if editionID, ok := request.Variables["editionId"].(float64); ok && int(editionID) == f.missingEditionID {
			_, _ = w.Write([]byte(`{"data":{"editions":[]}}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":{"editions":[{"id":%d,"book_id":%d,"reading_format_id":%d}]}}`, f.readbackEdition, f.readbackBook, f.readbackFormat)
	case strings.Contains(request.Query, "GetBook"):
		if f.bookResponse != "" {
			_, _ = w.Write([]byte(f.bookResponse))
		} else {
			_, _ = fmt.Fprintf(w, `{"data":{"book":{"id":%d,"title":"Prepopulated","asin":%q,"isbn13":"9780306406157","authors":[{"id":3,"name":"Author"}],"narrators":[{"id":4,"name":"Narrator"}]}}}`, boundaryBookID, boundaryASIN)
		}
	case f.emptyLookups && !strings.HasPrefix(strings.TrimSpace(request.Query), "mutation"):
		_, _ = w.Write([]byte(`{"data":{"books":[],"editions":[],"book_mappings":[]}}`))
	default:
		f.t.Errorf("unexpected Hardcover query: %s", request.Query)
		http.Error(w, "unexpected query", http.StatusBadRequest)
	}
}

func writeFakeResponse(w http.ResponseWriter, status int, body string) {
	if status != 0 && status != http.StatusOK {
		if body != "" {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		http.Error(w, "Hardcover unavailable", status)
		return
	}
	_, _ = w.Write([]byte(body))
}

func (f *fakeHardcover) counts() (upserts, inserts int, externalID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upsertCalls, f.insertEditions, f.upsertExternalID
}

func (f *fakeHardcover) upsertIncludesBookID() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upsertBookIDPresent
}

// commandEnv is a config file pointing the command at fake Hardcover and
// Audiobookshelf servers, with an isolated state file.
type commandEnv struct {
	configPath string
	statePath  string
	hardcover  *fakeHardcover
}

type gatedConfirmationReader struct {
	started     chan struct{}
	release     chan struct{}
	response    string
	startOnce   sync.Once
	releaseOnce sync.Once
}

func (r *gatedConfirmationReader) Read(p []byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.release
	return copy(p, r.response), nil
}

func (r *gatedConfirmationReader) continuePrompt() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func newGatedConfirmationReader(response string) *gatedConfirmationReader {
	return &gatedConfirmationReader{
		started: make(chan struct{}), release: make(chan struct{}), response: response,
	}
}

func unanchoredPromptServices(item *models.AudiobookshelfBook, importCalls *int) createServices {
	return createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) { return item, nil },
		getAudibleBook: func(context.Context, string, string) (*audnex.Book, error) {
			return &audnex.Book{ASIN: boundaryASIN, Title: "Boundary Book"}, nil
		},
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			(*importCalls)++
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookCreated, BookID: boundaryBookID, EditionID: boundaryEditionID,
				ReadingFormatID: models.ReadingFormatID(models.ReadingFormatAudiobook), RegionalExternalID: boundaryRegional,
			}, nil
		},
	}
}

func newCommandEnv(t *testing.T, hc *fakeHardcover, absItemJSON string) commandEnv {
	t.Helper()
	logger.Setup(logger.Config{Level: "error", Output: &bytes.Buffer{}})
	hcServer := httptest.NewServer(hc)
	t.Cleanup(hcServer.Close)
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if absItemJSON == "" || r.URL.Path != "/api/items/"+boundaryABSItem {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(absItemJSON))
	}))
	t.Cleanup(absServer.Close)

	dir := t.TempDir()
	env := commandEnv{
		configPath: filepath.Join(dir, "config.yaml"),
		statePath:  filepath.Join(dir, "sync_state.json"),
		hardcover:  hc,
	}
	configYAML := fmt.Sprintf(`hardcover:
  token: test-token
  base_url: %s
audiobookshelf:
  url: %s
  token: abs-token
sync:
  state_file: %s
rate_limit:
  rate: 1ms
  max_concurrent: 1
`, hcServer.URL, absServer.URL, env.statePath)
	if err := os.WriteFile(env.configPath, []byte(configYAML), 0600); err != nil {
		t.Fatal(err)
	}
	return env
}

func (env commandEnv) run(t *testing.T, args ...string) (map[string]interface{}, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := newApp()
	app.Writer = &stdout
	app.ErrWriter = &stderr
	err := app.Run(append([]string{"edition", "--config", env.configPath}, args...))
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("command output is not JSON: %v\n%s", decodeErr, stdout.String())
	}
	return result, nil
}

func (env commandEnv) association(t *testing.T) (state.Association, bool) {
	t.Helper()
	if _, err := os.Stat(env.statePath); os.IsNotExist(err) {
		return state.Association{}, false
	}
	loaded, err := state.LoadState(env.statePath)
	if err != nil {
		t.Fatal(err)
	}
	return loaded.GetAssociation(boundaryABSItem)
}

func (env commandEnv) createServices(t *testing.T, dryRun bool, audible *audnex.Book, region string) createServices {
	t.Helper()
	cfg, err := loadEditionConfig(env.configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	services, err := newCreateServices(cfg, logger.Get(), dryRun)
	if err != nil {
		t.Fatal(err)
	}
	services.getAudibleBook = func(_ context.Context, asin, requestedRegion string) (*audnex.Book, error) {
		if audible == nil {
			return nil, audnex.ErrNotFound
		}
		if asin != audible.ASIN || requestedRegion != region {
			t.Fatalf("unexpected exact-region Audnexus lookup: %q:%q", asin, requestedRegion)
		}
		return audible, nil
	}
	services.discoverAudibleBook = func(_ context.Context, asin, preferred string) (*audnex.Book, string, error) {
		if audible == nil {
			return nil, "", nil
		}
		if asin != audible.ASIN {
			t.Fatalf("unexpected Audnexus discovery ASIN %q", asin)
		}
		if preferred != "" && preferred != region {
			t.Fatalf("preferred Audnexus region %q, expected %q", preferred, region)
		}
		return audible, region, nil
	}
	return services
}

func TestCreateCommandImportsAudiobookAndSavesAssociation(t *testing.T) {
	for _, status := range []string{"created", "loaded"} {
		t.Run(status, func(t *testing.T) {
			hc := newFakeHardcover(t)
			hc.importStatus = status
			env := newCommandEnv(t, hc, boundaryABSItemJS)
			// A mismatch export carries informational fields the import ignores.
			inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"title":"Export title","asin":%q,"isbn_13":"9780306406157","author_ids":[3],"asin_region":"uk"}`,
				boundaryBookID, boundaryABSItem, boundaryASIN))

			result, err := env.run(t, "create", "--input", inputPath)
			if err != nil {
				t.Fatal(err)
			}
			if result["status"] != status || result["association_saved"] != true || result["edition_id"] != float64(boundaryEditionID) {
				t.Fatalf("unexpected command result: %#v", result)
			}
			upserts, inserts, externalID := hc.counts()
			if upserts != 1 || inserts != 0 || externalID != boundaryRegional {
				t.Fatalf("expected one regional upsert_book and no insert_edition, got upserts=%d inserts=%d external_id=%q", upserts, inserts, externalID)
			}
			association, ok := env.association(t)
			if !ok {
				t.Fatal("verified association was not saved to the configured state file")
			}
			if association.HardcoverBookID != "21" || association.HardcoverEditionID != "900" || association.RegionalExternalID != boundaryRegional || association.ReadingFormat != models.ReadingFormatAudiobook {
				t.Fatalf("unexpected saved association: %#v", association)
			}
		})
	}
}

func TestRunCreateImportsASINOnlyAudiobookAfterAudnexusConfirmation(t *testing.T) {
	hc := newFakeHardcover(t)
	absItem := `{"id":"li_boundary","mediaType":"book","media":{"metadata":{"title":"Boundary Book","subtitle":"ABS subtitle","authorName":"Test Author","narratorName":"Reader One","series":[{"name":"The Saga","sequence":"1"}],"publishedDate":"2024-01-02","publisher":"Test Press","asin":"B0ABCDE123","isbn":"9780306406157","language":"en"},"duration":100,"coverPath":"/api/items/li_boundary/cover"}}`
	env := newCommandEnv(t, hc, absItem)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"abs_item_id":%q,"reading_format":"audiobook"}`, boundaryASIN, boundaryABSItem))
	audible := &audnex.Book{
		ASIN: boundaryASIN, Title: "Boundary Book", Subtitle: "Audible subtitle",
		Authors: []interface{}{"Test Author"}, Narrators: []interface{}{"Reader One"},
		SeriesPrimary: &audnex.Series{Name: "The Saga", Position: "1"},
		PublisherName: "Test Press", ReleaseDate: "2024", RuntimeLengthMin: 100.0 / 60,
		Language: "en", Image: "https://images.example/book.jpg",
	}
	services := env.createServices(t, false, audible, "uk")
	var preview bytes.Buffer
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, StateFile: env.statePath, PreferredRegion: "uk",
		ConfirmAudnexus: true, PreviewWriter: &preview,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "created" || result.BookID != boundaryBookID || result.EditionID != boundaryEditionID || result.AudnexusRegion != "uk" {
		t.Fatalf("unexpected ASIN-only import result: %#v", result)
	}
	if result.AudnexusRecord == nil || result.AudnexusComparison == nil || result.AudnexusComparison.Title != edition.AudnexusMatch || result.AudnexusComparison.Subtitle != edition.AudnexusDiffers {
		t.Fatalf("Audnexus preview or comparison is incomplete: %#v", result)
	}
	if result.AudnexusRecord.CoverURL != "https://images.example/book.jpg" || result.AudnexusComparison.CoverURL != "" {
		t.Fatalf("Audnexus cover source should be retained without a comparison status: record=%#v comparison=%#v", result.AudnexusRecord, result.AudnexusComparison)
	}
	if result.AudnexusRecord.ReleaseDate != "2024" {
		t.Fatalf("year-only Audnexus release date lost its precision: %#v", result.AudnexusRecord)
	}
	if !strings.Contains(preview.String(), "Audnexus regional record") ||
		!strings.Contains(preview.String(), "Audiobookshelf item") ||
		!strings.Contains(preview.String(), "Title: match") ||
		!strings.Contains(preview.String(), "Subtitle: differs") ||
		!strings.Contains(preview.String(), `Release date: "2024"`) ||
		strings.Contains(preview.String(), `Release date: "2024-01-01"`) ||
		!strings.Contains(preview.String(), `Cover URL: "https://images.example/book.jpg"`) ||
		!strings.Contains(preview.String(), `Cover path: "/api/items/li_boundary/cover"`) ||
		strings.Contains(preview.String(), "Cover:") {
		t.Fatalf("CLI preview omitted the record or field comparison: %s", preview.String())
	}
	if upserts, inserts, externalID := hc.counts(); upserts != 1 || inserts != 0 || externalID != boundaryRegional {
		t.Fatalf("expected one regional upsert and no ebook insert, got upserts=%d inserts=%d external_id=%q", upserts, inserts, externalID)
	}
	if hc.upsertIncludesBookID() {
		t.Fatal("unanchored upsert request included book_id")
	}
	association, ok := env.association(t)
	if !ok || association.Provenance != "audible_import_unanchored" || association.RegionalExternalID != boundaryRegional || association.AudnexusConfirmedRegion != "uk" || association.AudnexusConfirmedAt.IsZero() || association.SourceASIN != boundaryASIN || association.SourceISBN13 != "9780306406157" {
		t.Fatalf("unanchored import association lacks confirmed provenance: %#v, exists=%t", association, ok)
	}
}

func TestRunCreateUnanchoredAssociationIsVisibleAsMultiuserEditionAddition(t *testing.T) {
	for _, sourceASIN := range []string{boundaryASIN, "B099999999"} {
		name := "source ASIN"
		if sourceASIN != boundaryASIN {
			name = "corrected source ASIN"
		}
		t.Run(name, func(t *testing.T) {
			hc := newFakeHardcover(t)
			itemJSON := strings.Replace(boundaryABSItemJS, `"B0ABCDE123"`, fmt.Sprintf("%q", sourceASIN), 1)
			env := newCommandEnv(t, hc, itemJSON)
			annotator, profileID, profileStatePath := newCLIAnnotationService(t, filepath.Dir(env.statePath))
			configData, err := os.ReadFile(env.configPath)
			if err != nil {
				t.Fatal(err)
			}
			configText := strings.Replace(string(configData), env.statePath, profileStatePath, 1)
			if err := os.WriteFile(env.configPath, []byte(configText), 0600); err != nil {
				t.Fatal(err)
			}
			env.statePath = profileStatePath

			inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"abs_item_id":%q,"reading_format":"audiobook"}`, boundaryASIN, boundaryABSItem))
			audible := &audnex.Book{ASIN: boundaryASIN, Title: "Boundary Book"}
			result, err := runCreate(context.Background(), createOptions{
				InputPath: inputPath, StateFile: env.statePath, PreferredRegion: "uk", ConfirmAudnexus: true,
				ConfirmIdentifierCorrection: true,
			}, env.createServices(t, false, audible, "uk"))
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || !result.AssociationSaved {
				t.Fatalf("expected the confirmed import association to be saved: %#v", result)
			}
			association, ok := env.association(t)
			if !ok {
				t.Fatal("confirmed import association was not persisted")
			}

			snapshot := absync.SyncSnapshot{
				State: "completed",
				BookOutcomes: []absync.BookOutcomeRecord{{
					BookID: boundaryABSItem, Outcome: absync.OutcomeNeedsReview,
					Reason: mismatch.ReasonAudibleImportAvailable, ASIN: sourceASIN, SourceASIN: sourceASIN,
					ISBN: "9780306406157", SourceISBN13: "9780306406157", Format: models.ReadingFormatAudiobook,
				}},
			}
			if err := annotator.AnnotateEditionAdditions(profileID, &snapshot); err != nil {
				t.Fatal(err)
			}
			if !snapshot.BookOutcomes[0].EditionAdded {
				t.Fatalf("multiuser annotation did not recognize the saved association: %#v", association)
			}
		})
	}
}

func newCLIAnnotationService(t *testing.T, dataDir string) (*multiuser.MultiUserService, string, string) {
	t.Helper()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "annotation-test.db"),
	}, logger.Get())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close annotation test database: %v", err)
		}
	})
	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	if err != nil {
		t.Fatal(err)
	}
	globalConfig := config.DefaultConfig()
	globalConfig.Paths.DataDir = dataDir
	service := multiuser.NewMultiUserService(database.NewRepository(db, encryptor, logger.Get()), globalConfig, logger.Get())
	profileID := "cli-create"
	if err := service.CreateProfile(profileID, "CLI create", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{StateFile: "sync_state.json"}); err != nil {
		t.Fatal(err)
	}
	return service, profileID, filepath.Join(dataDir, "sync_state."+profileID)
}

func TestRunCreateUnanchoredInteractiveDeclineDoesNotMutate(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, "")
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"region":"uk"}`, boundaryASIN))
	services := env.createServices(t, false, &audnex.Book{ASIN: boundaryASIN, Title: "Boundary Book"}, "uk")
	var preview bytes.Buffer
	_, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, ConfirmationReader: strings.NewReader("n\n"), PreviewWriter: &preview,
	}, services)
	if err == nil || !strings.Contains(err.Error(), "was not confirmed") {
		t.Fatalf("expected confirmation-decline error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
		t.Fatalf("declined confirmation reached Hardcover: upserts=%d inserts=%d", upserts, inserts)
	}
	if !strings.Contains(preview.String(), "Audnexus regional record") || !strings.Contains(preview.String(), "Type yes to confirm") {
		t.Fatalf("interactive command did not display its preview and confirmation prompt: %s", preview.String())
	}
}

func TestRunCreateUnanchoredInteractiveConfirmationImportsDisplayedRegion(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, "")
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q}`, boundaryASIN))
	services := env.createServices(t, false, &audnex.Book{ASIN: boundaryASIN, Title: "Boundary Book"}, "uk")
	var preview bytes.Buffer
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, PreferredRegion: "uk", ConfirmationReader: strings.NewReader("yes\n"),
		PreviewWriter: &preview,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "created" || result.AudnexusRegion != "uk" {
		t.Fatalf("interactive confirmation did not import the displayed region: %#v", result)
	}
	if upserts, inserts, externalID := hc.counts(); upserts != 1 || inserts != 0 || externalID != boundaryRegional {
		t.Fatalf("confirmed input did not reach the regional import once: upserts=%d inserts=%d external_id=%q", upserts, inserts, externalID)
	}
	if !strings.Contains(preview.String(), "Audnexus regional record") || !strings.Contains(preview.String(), "Type yes to confirm") {
		t.Fatalf("interactive confirmation did not show what it would import: %s", preview.String())
	}
}

func TestRunCreateUnanchoredAudnexusMissAndRateLimitDoNotMutate(t *testing.T) {
	tests := []struct {
		name      string
		lookupErr error
		wantError string
	}{
		{name: "regional miss", lookupErr: audnex.ErrNotFound, wantError: "did not find ASIN"},
		{name: "rate limited", lookupErr: audnex.ErrRateLimited, wantError: "rate limited"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := newFakeHardcover(t)
			inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"asin_region":"uk"}`, boundaryASIN))
			services := createServices{
				getAudibleBook: func(context.Context, string, string) (*audnex.Book, error) {
					return nil, tt.lookupErr
				},
				importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					t.Fatal("Audnexus lookup failure reached the import service")
					return nil, nil
				},
			}
			_, err := runCreate(context.Background(), createOptions{InputPath: inputPath, ConfirmAudnexus: true}, services)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected %q error, got %v", tt.wantError, err)
			}
			if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
				t.Fatalf("failed preview reached Hardcover: upserts=%d inserts=%d", upserts, inserts)
			}
		})
	}
}

func TestRunCreateUnanchoredDryRunPreviewsWithoutSavingOrMutating(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, boundaryABSItemJS)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"asin_region":"uk","abs_item_id":%q}`, boundaryASIN, boundaryABSItem))
	services := env.createServices(t, true, &audnex.Book{ASIN: boundaryASIN, Title: "Boundary Book"}, "uk")
	var preview bytes.Buffer
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, StateFile: env.statePath, DryRun: true, PreviewWriter: &preview,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "dry_run" || result.AssociationSaved || result.AudnexusRegion != "uk" {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	if !strings.Contains(preview.String(), "Audnexus regional record") || !strings.Contains(preview.String(), "Per-field comparison") {
		t.Fatalf("dry run did not show the comparison preview: %s", preview.String())
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
		t.Fatalf("dry run reached Hardcover: upserts=%d inserts=%d", upserts, inserts)
	}
	if _, ok := env.association(t); ok {
		t.Fatal("dry run saved an Audiobookshelf association")
	}
}

func TestRunCreateLeavesStateLockAvailableDuringAudnexusPrompt(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sync-state.json")
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"asin_region":"uk","abs_item_id":"item-1"}`, boundaryASIN))
	reader := newGatedConfirmationReader("n\n")
	var importCalls int
	services := unanchoredPromptServices(testAudiobook("item-1", boundaryASIN), &importCalls)
	done := make(chan error, 1)
	go func() {
		_, err := runCreate(context.Background(), createOptions{
			InputPath: inputPath, StateFile: statePath, ConfirmationReader: reader,
		}, services)
		done <- err
	}()
	waitForConfirmationPrompt(t, reader)
	lock, lockErr := state.AcquireFileLock(statePath)
	if lock != nil {
		_ = lock.Close()
	}
	reader.continuePrompt()
	runErr := <-done
	if lockErr != nil {
		t.Fatalf("state-file lock was held while waiting for user confirmation: %v", lockErr)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "was not confirmed") {
		t.Fatalf("expected decline after the prompt, got %v", runErr)
	}
	if importCalls != 0 {
		t.Fatalf("declined prompt reached the import service %d times", importCalls)
	}
}

func TestRunCreateRejectsAssociationSavedDuringAudnexusPrompt(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sync-state.json")
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"asin_region":"uk","abs_item_id":"item-1"}`, boundaryASIN))
	reader := newGatedConfirmationReader("yes\n")
	var importCalls int
	services := unanchoredPromptServices(testAudiobook("item-1", boundaryASIN), &importCalls)
	done := make(chan error, 1)
	go func() {
		_, err := runCreate(context.Background(), createOptions{
			InputPath: inputPath, StateFile: statePath, ConfirmationReader: reader,
		}, services)
		done <- err
	}()
	waitForConfirmationPrompt(t, reader)
	lock, lockErr := state.AcquireFileLock(statePath)
	if lockErr == nil {
		loaded, loadErr := state.LoadState(lock.StatePath())
		if loadErr == nil {
			loadErr = loaded.SetAssociation(state.Association{
				ABSItemID: "item-1", HardcoverBookID: "51", HardcoverEditionID: "52",
				ReadingFormat: models.ReadingFormatAudiobook, Provenance: "another-import",
			})
		}
		if loadErr == nil {
			loadErr = loaded.Save(lock.StatePath())
		}
		if closeErr := lock.Close(); loadErr == nil {
			loadErr = closeErr
		}
		if loadErr != nil {
			reader.continuePrompt()
			<-done
			t.Fatalf("save competing association during prompt: %v", loadErr)
		}
	}
	reader.continuePrompt()
	runErr := <-done
	if lockErr != nil {
		t.Fatalf("state-file lock was held while waiting to save a competing association: %v", lockErr)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "already has a confirmed Hardcover association") {
		t.Fatalf("expected post-prompt association recheck, got %v", runErr)
	}
	if importCalls != 0 {
		t.Fatalf("competing association was overwritten after %d import calls", importCalls)
	}
}

func TestRunCreateRejectsChangedABSSnapshotDuringAudnexusPrompt(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sync-state.json")
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":%q,"asin_region":"uk","abs_item_id":"item-1"}`, boundaryASIN))
	reader := newGatedConfirmationReader("yes\n")
	original := testAudiobook("item-1", boundaryASIN)
	changed := testAudiobook("item-1", "B099999999")
	fetchCalls := 0
	var importCalls int
	services := unanchoredPromptServices(original, &importCalls)
	services.fetchABSItem = func(context.Context, string) (*models.AudiobookshelfBook, error) {
		fetchCalls++
		if fetchCalls == 1 {
			return original, nil
		}
		return changed, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := runCreate(context.Background(), createOptions{
			InputPath: inputPath, StateFile: statePath, ConfirmationReader: reader,
		}, services)
		done <- err
	}()
	waitForConfirmationPrompt(t, reader)
	reader.continuePrompt()
	runErr := <-done
	if runErr == nil || !strings.Contains(runErr.Error(), "changed while awaiting Audnexus confirmation") {
		t.Fatalf("expected stale ABS source rejection, got %v", runErr)
	}
	if importCalls != 0 {
		t.Fatalf("changed ABS source reached the import service %d times", importCalls)
	}
}

func waitForConfirmationPrompt(t *testing.T, reader *gatedConfirmationReader) {
	t.Helper()
	select {
	case <-reader.started:
	case <-time.After(5 * time.Second):
		reader.continuePrompt()
		t.Fatal("create did not reach the interactive Audnexus confirmation")
	}
}

// syncAssociationHardcover avoids unrelated network operations after the sync
// service has loaded the state written by edition create. Its identifier
// lookup is the boundary signal: a valid saved association should bypass it.
type syncAssociationHardcover struct {
	*hardcover.Client
	identifierLookups int
}

func (c *syncAssociationHardcover) SearchBookByASINResult(context.Context, string) (*hardcover.ASINLookupResult, error) {
	c.identifierLookups++
	return nil, nil
}

func (c *syncAssociationHardcover) SearchBookByISBN10(context.Context, string) (*models.HardcoverBook, error) {
	c.identifierLookups++
	return nil, nil
}

func (c *syncAssociationHardcover) SearchBookByISBN13(context.Context, string) (*models.HardcoverBook, error) {
	c.identifierLookups++
	return nil, nil
}

func (c *syncAssociationHardcover) SearchBooks(context.Context, string, string) ([]models.HardcoverBook, error) {
	c.identifierLookups++
	return nil, nil
}

func (c *syncAssociationHardcover) GetEdition(_ context.Context, editionID string) (*models.Edition, error) {
	return &models.Edition{ID: editionID, BookID: fmt.Sprint(boundaryBookID)}, nil
}

func (c *syncAssociationHardcover) GetEditionUncached(_ context.Context, editionID string) (*models.Edition, error) {
	return &models.Edition{ID: editionID, BookID: fmt.Sprint(boundaryBookID)}, nil
}

func (c *syncAssociationHardcover) GetUserBookID(context.Context, int) (int, error) {
	return 7, nil
}

func TestCreateCommandAssociationIsUsedByNextSync(t *testing.T) {
	t.Chdir(t.TempDir()) // Audiobookshelf's client writes diagnostic response files.
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, boundaryABSItemJS)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryABSItem, boundaryASIN))
	if _, err := env.run(t, "create", "--input", inputPath); err != nil {
		t.Fatal(err)
	}

	const syncItem = `{"id":"li_boundary","libraryId":"library-1","mediaType":"book","media":{"metadata":{"title":"Boundary Book","authorName":"Test Author","asin":"B0ABCDE123","isbn":"9780306406157"},"duration":100},"progress":{"currentTime":0,"isFinished":false}}`
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/me":
			_, _ = w.Write([]byte(`{"mediaProgress":[]}`))
		case "/api/libraries":
			_, _ = w.Write([]byte(`{"libraries":[{"id":"library-1","name":"Test"}]}`))
		case "/api/libraries/library-1/items":
			_, _ = fmt.Fprintf(w, `{"results":[%s]}`, syncItem)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(absServer.Close)
	absClient, err := audiobookshelf.NewClientWithNetworkTrust(absServer.URL, "abs-token", audiobookshelf.NetworkTrustAllowPrivate)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Audiobookshelf.URL = absServer.URL
	cfg.Audiobookshelf.Token = "abs-token"
	cfg.Sync.StateFile = env.statePath
	cfg.Sync.Incremental = false
	cfg.Sync.SyncWantToRead = false
	cfg.Paths.CacheDir = t.TempDir()
	cfg.Paths.MismatchOutputDir = t.TempDir()
	syncHC := &syncAssociationHardcover{Client: hardcover.NewClient("test-token", logger.Get())}
	svc, err := absync.NewServiceWithRunIdentity(absClient, syncHC, cfg, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshot := svc.GetSnapshot()
	if len(snapshot.BookOutcomes) != 1 {
		t.Fatalf("expected one sync outcome, got %#v", snapshot.BookOutcomes)
	}
	outcome := snapshot.BookOutcomes[0]
	if outcome.HardcoverBookID != fmt.Sprint(boundaryBookID) || outcome.EditionID != fmt.Sprint(boundaryEditionID) {
		t.Fatalf("next sync did not use the CLI-saved association: %#v", outcome)
	}
	if syncHC.identifierLookups != 0 {
		t.Fatalf("next sync performed %d identifier lookups despite the saved association", syncHC.identifierLookups)
	}
}

func TestCreateCommandSavesNothingWhenHardcoverImportFails(t *testing.T) {
	const scopeDenied = `{"error":"insufficient_scope","error_description":"Missing scopes: write:catalog:append","scope":"write:catalog:append"}`
	const unknownField = `{"errors":[{"message":"field 'upsert_book' not found in type: 'mutation_root'","extensions":{"code":"validation-failed"}}]}`
	tests := []struct {
		name      string
		setup     func(*fakeHardcover)
		wantError string
	}{
		{
			name:      "missing catalogue write scope",
			setup:     func(f *fakeHardcover) { f.upsertHTTPStatus, f.upsertResponse = http.StatusForbidden, scopeDenied },
			wantError: "catalogue write permission is required, and no edition was created",
		},
		{
			name:      "unknown mutation field remains ambiguous",
			setup:     func(f *fakeHardcover) { f.upsertResponse = unknownField },
			wantError: "Hardcover may have processed the import",
		},
		{
			name: "import failure",
			setup: func(f *fakeHardcover) {
				f.upsertResponse = `{"data":{"upsert_book":{"id":77,"status":"failed","book":null,"edition":null,"edition_id":null,"errors":["Audible lookup failed"]}}}`
			},
			wantError: "regional audiobook import failed",
		},
		{
			name:      "unanswered import is not resent",
			setup:     func(f *fakeHardcover) { f.upsertHTTPStatus = http.StatusBadGateway },
			wantError: "Hardcover may have processed the import",
		},
		{
			name:      "wrong book",
			setup:     func(f *fakeHardcover) { f.readbackBook = boundaryBookID + 1 },
			wantError: "Hardcover may have processed the import",
		},
		{
			name:      "wrong reading format",
			setup:     func(f *fakeHardcover) { f.readbackFormat = 4 },
			wantError: "Hardcover may have processed the import",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := newFakeHardcover(t)
			tt.setup(hc)
			env := newCommandEnv(t, hc, boundaryABSItemJS)
			inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryABSItem, boundaryASIN))

			_, err := env.run(t, "create", "--input", inputPath)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
			if upserts, inserts, _ := hc.counts(); upserts != 1 || inserts != 0 {
				t.Fatalf("expected upsert_book sent once and no insert_edition fallback, got upserts=%d inserts=%d", upserts, inserts)
			}
			if _, ok := env.association(t); ok {
				t.Fatal("a failed import saved an association")
			}
		})
	}
}

func TestCreateCommandRefusesMissingABSItemBeforeHardcover(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, "")
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryASIN))

	_, err := env.run(t, "create", "--input", inputPath, "--abs-item-id", boundaryABSItem)
	if err == nil || !strings.Contains(err.Error(), "failed to verify Audiobookshelf item") {
		t.Fatalf("expected ABS item verification error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
		t.Fatalf("missing ABS item reached Hardcover: upserts=%d inserts=%d", upserts, inserts)
	}
	if _, ok := env.association(t); ok {
		t.Fatal("missing ABS item saved an association")
	}
}

func TestCreateCommandPrepopulatedTemplateImportsAudiobook(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, boundaryABSItemJS)
	templatePath := filepath.Join(t.TempDir(), "edition.json")

	var stdout bytes.Buffer
	app := newApp()
	app.Writer = &stdout
	app.ErrWriter = &bytes.Buffer{}
	if err := app.Run([]string{"edition", "--config", env.configPath, "prepopulate", "--book-id", "21", "--output", templatePath}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), templatePath) {
		t.Fatalf("prepopulate did not report its output file: %s", stdout.String())
	}

	// A book with both an ASIN and ISBN defaults to the established audiobook
	// path. The import reads its book ID and ASIN plus the supplied region.
	var template map[string]interface{}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &template); err != nil {
		t.Fatal(err)
	}
	if template["reading_format"] != models.ReadingFormatAudiobook {
		t.Fatalf("ASIN-backed prepopulate should select audiobook, got %#v", template["reading_format"])
	}
	template["asin_region"] = "uk"
	data, err = json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templatePath, data, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := env.run(t, "create", "--input", templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "created" || result["association_saved"] != false {
		t.Fatalf("unexpected result for prepopulated template: %#v", result)
	}
	if upserts, inserts, externalID := hc.counts(); upserts != 1 || inserts != 0 || externalID != boundaryRegional {
		t.Fatalf("prepopulated template did not use the regional import: upserts=%d inserts=%d external_id=%q", upserts, inserts, externalID)
	}
	if _, err := os.Stat(env.statePath); !os.IsNotExist(err) {
		t.Fatalf("create without an ABS item ID wrote state: %v", err)
	}
}

func TestCreateCommandISBNOnlyAudiobookInsertsAudiobookEdition(t *testing.T) {
	hc := newFakeHardcover(t)
	hc.emptyLookups = true
	hc.insertResponse = `{"data":{"insert_edition":{"id":901,"errors":[]}}}`
	hc.readbackEdition = 901
	hc.readbackFormat = models.ReadingFormatID(models.ReadingFormatAudiobook)
	itemJSON := `{"id":"li_boundary","mediaType":"book","media":{"metadata":{"asin":"bad asin","isbn":"9780306406157"},"duration":100}}`
	env := newCommandEnv(t, hc, itemJSON)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"title":"Audio Book","isbn_13":"9780306406157","author_ids":[3],"reading_format":"audiobook","abs_item_id":%q}`, boundaryBookID, boundaryABSItem))

	result, err := env.run(t, "create", "--input", inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "created" || result["reading_format"] != models.ReadingFormatAudiobook || result["association_saved"] != true {
		t.Fatalf("unexpected ISBN-only audiobook result: %#v", result)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 1 {
		t.Fatalf("ISBN-only audiobook did not use insert_edition: upserts=%d inserts=%d", upserts, inserts)
	}
	association, exists := env.association(t)
	if !exists || association.HardcoverEditionID != "901" || association.ReadingFormat != models.ReadingFormatAudiobook || association.Provenance != "cli_audiobook_created" {
		t.Fatalf("unexpected ISBN-only audiobook association: %#v exists=%t", association, exists)
	}
}

func TestCreateCommandRequiresConfirmationForISBNCorrection(t *testing.T) {
	hc := newFakeHardcover(t)
	hc.emptyLookups = true
	hc.insertResponse = `{"data":{"insert_edition":{"id":901,"errors":[]}}}`
	hc.readbackEdition = 901
	hc.readbackFormat = models.ReadingFormatID(models.ReadingFormatAudiobook)
	itemJSON := `{"id":"li_boundary","mediaType":"book","media":{"metadata":{"asin":"invalid","isbn":"9780804429573"},"duration":100}}`
	env := newCommandEnv(t, hc, itemJSON)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"title":"Audio Book","isbn_13":"9780306406157","author_ids":[3],"reading_format":"audiobook","abs_item_id":%q}`, boundaryBookID, boundaryABSItem))

	if _, err := env.run(t, "create", "--input", inputPath); err == nil || !strings.Contains(err.Error(), "submitted ISBN does not match Audiobookshelf item") {
		t.Fatalf("expected ISBN correction confirmation error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
		t.Fatalf("unconfirmed ISBN correction reached Hardcover: upserts=%d inserts=%d", upserts, inserts)
	}

	result, err := env.run(t, "create", "--input", inputPath, "--confirm-identifier-correction")
	if err != nil {
		t.Fatal(err)
	}
	if result["association_saved"] != true || !strings.Contains(result["warning"].(string), "WARNING:") {
		t.Fatalf("confirmed ISBN correction was not reported and associated: %#v", result)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 1 {
		t.Fatalf("confirmed ISBN correction did not use insert_edition: upserts=%d inserts=%d", upserts, inserts)
	}
}

func TestCreateCommandRequiresConfirmationForASINCorrectionFromInvalidSource(t *testing.T) {
	hc := newFakeHardcover(t)
	itemJSON := `{"id":"li_boundary","mediaType":"book","media":{"metadata":{"asin":"invalid","isbn":"9780306406157"},"duration":100}}`
	env := newCommandEnv(t, hc, itemJSON)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"asin":%q,"title":"Audio Book","isbn_13":"9780306406157","author_ids":[3],"reading_format":"audiobook","asin_region":"uk","abs_item_id":%q}`, boundaryBookID, boundaryASIN, boundaryABSItem))

	if _, err := env.run(t, "create", "--input", inputPath); err == nil || !strings.Contains(err.Error(), "submitted ASIN does not match Audiobookshelf item") {
		t.Fatalf("expected ASIN correction confirmation error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
		t.Fatalf("unconfirmed ASIN correction reached Hardcover: upserts=%d inserts=%d", upserts, inserts)
	}

	result, err := env.run(t, "create", "--input", inputPath, "--confirm-identifier-correction")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "created" || result["association_saved"] != true || !strings.Contains(result["warning"].(string), "WARNING:") {
		t.Fatalf("confirmed ASIN correction was not reported and associated: %#v", result)
	}
	if upserts, inserts, externalID := hc.counts(); upserts != 1 || inserts != 0 || externalID != boundaryRegional {
		t.Fatalf("confirmed ASIN correction did not use regional import: upserts=%d inserts=%d external_id=%q", upserts, inserts, externalID)
	}
	association, exists := env.association(t)
	if !exists || association.Correction != boundaryASIN || association.RegionalExternalID != boundaryRegional {
		t.Fatalf("corrected ASIN was not preserved in the association: %#v exists=%t", association, exists)
	}
}

func TestCreateCommandCannotBypassValidABSASIN(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, boundaryABSItemJS)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"title":"Audio Book","isbn_13":"9780306406157","author_ids":[3],"reading_format":"audiobook","asin_region":"uk","abs_item_id":%q}`, boundaryBookID, boundaryABSItem))

	result, err := env.run(t, "create", "--input", inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "created" {
		t.Fatalf("unexpected regional audiobook result: %#v", result)
	}
	if upserts, inserts, externalID := hc.counts(); upserts != 1 || inserts != 0 || externalID != boundaryRegional {
		t.Fatalf("valid ABS ASIN was bypassed: upserts=%d inserts=%d external_id=%q", upserts, inserts, externalID)
	}
}

func TestCreateCommandTreatsMalformedSubmittedASINAsMissing(t *testing.T) {
	hc := newFakeHardcover(t)
	hc.emptyLookups = true
	hc.insertResponse = `{"data":{"insert_edition":{"id":901,"errors":[]}}}`
	hc.readbackEdition = 901
	hc.readbackFormat = models.ReadingFormatID(models.ReadingFormatAudiobook)
	env := newCommandEnv(t, hc, "")
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"title":"Audio Book","asin":"not-an-asin","isbn_13":"9780306406157","author_ids":[3],"reading_format":"audiobook"}`, boundaryBookID))

	result, err := env.run(t, "create", "--input", inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "created" || result["reading_format"] != models.ReadingFormatAudiobook {
		t.Fatalf("unexpected malformed-ASIN fallback result: %#v", result)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 1 {
		t.Fatalf("malformed submitted ASIN did not fall back to insert_edition: upserts=%d inserts=%d", upserts, inserts)
	}
}

func TestPrepopulateISBNOnlyBookCreatesEbookTemplate(t *testing.T) {
	hc := newFakeHardcover(t)
	hc.bookResponse = fmt.Sprintf(`{"data":{"book":{"id":%d,"title":"ISBN Book","isbn13":"9780306406157","authors":[{"id":3,"name":"Author"}]}}}`, boundaryBookID)
	hc.emptyLookups = true
	hc.insertResponse = `{"data":{"insert_edition":{"id":901,"errors":[]}}}`
	hc.readbackEdition = 901
	hc.readbackFormat = models.ReadingFormatID(models.ReadingFormatEbook)
	// No edition has the same numeric ID as the book ID. Prepopulation must
	// fetch the book itself instead of treating the book ID as an edition ID.
	hc.missingEditionID = boundaryBookID
	env := newCommandEnv(t, hc, "")
	templatePath := filepath.Join(t.TempDir(), "edition.json")

	var stdout bytes.Buffer
	app := newApp()
	app.Writer = &stdout
	app.ErrWriter = &bytes.Buffer{}
	if err := app.Run([]string{"edition", "--config", env.configPath, "prepopulate", "--book-id", "21", "--output", templatePath}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	var template map[string]interface{}
	if err := json.Unmarshal(data, &template); err != nil {
		t.Fatal(err)
	}
	if template["reading_format"] != models.ReadingFormatEbook || template["edition_format"] != "Ebook" {
		t.Fatalf("ISBN-only template should select ebook, got %#v", template)
	}

	result, err := env.run(t, "create", "--input", templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "created" || result["reading_format"] != models.ReadingFormatEbook {
		t.Fatalf("prepopulated ISBN-only template did not create an ebook: %#v", result)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 1 {
		t.Fatalf("ISBN-only template should use ebook insertion once: upserts=%d inserts=%d", upserts, inserts)
	}
}

func TestPrepopulateRejectsUnknownReadingFormat(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, "")
	app := newApp()
	app.Writer = &bytes.Buffer{}
	app.ErrWriter = &bytes.Buffer{}
	err := app.Run([]string{"edition", "--config", env.configPath, "prepopulate", "--book-id", "21", "--reading-format", "print"})
	if err == nil || !strings.Contains(err.Error(), `invalid reading_format "print"`) {
		t.Fatalf("expected invalid format error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
		t.Fatalf("invalid format reached a Hardcover mutation: upserts=%d inserts=%d", upserts, inserts)
	}
}

func TestPrepopulateValidatesFetchedBookIdentity(t *testing.T) {
	tests := []struct {
		name      string
		book      string
		wantError string
	}{
		{
			name:      "book not found",
			book:      `{"data":{"book":null}}`,
			wantError: "Hardcover book 21 was not found",
		},
		{
			name:      "different book returned",
			book:      `{"data":{"book":{"id":22}}}`,
			wantError: "requested Hardcover book 21 but received book 22",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := newFakeHardcover(t)
			hc.bookResponse = tt.book
			env := newCommandEnv(t, hc, "")
			templatePath := filepath.Join(t.TempDir(), "edition.json")
			app := newApp()
			app.Writer = &bytes.Buffer{}
			app.ErrWriter = &bytes.Buffer{}
			err := app.Run([]string{"edition", "--config", env.configPath, "prepopulate", "--book-id", "21", "--output", templatePath})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
			if _, err := os.Stat(templatePath); !os.IsNotExist(err) {
				t.Fatalf("invalid Hardcover book wrote a template: %v", err)
			}
		})
	}
}

func TestPrepopulateExplicitReadingFormatOverridesASINInference(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, "")
	templatePath := filepath.Join(t.TempDir(), "ebook.json")
	app := newApp()
	app.Writer = &bytes.Buffer{}
	app.ErrWriter = &bytes.Buffer{}
	if err := app.Run([]string{"edition", "--config", env.configPath, "prepopulate", "--book-id", "21", "--reading-format", "ebook", "--output", templatePath}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	var template map[string]interface{}
	if err := json.Unmarshal(data, &template); err != nil {
		t.Fatal(err)
	}
	if template["reading_format"] != models.ReadingFormatEbook || template["edition_format"] != "Ebook" {
		t.Fatalf("explicit ebook selection was not applied: %#v", template)
	}
}

func realCreateServices(t *testing.T, env commandEnv) createServices {
	t.Helper()
	cfg, err := loadEditionConfig(env.configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	services, err := newCreateServices(cfg, logger.Get(), false)
	if err != nil {
		t.Fatal(err)
	}
	return services
}

func TestRunCreateReportsPossibleImportWhenStoppedAfterSending(t *testing.T) {
	hc := newFakeHardcover(t)
	hc.importStatus = "fetching"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The operator stops the command while Hardcover is still importing.
	hc.onPoll = cancel
	env := newCommandEnv(t, hc, boundaryABSItemJS)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryABSItem, boundaryASIN))

	_, err := runCreate(ctx, createOptions{InputPath: inputPath, StateFile: env.statePath}, realCreateServices(t, env))
	if err == nil || !strings.Contains(err.Error(), "Hardcover may have processed the import") {
		t.Fatalf("expected a possible-import error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 1 || inserts != 0 {
		t.Fatalf("expected one upsert_book and no insert_edition, got upserts=%d inserts=%d", upserts, inserts)
	}
	if _, ok := env.association(t); ok {
		t.Fatal("an unfinished import saved an association")
	}
}

func TestRunCreateRefusesImportWithoutMutationReserve(t *testing.T) {
	hc := newFakeHardcover(t)
	env := newCommandEnv(t, hc, boundaryABSItemJS)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryASIN))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := runCreate(ctx, createOptions{InputPath: inputPath, StateFile: env.statePath}, realCreateServices(t, env))
	if err == nil || !strings.Contains(err.Error(), "no Hardcover change was made") {
		t.Fatalf("expected a no-change budget error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 0 {
		t.Fatalf("a mutation was sent without its reserve: upserts=%d inserts=%d", upserts, inserts)
	}
}

func TestCreateCommandSendsEbookInsertionOnce(t *testing.T) {
	const scopeDenied = `{"error":"insufficient_scope","error_description":"Missing scopes: write:catalog:append","scope":"write:catalog:append"}`
	const unknownField = `{"errors":[{"message":"field 'insert_edition' not found in type: 'mutation_root'","extensions":{"code":"validation-failed"}}]}`
	tests := []struct {
		name      string
		setup     func(*fakeHardcover)
		wantError string
	}{
		{
			name:      "missing catalogue write scope",
			setup:     func(f *fakeHardcover) { f.insertHTTPStatus, f.insertResponse = http.StatusForbidden, scopeDenied },
			wantError: "catalogue write permission is required, and no edition was created",
		},
		{
			name:      "unknown mutation field remains ambiguous",
			setup:     func(f *fakeHardcover) { f.insertResponse = unknownField },
			wantError: "Hardcover may have processed the insertion",
		},
		{
			name:      "unanswered insertion is not resent",
			setup:     func(f *fakeHardcover) { f.insertHTTPStatus = http.StatusBadGateway },
			wantError: "Hardcover may have processed the insertion",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := newFakeHardcover(t)
			hc.emptyLookups = true
			tt.setup(hc)
			env := newCommandEnv(t, hc, "")
			inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"title":"Ebook","isbn_13":"9780306406157","author_ids":[3],"reading_format":"ebook"}`, boundaryBookID))

			_, err := env.run(t, "create", "--input", inputPath)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
			if upserts, inserts, _ := hc.counts(); upserts != 0 || inserts != 1 {
				t.Fatalf("expected insert_edition sent once, got upserts=%d inserts=%d", upserts, inserts)
			}
		})
	}
}

func TestRunCreateReportsLocalSaveFailureAfterRemoteSuccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop root from writing the state file")
	}
	tests := []struct {
		name       string
		input      string
		bookID     int
		unanchored bool
	}{
		{
			name:   "anchored",
			input:  fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryABSItem, boundaryASIN),
			bookID: boundaryBookID,
		},
		{
			name:       "unanchored",
			input:      fmt.Sprintf(`{"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryABSItem, boundaryASIN),
			unanchored: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newCommandEnv(t, newFakeHardcover(t), boundaryABSItemJS)
			stateDir := t.TempDir()
			statePath := filepath.Join(stateDir, "sync-state.json")
			t.Cleanup(func() { _ = os.Chmod(stateDir, 0700) })
			inputPath := writeCreateInput(t, tt.input)
			audible := &audnex.Book{ASIN: boundaryASIN, Title: "Boundary Book"}
			services := env.createServices(t, false, audible, "uk")
			var importInput hardcover.RegionalAudiobookInput
			importCalls := 0
			services.importAudiobook = func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
				importCalls++
				importInput = input
				// Hardcover succeeds, then the state directory stops accepting writes.
				if err := os.Chmod(stateDir, 0500); err != nil {
					t.Fatal(err)
				}
				return &hardcover.RegionalAudiobookResult{
					Status: hardcover.RegionalAudiobookCreated, BookID: boundaryBookID, EditionID: boundaryEditionID,
					ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
					RegionalExternalID: boundaryRegional,
				}, nil
			}

			options := createOptions{InputPath: inputPath, StateFile: statePath}
			if tt.unanchored {
				options.ConfirmAudnexus = true
			}
			result, err := runCreate(context.Background(), options, services)
			if err == nil || result != nil {
				t.Fatalf("expected a local save failure, got result=%#v err=%v", result, err)
			}
			for _, want := range []string{"Hardcover reported audiobook created", "local association could not be saved", "Verify the Hardcover result before retrying"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("save failure message %q is missing %q", err.Error(), want)
				}
			}
			if importCalls != 1 || importInput.BookID != tt.bookID || importInput.Unanchored != tt.unanchored {
				t.Fatalf("unexpected import request after mode %q: calls=%d input=%#v", tt.name, importCalls, importInput)
			}
			if err := os.Chmod(stateDir, 0700); err != nil {
				t.Fatal(err)
			}
			if _, statErr := os.Stat(statePath); statErr == nil {
				loaded, loadErr := state.LoadState(statePath)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if _, ok := loaded.GetAssociation(boundaryABSItem); ok {
					t.Fatal("association was reported unsaved but exists in state")
				}
			}
		})
	}
}

func TestRunCreateUnanchoredImportTimeoutWarnsAndDoesNotSaveAssociation(t *testing.T) {
	env := newCommandEnv(t, newFakeHardcover(t), boundaryABSItemJS)
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"asin":"%s","asin_region":"uk","abs_item_id":%q}`, strings.ToLower(boundaryASIN), boundaryABSItem))
	var importInput hardcover.RegionalAudiobookInput
	importCalls := 0
	services := unanchoredPromptServices(testAudiobook(boundaryABSItem, boundaryASIN), &importCalls)
	services.importAudiobook = func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
		importCalls++
		importInput = input
		return nil, fmt.Errorf("status polling stopped: %w", hardcover.ErrRegionalAudiobookImportTimeout)
	}

	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, StateFile: env.statePath, ConfirmAudnexus: true,
	}, services)
	if err == nil || result != nil {
		t.Fatalf("expected an uncertain import timeout, got result=%#v err=%v", result, err)
	}
	if !errors.Is(err, hardcover.ErrRegionalAudiobookImportTimeout) {
		t.Fatalf("timeout error lost its Hardcover identity: %v", err)
	}
	for _, want := range []string{"Hardcover may have processed the import", "verify the book in Hardcover before retrying"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("timeout error %q is missing operator guidance %q", err.Error(), want)
		}
	}
	if importCalls != 1 {
		t.Fatalf("timed-out import was automatically resubmitted %d times", importCalls-1)
	}
	if importInput.BookID != 0 || importInput.ASIN != boundaryASIN || importInput.Region != "uk" || !importInput.Unanchored {
		t.Fatalf("unanchored import did not receive the canonical regional request: %#v", importInput)
	}
	if _, ok := env.association(t); ok {
		t.Fatal("timed-out import saved an Audiobookshelf association")
	}
	lock, lockErr := state.AcquireFileLock(env.statePath)
	if lockErr != nil {
		t.Fatalf("state-file lock was not released after import timeout: %v", lockErr)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("release reacquired state-file lock: %v", err)
	}
}
