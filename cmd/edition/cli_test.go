package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

func TestRunCreateImportsMismatchExportAndReportsLoaded(t *testing.T) {
	inputPath := writeCreateInput(t, `{
		"book_id": 21,
		"title": "Export title",
		"asin": "B012345678",
		"reading_format": "audiobook",
		"info": {"author": "informational only", "reason": "mismatch export"}
	}`)
	var got hardcover.RegionalAudiobookInput
	services := createServices{
		discoverAudible: func(_ context.Context, asin, preferred string) (string, error) {
			if asin != "B012345678" || preferred != "ca" {
				t.Fatalf("unexpected discovery input: ASIN=%q preferred=%q", asin, preferred)
			}
			return "ca", nil
		},
		importAudiobook: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			got = input
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookLoaded, BookID: 21, EditionID: 34,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: "B012345678:ca",
			}, nil
		},
	}
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, PreferredRegion: "ca",
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if got != (hardcover.RegionalAudiobookInput{BookID: 21, ASIN: "B012345678", Region: "ca"}) {
		t.Fatalf("unexpected resolver input: %#v", got)
	}
	if result.Status != "loaded" || result.EditionID != 34 || result.BookID != 21 || result.AssociationSaved {
		t.Fatalf("unexpected CLI result: %#v", result)
	}
}

func TestReadEditionCreateInputAcceptsMismatchABSItemID(t *testing.T) {
	path := writeCreateInput(t, `{
		"book_id": 21,
		"asin": "B012345678",
		"abs_item_id": "item-from-export",
		"info": {"reason": "informational"}
	}`)
	input, err := readEditionCreateInput(path)
	if err != nil {
		t.Fatal(err)
	}
	if input.ABSItemID != "item-from-export" || input.BookID != 21 || input.ASIN != "B012345678" {
		t.Fatalf("mismatch export fields were not decoded: %#v", input)
	}
}

func TestRunCreateStoresVerifiedAudiobookAssociationUnderLock(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "sync-state.json")
	inputPath := writeCreateInput(t, `{
		"book_id": 21,
		"asin": "B012345678",
		"abs_item_id": "ignored-json-item",
		"region": "uk",
		"reading_format": "audiobook",
		"info": {"title": "ignored"}
	}`)
	item := testAudiobook("item-1", "B012345678")
	services := createServices{
		fetchABSItem: func(_ context.Context, itemID string) (*models.AudiobookshelfBook, error) {
			if itemID != item.ID {
				t.Fatalf("requested item %q, expected %q", itemID, item.ID)
			}
			return item, nil
		},
		importAudiobook: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			lock, err := state.AcquireFileLock(statePath)
			if lock != nil {
				_ = lock.Close()
			}
			if !errors.Is(err, state.ErrStateFileLocked) {
				t.Fatalf("state lock was not held during Hardcover import: %v", err)
			}
			if input.Region != "uk" {
				t.Fatalf("expected confirmed region uk, got %q", input.Region)
			}
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookCreated, BookID: 21, EditionID: 34,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: "B012345678:uk",
			}, nil
		},
	}
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, ABSItemID: "item-1", StateFile: statePath,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "created" || !result.AssociationSaved {
		t.Fatalf("unexpected result: %#v", result)
	}
	loaded, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	association, ok := loaded.GetAssociation("item-1")
	if !ok {
		t.Fatal("verified ABS association was not saved")
	}
	if association.SourceASIN != "B012345678" || association.RegionalExternalID != "B012345678:uk" || association.HardcoverBookID != "21" || association.HardcoverEditionID != "34" || association.Provenance != "cli_regional_created" {
		t.Fatalf("unexpected saved association: %#v", association)
	}
}

