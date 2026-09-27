package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
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

	upsertResponse string
	importStatus   string
	readbackBook   int
	readbackFormat int

	mu               sync.Mutex
	upsertCalls      int
	insertEditions   int
	upsertExternalID string
}

func newFakeHardcover(t *testing.T) *fakeHardcover {
	return &fakeHardcover{
		t:              t,
		upsertResponse: fmt.Sprintf(`{"data":{"upsert_book":{"id":77,"status":"fetching","book":{"id":%d},"edition":{"id":%d,"book_id":%d,"reading_format_id":2},"edition_id":%d,"errors":[]}}}`, boundaryBookID, boundaryEditionID, boundaryBookID, boundaryEditionID),
		importStatus:   "created",
		readbackBook:   boundaryBookID,
		readbackFormat: 2,
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
		http.Error(w, "unexpected insert_edition", http.StatusBadRequest)
	case strings.Contains(request.Query, "UpsertRegionalAudibleBook"):
		f.upsertCalls++
		if book, ok := request.Variables["book"].(map[string]interface{}); ok {
			f.upsertExternalID, _ = book["external_id"].(string)
		}
		_, _ = w.Write([]byte(f.upsertResponse))
	case strings.Contains(request.Query, "RegionalAudibleImport"):
		statuses := []map[string]interface{}{{
			"status": f.importStatus, "book_id": boundaryBookID, "edition_id": boundaryEditionID,
			"external_id": boundaryRegional, "platform_id": 32,
		}}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
			"book_import_statuses": statuses,
			"book_mappings":        []interface{}{},
		}})
	case strings.Contains(request.Query, "GetEdition"):
		_, _ = fmt.Fprintf(w, `{"data":{"editions":[{"id":%d,"book_id":%d,"reading_format_id":%d}]}}`, boundaryEditionID, f.readbackBook, f.readbackFormat)
	case strings.Contains(request.Query, "GetBook"):
		_, _ = fmt.Fprintf(w, `{"data":{"book":{"id":%d,"title":"Prepopulated","asin":%q,"isbn13":"9780306406157","authors":[{"id":3,"name":"Author"}],"narrators":[{"id":4,"name":"Narrator"}]}}}`, boundaryBookID, boundaryASIN)
	default:
		f.t.Errorf("unexpected Hardcover query: %s", request.Query)
		http.Error(w, "unexpected query", http.StatusBadRequest)
	}
}

func (f *fakeHardcover) counts() (upserts, inserts int, externalID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upsertCalls, f.insertEditions, f.upsertExternalID
}

// commandEnv is a config file pointing the command at fake Hardcover and
// Audiobookshelf servers, with an isolated state file.
type commandEnv struct {
	configPath string
	statePath  string
	hardcover  *fakeHardcover
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

func TestCreateCommandSavesNothingWhenHardcoverImportFails(t *testing.T) {
	const scopeDenied = `{"errors":[{"message":"field 'upsert_book' not found in type: 'mutation_root'","extensions":{"code":"validation-failed"}}]}`
	tests := []struct {
		name  string
		setup func(*fakeHardcover)
	}{
		{name: "missing catalogue write scope", setup: func(f *fakeHardcover) { f.upsertResponse = scopeDenied }},
		{name: "import failure", setup: func(f *fakeHardcover) {
			f.upsertResponse = `{"data":{"upsert_book":{"id":77,"status":"failed","book":null,"edition":null,"edition_id":null,"errors":["Audible lookup failed"]}}}`
		}},
		{name: "wrong book", setup: func(f *fakeHardcover) { f.readbackBook = boundaryBookID + 1 }},
		{name: "wrong reading format", setup: func(f *fakeHardcover) { f.readbackFormat = 4 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := newFakeHardcover(t)
			tt.setup(hc)
			env := newCommandEnv(t, hc, boundaryABSItemJS)
			inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryABSItem, boundaryASIN))

			if _, err := env.run(t, "create", "--input", inputPath); err == nil {
				t.Fatal("expected the failed import to exit with an error")
			}
			if upserts, inserts, _ := hc.counts(); upserts == 0 || inserts != 0 {
				t.Fatalf("expected an upsert_book attempt and no insert_edition fallback, got upserts=%d inserts=%d", upserts, inserts)
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

	// The prepopulated template carries ebook-style metadata; an audiobook
	// import reads only its book ID and ASIN plus the supplied region.
	var template map[string]interface{}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &template); err != nil {
		t.Fatal(err)
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

func TestRunCreateSavesNothingWhenImportTimesOut(t *testing.T) {
	hc := newFakeHardcover(t)
	hc.importStatus = "fetching"
	env := newCommandEnv(t, hc, boundaryABSItemJS)
	cfg, err := loadEditionConfig(env.configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	services, err := newCreateServices(cfg, logger.Get(), false)
	if err != nil {
		t.Fatal(err)
	}
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryABSItem, boundaryASIN))
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	_, err = runCreate(ctx, createOptions{InputPath: inputPath, StateFile: env.statePath}, services)
	if err == nil || !strings.Contains(err.Error(), "failed to import audiobook") {
		t.Fatalf("expected an import timeout error, got %v", err)
	}
	if upserts, inserts, _ := hc.counts(); upserts != 1 || inserts != 0 {
		t.Fatalf("expected one upsert_book and no insert_edition, got upserts=%d inserts=%d", upserts, inserts)
	}
	if _, ok := env.association(t); ok {
		t.Fatal("a timed-out import saved an association")
	}
}

func TestRunCreateReportsLocalSaveFailureAfterRemoteSuccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop root from writing the state file")
	}
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "sync-state.json")
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0700) })
	inputPath := writeCreateInput(t, fmt.Sprintf(`{"book_id":%d,"abs_item_id":%q,"asin":%q,"asin_region":"uk"}`, boundaryBookID, boundaryABSItem, boundaryASIN))
	item := testAudiobook(boundaryABSItem, boundaryASIN)
	services := createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) { return item, nil },
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			// Hardcover succeeds, then the state directory stops accepting writes.
			if err := os.Chmod(stateDir, 0500); err != nil {
				t.Fatal(err)
			}
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookCreated, BookID: boundaryBookID, EditionID: boundaryEditionID,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: boundaryRegional,
			}, nil
		},
	}

	result, err := runCreate(context.Background(), createOptions{InputPath: inputPath, StateFile: statePath}, services)
	if err == nil || result != nil {
		t.Fatalf("expected a local save failure, got result=%#v err=%v", result, err)
	}
	for _, want := range []string{"Hardcover reported audiobook created", "local association could not be saved", "Verify the Hardcover result before retrying"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("save failure message %q is missing %q", err.Error(), want)
		}
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
}