func TestRunCreateRejectsExistingAssociationBeforeRemoteCalls(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "sync-state.json")
	initial := state.NewState()
	existing := state.Association{
		ABSItemID: "item-1", SourceASIN: "B012345678", HardcoverBookID: "21",
		HardcoverEditionID: "34", ReadingFormat: models.ReadingFormatAudiobook, Provenance: "cli_audiobook_created",
	}
	if err := initial.SetAssociation(existing); err != nil {
		t.Fatal(err)
	}
	if err := initial.Save(statePath); err != nil {
		t.Fatal(err)
	}
	inputPath := writeCreateInput(t, `{"book_id":21,"asin":"B012345678","abs_item_id":"item-1","region":"uk"}`)
	remoteCalled := false
	services := createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
			remoteCalled = true
			return nil, nil
		},
		discoverAudible: func(context.Context, string, string) (string, error) {
			remoteCalled = true
			return "uk", nil
		},
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			remoteCalled = true
			return nil, nil
		},
	}
	_, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, ABSItemID: "item-1", StateFile: statePath,
	}, services)
	if err == nil || !strings.Contains(err.Error(), "already has a confirmed Hardcover association") {
		t.Fatalf("expected existing association error, got %v", err)
	}
	if remoteCalled {
		t.Fatal("existing association reached a remote service")
	}
	loaded, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := loaded.GetAssociation("item-1"); !ok || got != existing {
		t.Fatalf("existing association changed: got %#v, exists=%t", got, ok)
	}
}

func TestRunCreateKeepsAssociationOnLockedTargetAfterStateAliasRetarget(t *testing.T) {
	tmp := t.TempDir()
	targetA := filepath.Join(tmp, "state-a.json")
	targetB := filepath.Join(tmp, "state-b.json")
	stateAlias := filepath.Join(tmp, "state.json")
	initialA := state.NewState()
	initialA.UpdateBook("target-a-marker", 0.1, "IN_PROGRESS")
	if err := initialA.Save(targetA); err != nil {
		t.Fatal(err)
	}
	initialB := state.NewState()
	initialB.UpdateBook("target-b-marker", 0.2, "IN_PROGRESS")
	if err := initialB.Save(targetB); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetA, stateAlias); err != nil {
		t.Fatalf("create state alias: %v", err)
	}
	inputPath := writeCreateInput(t, `{
		"book_id": 21,
		"asin": "B012345678",
		"abs_item_id": "item-1",
		"region": "uk",
		"reading_format": "audiobook"
	}`)
	services := createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
			// The create command has acquired the lock and loaded state by this
			// point. Retargeting the alias must not redirect its later save.
			if err := os.Remove(stateAlias); err != nil {
				t.Fatalf("remove state alias: %v", err)
			}
			if err := os.Symlink(targetB, stateAlias); err != nil {
				t.Fatalf("retarget state alias: %v", err)
			}
			return testAudiobook("item-1", "B012345678"), nil
		},
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookLoaded, BookID: 21, EditionID: 34,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: "B012345678:uk",
			}, nil
		},
	}
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, ABSItemID: "item-1", StateFile: stateAlias,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AssociationSaved {
		t.Fatalf("expected association to be saved: %#v", result)
	}
	lockedTarget, err := state.LoadState(targetA)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lockedTarget.GetAssociation("item-1"); !ok {
		t.Fatal("association was not saved to the target whose lock was acquired")
	}
	retargeted, err := state.LoadState(targetB)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retargeted.GetAssociation("item-1"); ok {
		t.Fatal("association was redirected to the alias's new target")
	}
}

func TestRunCreateRejectsUnknownRegionBeforeExternalCalls(t *testing.T) {
	inputPath := writeCreateInput(t, `{"book_id": 21, "asin": "B012345678", "asin_region": "xx"}`)
	called := false
	services := createServices{
		discoverAudible: func(context.Context, string, string) (string, error) {
			called = true
			return "", nil
		},
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			called = true
			return nil, nil
		},
	}
	_, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, StateFile: filepath.Join(t.TempDir(), "state.json"),
	}, services)
	if err == nil || !strings.Contains(err.Error(), "unknown Audible region") {
		t.Fatalf("expected invalid region error, got %v", err)
	}
	if called {
		t.Fatal("invalid region reached an external service")
	}
}

func TestRunCreateAudibleRegionOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		inputJSON   string
		discoverErr error
		wantRegion  string
		wantError   string
	}{
		{
			name:        "explicit region imports without Audnex",
			inputJSON:   `{"book_id":21,"asin":"b012345678","asin_region":"UK"}`,
			discoverErr: audnex.ErrTransient,
			wantRegion:  "uk",
		},
		{
			name:        "rate-limited discovery is retryable and imports nothing",
			inputJSON:   `{"book_id":21,"asin":"B012345678"}`,
			discoverErr: audnex.ErrRateLimited,
			wantError:   "temporarily unavailable; retry later or set asin_region",
		},
		{
			name:      "malformed ASIN is rejected before external calls",
			inputJSON: `{"book_id":21,"asin":"B0123","asin_region":"uk"}`,
			wantError: "exactly ten letters or digits",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inputPath := writeCreateInput(t, test.inputJSON)
			discovered := false
			var imported *hardcover.RegionalAudiobookInput
			services := createServices{
				discoverAudible: func(context.Context, string, string) (string, error) {
					discovered = true
					return "", test.discoverErr
				},
				importAudiobook: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					imported = &input
					return &hardcover.RegionalAudiobookResult{
						Status: hardcover.RegionalAudiobookCreated, BookID: 21, EditionID: 34,
						ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
						RegionalExternalID: input.ASIN + ":" + input.Region,
					}, nil
				},
			}
			result, err := runCreate(context.Background(), createOptions{InputPath: inputPath}, services)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("expected error containing %q, got %v", test.wantError, err)
				}
				if imported != nil {
					t.Fatalf("failed region resolution reached Hardcover: %#v", imported)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if discovered {
				t.Fatal("explicit region was sent to Audnex discovery")
			}
			if imported == nil || imported.ASIN != "B012345678" || imported.Region != test.wantRegion || result.Status != "created" {
				t.Fatalf("unexpected import %#v with result %#v", imported, result)
			}
		})
	}
}

func TestRunCreateRejectsABSFormatMismatchBeforeHardcover(t *testing.T) {
	inputPath := writeCreateInput(t, `{"book_id": 21, "asin": "B012345678", "abs_item_id": "item-1"}`)
	called := false
	services := createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
			item := &models.AudiobookshelfBook{ID: "item-1", MediaType: "ebook"}
			item.Media.EbookFormat = "epub"
			return item, nil
		},
		discoverAudible: func(context.Context, string, string) (string, error) {
			called = true
			return "ca", nil
		},
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			called = true
			return nil, nil
		},
	}
	_, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, StateFile: filepath.Join(t.TempDir(), "state.json"),
	}, services)
	if err == nil || !strings.Contains(err.Error(), "input reading_format is audiobook") {
		t.Fatalf("expected item format mismatch, got %v", err)
	}
	if called {
		t.Fatal("format mismatch reached region discovery or Hardcover")
	}
}

func TestRunCreateRecordsEbookIdentifierDifferences(t *testing.T) {
	tests := []struct {
		name           string
		inputJSON      string
		item           *models.AudiobookshelfBook
		wantCorrection string
	}{
		{
			name:           "different ASIN is recorded as a correction",
			inputJSON:      `{"book_id":21,"title":"Ebook","asin":"B012345678","author_ids":[3],"reading_format":"ebook","abs_item_id":"item-1"}`,
			item:           testEbook("item-1", "B099999999", "9780306406157"),
			wantCorrection: "B012345678",
		},
		{
			name:      "different ISBN uses the supplied metadata",
			inputJSON: `{"book_id":21,"title":"Ebook","isbn_13":"9780306406157","author_ids":[3],"reading_format":"ebook","abs_item_id":"item-1"}`,
			item:      testEbook("item-1", "B012345678", "9780804429573"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "sync-state.json")
			inputPath := writeCreateInput(t, test.inputJSON)
			var submitted edition.EditionInput
			services := createServices{
				fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
					return test.item, nil
				},
				createEbook: func(_ context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
					submitted = *input
					return &edition.EditionResult{Success: true, EditionID: 34}, nil
				},
				getEditionUncached: func(context.Context, string) (*models.Edition, error) {
					return &models.Edition{ID: "34", BookID: "21", ReadingFormatID: "4"}, nil
				},
			}
			result, err := runCreate(context.Background(), createOptions{InputPath: inputPath, StateFile: statePath}, services)
			if err != nil {
				t.Fatal(err)
			}
			if !result.AssociationSaved {
				t.Fatalf("association was not saved: %#v", result)
			}
			expected, err := readEditionCreateInput(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			if submitted.ASIN != expected.ASIN || submitted.ISBN13 != expected.ISBN13 {
				t.Fatalf("ebook creation did not use the supplied identifiers: %#v", submitted)
			}
			loaded, err := state.LoadState(statePath)
			if err != nil {
				t.Fatal(err)
			}
			association, ok := loaded.GetAssociation("item-1")
			if !ok || association.SourceASIN != test.item.Media.Metadata.ASIN ||
				association.SourceISBN13 != test.item.Media.Metadata.ISBN || association.Correction != test.wantCorrection {
				t.Fatalf("association did not keep the item's source identifiers and correction: %#v", association)
			}
		})
	}
}

func TestRunCreateRecordsAudiobookASINCorrectionAndIgnoresISBN(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sync-state.json")
	inputPath := writeCreateInput(t, `{
		"book_id": 21,
		"asin": "B012345678",
		"isbn_13": "9780306406157",
		"abs_item_id": "item-1",
		"region": "uk",
		"reading_format": "audiobook"
	}`)
	item := testAudiobook("item-1", "B099999999")
	services := createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
			return item, nil
		},
		importAudiobook: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			if input.ASIN != "B012345678" || input.Region != "uk" {
				t.Fatalf("correction was not sent to the importer: %#v", input)
			}
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookLoaded, BookID: 21, EditionID: 34,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: "B012345678:uk",
			}, nil
		},
	}
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, ABSItemID: "item-1", StateFile: statePath,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "loaded" || !result.AssociationSaved {
		t.Fatalf("unexpected correction result: %#v", result)
	}
	loaded, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	association, ok := loaded.GetAssociation("item-1")
	if !ok || association.SourceASIN != "B099999999" || association.Correction != "B012345678" {
		t.Fatalf("source and correction identifiers were not preserved: %#v", association)
	}
}

func TestRunCreateRefusesBusyStateFileBeforeABSOrHardcover(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sync-state.json")
	lock, err := state.AcquireFileLock(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	inputPath := writeCreateInput(t, `{"book_id": 21, "asin": "B012345678", "abs_item_id": "item-1"}`)
	called := false
	services := createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
			called = true
			return nil, nil
		},
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			called = true
			return nil, nil
		},
	}
	_, err = runCreate(context.Background(), createOptions{InputPath: inputPath, StateFile: statePath}, services)
	if !errors.Is(err, state.ErrStateFileLocked) {
		t.Fatalf("expected busy state-file error, got %v", err)
	}
	if called {
		t.Fatal("busy state file reached Audiobookshelf or Hardcover")
	}
}

func TestRunCreateKeepsEbookDryRunPath(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sync-state.json")
	inputPath := writeCreateInput(t, `{"book_id":21,"title":"Ebook","isbn_13":"9780306406157","author_ids":[3],"reading_format":"ebook","abs_item_id":"item-1"}`)
	called, verified := false, false
	services := createServices{
		fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
			return testEbook("item-1", "", "9780306406157"), nil
		},
		createEbook: func(_ context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
			called = true
			if input.ReadingFormat != models.ReadingFormatEbook {
				t.Fatalf("ebook was sent with reading format %q", input.ReadingFormat)
			}
			return &edition.EditionResult{Success: true}, nil
		},
		getEditionUncached: func(context.Context, string) (*models.Edition, error) {
			verified = true
			return nil, nil
		},
	}
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, StateFile: statePath, DryRun: true,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if !called || verified || result.Status != "dry_run" || result.EditionID != 0 || result.AssociationSaved {
		t.Fatalf("unexpected ebook dry-run result: called=%v verified=%v result=%#v", called, verified, result)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("dry run unexpectedly wrote state: %v", err)
	}
}

func TestRunCreateReadsBackEbookBeforeSavingAssociation(t *testing.T) {
	tests := []struct {
		name      string
		verified  *models.Edition
		readErr   error
		wantError string
	}{
		{name: "verified", verified: &models.Edition{ID: "34", BookID: "21", ReadingFormatID: "4"}},
		{name: "wrong edition", verified: &models.Edition{ID: "35", BookID: "21", ReadingFormatID: "4"}, wantError: "identity did not match"},
		{name: "wrong book", verified: &models.Edition{ID: "34", BookID: "22", ReadingFormatID: "4"}, wantError: "identity did not match"},
		{name: "wrong reading format", verified: &models.Edition{ID: "34", BookID: "21", ReadingFormatID: "2"}, wantError: "identity did not match"},
		{name: "read failure", readErr: errors.New("Hardcover unavailable"), wantError: "failed to read back Hardcover ebook edition"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "sync-state.json")
			inputPath := writeCreateInput(t, `{"book_id":21,"title":"Ebook","isbn_13":"9780306406157","author_ids":[3],"reading_format":"ebook","abs_item_id":"item-1"}`)
			readBackCalled := false
			services := createServices{
				fetchABSItem: func(context.Context, string) (*models.AudiobookshelfBook, error) {
					return testEbook("item-1", "", "9780306406157"), nil
				},
				createEbook: func(_ context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
					if input.BookID != 21 || input.ReadingFormat != models.ReadingFormatEbook {
						t.Fatalf("unexpected ebook creation input: %#v", input)
					}
					return &edition.EditionResult{Success: true, EditionID: 34}, nil
				},
				getEditionUncached: func(_ context.Context, editionID string) (*models.Edition, error) {
					readBackCalled = true
					if editionID != "34" {
						t.Fatalf("read back edition %q, want 34", editionID)
					}
					return test.verified, test.readErr
				},
			}
			result, err := runCreate(context.Background(), createOptions{
				InputPath: inputPath, StateFile: statePath,
			}, services)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("expected %q error, got %v", test.wantError, err)
				}
				if result != nil {
					t.Fatalf("failed verification returned a result: %#v", result)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !result.AssociationSaved {
					t.Fatalf("verified ebook association was not saved: %#v", result)
				}
			}
			if !readBackCalled {
				t.Fatal("ebook edition was not read back")
			}
			loaded, loadErr := state.LoadState(statePath)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			association, exists := loaded.GetAssociation("item-1")
			if test.wantError != "" {
				if exists {
					t.Fatalf("failed verification persisted an association: %#v", association)
				}
				return
			}
			if !exists || association.HardcoverBookID != "21" || association.HardcoverEditionID != "34" || association.ReadingFormat != models.ReadingFormatEbook || association.Provenance != "cli_ebook_created" {
				t.Fatalf("unexpected saved association: %#v exists=%t", association, exists)
			}
		})
	}
}

func TestRunCreateDoesNotReadBackEbookWithoutABSAssociation(t *testing.T) {
	inputPath := writeCreateInput(t, `{"book_id":21,"title":"Ebook","isbn_13":"9780306406157","author_ids":[3],"reading_format":"ebook"}`)
	result, err := runCreate(context.Background(), createOptions{InputPath: inputPath}, createServices{
		createEbook: func(context.Context, *edition.EditionInput) (*edition.EditionResult, error) {
			return &edition.EditionResult{Success: true, EditionID: 34}, nil
		},
		getEditionUncached: func(context.Context, string) (*models.Edition, error) {
			t.Fatal("ebook without an ABS association was unexpectedly read back")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssociationSaved || result.Status != "created" || result.EditionID != 34 {
		t.Fatalf("unexpected ebook creation result: %#v", result)
	}
}

func TestRunCreateRejectsMissingIdentifier(t *testing.T) {
	inputPath := writeCreateInput(t, `{"book_id": 21, "title": "No identifier"}`)
	called := false
	services := createServices{
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			called = true
			return nil, nil
		},
	}
	_, err := runCreate(context.Background(), createOptions{InputPath: inputPath}, services)
	if err == nil || !strings.Contains(err.Error(), "ASIN or ISBN is required") {
		t.Fatalf("expected missing identifier error, got %v", err)
	}
	if called {
		t.Fatal("missing identifier reached Hardcover")
	}
}

func TestRunCreateDryRunDoesNotImportOrSave(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sync-state.json")
	inputPath := writeCreateInput(t, `{"book_id": 21, "asin": "B012345678", "abs_item_id": "item-1"}`)
	item := testAudiobook("item-1", "B012345678")
	called := false
	services := createServices{
		fetchABSItem:    func(context.Context, string) (*models.AudiobookshelfBook, error) { return item, nil },
		discoverAudible: func(context.Context, string, string) (string, error) { return "us", nil },
		importAudiobook: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			called = true
			return nil, nil
		},
	}
	result, err := runCreate(context.Background(), createOptions{
		InputPath: inputPath, StateFile: statePath, DryRun: true,
	}, services)
	if err != nil {
		t.Fatal(err)
	}
	if called || result.Status != "dry_run" || result.AssociationSaved {
		t.Fatalf("dry run was not mutation free: result=%#v importerCalled=%v", result, called)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("dry run unexpectedly wrote state: %v", err)
	}
}

func TestCreateCommandUsesConfiguredStateFileWithoutFlag(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "profile-state.json")
	lock, err := state.AcquireFileLock(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := "hardcover:\n  token: test-token\naudiobookshelf:\n  url: https://abs.example\n  token: test-token\nsync:\n  state_file: " + statePath + "\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0600); err != nil {
		t.Fatal(err)
	}
	inputPath := writeCreateInput(t, `{"book_id":21,"asin":"B012345678","abs_item_id":"item-from-json"}`)
	var stdout, stderr bytes.Buffer
	app := newApp()
	app.Writer = &stdout
	app.ErrWriter = &stderr
	// The configured state file is busy, so the command must stop at the lock,
	// before contacting Audiobookshelf, Audnex, or Hardcover.
	err = app.Run([]string{"edition", "--config", configPath, "create", "--input", inputPath})
	if !errors.Is(err, state.ErrStateFileLocked) {
		t.Fatalf("expected the configured state file to be locked, got %v", err)
	}
}

func TestFindConfigPathSupportsLongAndShortForms(t *testing.T) {
	tests := []struct {
		args         []string
		want         string
		wantExplicit bool
	}{
		{args: []string{"--config", "first.yaml", "create", "--config=last.yaml"}, want: "last.yaml", wantExplicit: true},
		{args: []string{"-c", "first.yaml", "create", "-clast.yaml"}, want: "last.yaml", wantExplicit: true},
		{args: []string{"-c=selected.yaml", "create"}, want: "selected.yaml", wantExplicit: true},
		{args: []string{"create", "--input", "edition.json"}, want: defaultConfigPath},
	}
	for _, test := range tests {
		got, explicit := findConfigPath(test.args)
		if got != test.want || explicit != test.wantExplicit {
			t.Fatalf("findConfigPath(%#v) = %q, %t; want %q, %t", test.args, got, explicit, test.want, test.wantExplicit)
		}
	}
}

func TestCreateHelpDocumentsAssociationFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := newApp()
	app.Writer = &stdout
	app.ErrWriter = &stderr
	if err := app.Run([]string{"edition", "create", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "--abs-item-id") || !strings.Contains(stdout.String(), "--state-file") {
		t.Fatalf("create help is missing association flags: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Import an audiobook") {
		t.Fatalf("create help is missing behavior description: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("help wrote unexpected stderr: %s", stderr.String())
	}
}

func writeCreateInput(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testAudiobook(id, asin string) *models.AudiobookshelfBook {
	item := &models.AudiobookshelfBook{ID: id, MediaType: "book"}
	item.Media.Duration = 60
	item.Media.Metadata.ASIN = asin
	item.Media.Metadata.ISBN = "9781234567890"
	return item
}

func testEbook(id, asin, itemISBN string) *models.AudiobookshelfBook {
	item := &models.AudiobookshelfBook{ID: id, MediaType: "ebook"}
	item.Media.EbookFormat = "epub"
	item.Media.Metadata.ASIN = asin
	item.Media.Metadata.ISBN = itemISBN
	return item
}
