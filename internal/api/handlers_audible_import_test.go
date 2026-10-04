package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	syncpkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/require"
)

type audibleImportDraftAudnexStub struct {
	getFn      func(context.Context, string, string) (*audnex.Book, error)
	discoverFn func(context.Context, string, string) (*audnex.Book, string, error)
}

func (s audibleImportDraftAudnexStub) GetBookByASIN(ctx context.Context, asin, region string) (*audnex.Book, error) {
	return s.getFn(ctx, asin, region)
}

func (s audibleImportDraftAudnexStub) DiscoverBookByASIN(ctx context.Context, asin, preferredRegion string) (*audnex.Book, string, error) {
	return s.discoverFn(ctx, asin, preferredRegion)
}

func TestGetEditionSourceDraftPreviewsCorrectedAudibleIdentifierWithoutHardcoverReads(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{
				"title":"  The Source Book ","subtitle":"ABS Subtitle","authorName":"Author A",
				"narratorName":"Narrator A","series":[{"name":"Series A","sequence":"1"}],
				"publisher":"Publisher A","publishedDate":"2020-01-01","asin":"B0SOURCE12","language":"en"
			},"duration":600,"coverPath":"abs-cover"
		}}`, "us")
	var exactCalls, discoveryCalls int
	fixture.handler.editionDraftAudnexClientFactory = func() editionDraftAudnexDiscoverer {
		return audibleImportDraftAudnexStub{
			getFn: func(_ context.Context, asin, region string) (*audnex.Book, error) {
				exactCalls++
				require.Equal(t, "B0OTHER123", asin)
				require.Equal(t, "ca", region)
				return &audnex.Book{
					ASIN: "B0OTHER123", Title: "the source book", Authors: []interface{}{"Author A"},
					Narrators: []interface{}{"Narrator B"}, SeriesPrimary: &audnex.Series{Name: "Series A"},
					PublisherName: "Publisher A", ReleaseDate: "2020-01-01", RuntimeLengthMin: 10,
					Language: "en", Image: "https://example.invalid/audnexus-cover",
				}, nil
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				discoveryCalls++
				return nil, "", nil
			},
		}
	}

	path := editionDraftItemPath + "?audible_identifier=" + url.QueryEscape("B0OTHER123:ca")
	response := fixture.request(path, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	draft := envelope.Data
	require.Equal(t, 1, exactCalls)
	require.Zero(t, discoveryCalls, "a corrected regional identifier is looked up exactly as entered")
	require.Zero(t, fixture.hardcoverRequests.Load(), "the comparison is between ABS and Audnexus only")
	require.Equal(t, "B0SOURCE12", draft.SourceIdentifiers.ASIN, "the ABS source snapshot remains unchanged")
	require.Equal(t, "B0OTHER123", draft.AudibleIdentifierCandidate.ASIN)
	require.Equal(t, "ca", draft.AudibleIdentifierCandidate.Region)
	require.Equal(t, "confirmed", draft.RegionStatus)
	require.Equal(t, "ca", draft.ConfirmedRegion)
	require.Equal(t, "B0OTHER123", draft.AudnexusRecord.ASIN)
	require.Equal(t, "the source book", draft.AudnexusRecord.Title)
	require.Equal(t, []string{"Author A"}, draft.AudnexusRecord.Authors)
	require.Equal(t, []string{"Narrator B"}, draft.AudnexusRecord.Narrators)
	require.Equal(t, 600, draft.AudnexusRecord.RuntimeSeconds)
	require.Equal(t, edition.AudnexusMatch, draft.AudnexusComparison.Title)
	require.Equal(t, edition.AudnexusMissing, draft.AudnexusComparison.Subtitle)
	require.Equal(t, edition.AudnexusMatch, draft.AudnexusComparison.Authors)
	require.Equal(t, edition.AudnexusDiffers, draft.AudnexusComparison.Narrators)
	require.Equal(t, edition.AudnexusMatch, draft.AudnexusComparison.Series)
	require.Equal(t, edition.AudnexusMissing, draft.AudnexusComparison.SeriesPosition)
	require.Equal(t, edition.AudnexusMatch, draft.AudnexusComparison.Publisher)
	require.Equal(t, edition.AudnexusMatch, draft.AudnexusComparison.ReleaseDate)
	require.Equal(t, edition.AudnexusMatch, draft.AudnexusComparison.Runtime)
	require.Equal(t, edition.AudnexusMatch, draft.AudnexusComparison.Language)
	require.Empty(t, draft.AudnexusComparison.CoverURL, "different cover URL representations are not meaningfully comparable")
	require.Equal(t, "https://example.invalid/audnexus-cover", draft.AudnexusRecord.CoverURL)
	require.NotEmpty(t, draft.MetadataPreview.CoverURL, "the ABS cover remains available in the source preview")
}

func TestCreateRegionalAudiobookAssociationUsesRecoveryTokenIssueTime(t *testing.T) {
	fixture := newAudibleImportCreateFixture(t)
	profile, err := fixture.multiUserService.GetProfile("draft-profile")
	require.NoError(t, err)
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return audibleImportDraftAudnexStub{
			getFn: func(_ context.Context, asin, region string) (*audnex.Book, error) {
				require.Equal(t, "B0OTHER123", asin)
				require.Equal(t, "ca", region)
				return &audnex.Book{ASIN: asin}, nil
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				t.Fatal("a confirmed regional identifier must not rediscover")
				return nil, "", nil
			},
		}
	}
	client := editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
		require.True(t, input.Unanchored)
		// The signed token is issued before the mutation. Keep completion in a
		// later second so using the post-mutation clock cannot accidentally pass.
		time.Sleep(1100 * time.Millisecond)
		return &hardcover.RegionalAudiobookResult{
			Status: hardcover.RegionalAudiobookCreated, BookID: 73, EditionID: 84,
			ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
			RegionalExternalID: "B0OTHER123:ca",
		}, nil
	}}
	request := editionCreateRequest{
		RunID: "run-audible-import", ABSItemID: "abs-item-1", AudnexusConfirmed: true,
		AudibleIdentifier: "B0OTHER123:ca",
	}
	response := editionCreateResponse{action: &syncpkg.EditionActionRecord{
		Outcome: editionOutcomeNotSubmitted, SubmittedBody: submittedEditionActionBody(request),
	}}
	item := audibleImportABSItem("Reviewed title", "B0SOURCE12", "978-0-306-40615-7")
	association, err := fixture.handler.createRegionalAudiobook(context.Background(), profile, item, audibleImportRunRecord(), request, client, &response)
	require.NoError(t, err)
	require.NotNil(t, response.recovery)
	claims, valid := verifyEditionRecoveryToken(profile.HardcoverToken, response.recovery.RecoveryToken, editionRecoveryClaims{
		ProfileID: "draft-profile", RunID: "run-audible-import", ABSItemID: "abs-item-1",
		AudibleIdentifier: "B0OTHER123:ca",
	})
	require.True(t, valid, "the recovery token must be valid for the attempted import")
	require.Equal(t, time.Unix(claims.IssuedAt, 0).UTC(), association.AudnexusConfirmedAt,
		"the association timestamp must preserve the signed Audnexus confirmation time")
}

func TestGetEditionSourceDraftDoesNotOfferUnconfirmedAudibleRecord(t *testing.T) {
	tests := []struct {
		name          string
		lookupErr     error
		found         *audnex.Book
		region        string
		wantStatus    string
		wantRetryable bool
		wantWarning   string
	}{
		{
			name:       "unknown region",
			wantStatus: "unknown",
			// No result without an error means discovery found no supported
			// regional record.
		},
		{
			name:          "rate limited exact region",
			lookupErr:     audnex.ErrRateLimited,
			wantStatus:    "temporarily_unavailable",
			wantRetryable: true,
		},
		{
			name:        "non-retryable discovery error",
			lookupErr:   errors.New("unexpected Audnexus response"),
			wantStatus:  "unknown",
			wantWarning: "audnex_lookup_failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"ABS title","asin":"B0SOURCE12"},"duration":100}}`, "us")
			var exactCalls, discoveryCalls int
			fixture.handler.editionDraftAudnexClientFactory = func() editionDraftAudnexDiscoverer {
				return audibleImportDraftAudnexStub{
					getFn: func(context.Context, string, string) (*audnex.Book, error) {
						exactCalls++
						return tt.found, tt.lookupErr
					},
					discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
						discoveryCalls++
						return tt.found, tt.region, tt.lookupErr
					},
				}
			}
			path := editionDraftItemPath
			if tt.name == "rate limited exact region" {
				path += "?audible_identifier=" + url.QueryEscape("B0OTHER123:ca")
			}
			response := fixture.request(path, fixture.sessionCookie(t, fixture.owner))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var envelope struct {
				Data editionDraftResponse `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Equal(t, tt.wantStatus, envelope.Data.RegionStatus)
			require.Nil(t, envelope.Data.AudnexusRecord)
			require.Nil(t, envelope.Data.AudnexusComparison)
			require.Empty(t, envelope.Data.ConfirmedRegion)
			require.Zero(t, fixture.hardcoverRequests.Load())
			if tt.wantWarning != "" {
				warning := editionDraftWarningByCode(envelope.Data, tt.wantWarning)
				require.NotNil(t, warning)
				require.False(t, warning.Retryable)
			}
			if tt.wantRetryable {
				var retryWarning *editionDraftWarning
				for i := range envelope.Data.Warnings {
					if envelope.Data.Warnings[i].Code == "audnex_temporarily_unavailable" {
						retryWarning = &envelope.Data.Warnings[i]
						break
					}
				}
				require.NotNil(t, retryWarning)
				require.True(t, retryWarning.Retryable)
				require.Equal(t, 1, exactCalls)
				require.Zero(t, discoveryCalls)
			} else {
				require.Equal(t, 1, discoveryCalls)
				require.Zero(t, exactCalls)
			}
		})
	}
}

func TestCreateUnanchoredAudibleImportRequiresExplicitConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "confirmation omitted",
			body: `{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audible_identifier":"B0OTHER123:ca"}`,
		},
		{
			name: "regional identifier omitted",
			body: `{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true}`,
		},
		{
			name: "explicitly unconfirmed identifier",
			body: `{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":false,"audible_identifier":"B0OTHER123:ca"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAudibleImportCreateFixture(t)
			var absReads, audnexReads, mutations int
			fixture.handler.editionCreateABSClientFactory = func(string, string, string) (editionCreateABSClient, error) {
				absReads++
				return editionCreateABSClientFunc(func(context.Context, string) (*models.AudiobookshelfBook, error) {
					return audibleImportABSItem("Reviewed title", "B0SOURCE12", "978-0-306-40615-7"), nil
				}), nil
			}
			fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
				return editionCreateAudnexStub{
					getFn: func(context.Context, string, string) (*audnex.Book, error) {
						audnexReads++
						return &audnex.Book{ASIN: "B0OTHER123"}, nil
					},
					discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
						audnexReads++
						return nil, "", nil
					},
				}
			}
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					mutations++
					return nil, nil
				}}
			}

			response := postEditionCreate(t, fixture, fixture.owner, tc.body)

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Zero(t, absReads)
			require.Zero(t, audnexReads)
			require.Zero(t, mutations)
			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			_, associated := stored.GetAssociation("abs-item-1")
			require.False(t, associated)
		})
	}
}

func TestCreateUnanchoredAudibleImportRefusesDryRunWithConfirmedIdentifier(t *testing.T) {
	fixture := newAudibleImportCreateFixture(t)
	profile, err := fixture.multiUserService.GetProfile("draft-profile")
	require.NoError(t, err)
	profile.SyncConfig.DryRun = true
	require.NoError(t, fixture.multiUserService.UpdateProfileConfig(
		profile.Profile.ID, profile.AudiobookshelfURL, profile.AudiobookshelfToken, profile.HardcoverToken, profile.SyncConfig,
	))
	var audnexFactoryCalls, hardcoverFactoryCalls, mutations int
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		audnexFactoryCalls++
		return editionCreateAudnexStub{}
	}
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		hardcoverFactoryCalls++
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutations++
			return nil, nil
		}}
	}

	response := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":"B0OTHER123:ca"}`)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "dry run")
	require.Zero(t, fixture.absRequests.Load(), "dry-run refusal happens before rereading the ABS source")
	require.Zero(t, fixture.hardcoverRequests.Load())
	require.Zero(t, audnexFactoryCalls)
	require.Zero(t, hardcoverFactoryCalls)
	require.Zero(t, mutations)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, associated := stored.GetAssociation("abs-item-1")
	require.False(t, associated)
}

func TestCreateUnanchoredAudibleImportRejectsMalformedConfirmedIdentifierBeforeMutation(t *testing.T) {
	fixture := newAudibleImportCreateFixture(t)
	var absReads, audnexReads, mutations int
	fixture.handler.editionCreateABSClientFactory = func(string, string, string) (editionCreateABSClient, error) {
		absReads++
		return editionCreateABSClientFunc(func(context.Context, string) (*models.AudiobookshelfBook, error) {
			return audibleImportABSItem("Reviewed title", "B0SOURCE12", "978-0-306-40615-7"), nil
		}), nil
	}
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		audnexReads++
		return editionCreateAudnexStub{}
	}
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutations++
			return nil, nil
		}}
	}

	response := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":"B0OTHER123:not-a-region"}`)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	require.Equal(t, 1, absReads, "the current source snapshot is checked before validating the submitted correction")
	require.Zero(t, audnexReads)
	require.Zero(t, mutations)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, associated := stored.GetAssociation("abs-item-1")
	require.False(t, associated)
}

func TestCreateUnanchoredAudibleImportRejectsStaleSourceBeforeRemoteReads(t *testing.T) {
	fixture := newAudibleImportCreateFixture(t)
	var audnexReads, mutations int
	fixture.handler.editionCreateABSClientFactory = func(string, string, string) (editionCreateABSClient, error) {
		return editionCreateABSClientFunc(func(context.Context, string) (*models.AudiobookshelfBook, error) {
			return audibleImportABSItem("Changed title", "B0SOURCE12", "978-0-306-40615-7"), nil
		}), nil
	}
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{
			getFn: func(context.Context, string, string) (*audnex.Book, error) {
				audnexReads++
				return &audnex.Book{ASIN: "B0OTHER123"}, nil
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				return nil, "", nil
			},
		}
	}
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutations++
			return nil, nil
		}}
	}

	response := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":"B0OTHER123:ca"}`)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Zero(t, audnexReads, "changed ABS metadata rejects the create before the confirmed-region lookup")
	require.Zero(t, mutations)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, associated := stored.GetAssociation("abs-item-1")
	require.False(t, associated)
}

func TestCreateUnanchoredAudibleImportRejectsChangedNewerCandidateBeforeRemoteReads(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*syncpkg.BookOutcomeRecord)
		wantStatus int
	}{
		{
			name: "newer source ASIN changed",
			mutate: func(record *syncpkg.BookOutcomeRecord) {
				record.SourceASIN = "B0OTHER456"
			},
			wantStatus: http.StatusConflict,
		},
		{
			name: "newer source ISBN-13 changed",
			mutate: func(record *syncpkg.BookOutcomeRecord) {
				record.SourceISBN13 = "9780140328721"
			},
			wantStatus: http.StatusConflict,
		},
		{name: "unchanged newer candidate remains usable", wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAudibleImportCreateFixture(t)
			newerRecord := audibleImportRunRecord()
			if test.mutate != nil {
				test.mutate(&newerRecord)
			}
			addCompletedNeedsReviewRun(t, fixture, "run-audible-import-newer", newerRecord)

			var absReads, audnexReads, mutations int
			fixture.handler.editionCreateABSClientFactory = func(string, string, string) (editionCreateABSClient, error) {
				return editionCreateABSClientFunc(func(context.Context, string) (*models.AudiobookshelfBook, error) {
					absReads++
					return audibleImportABSItem("Reviewed title", "B0SOURCE12", "978-0-306-40615-7"), nil
				}), nil
			}
			fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
				return editionCreateAudnexStub{
					getFn: func(_ context.Context, asin, _ string) (*audnex.Book, error) {
						audnexReads++
						return &audnex.Book{ASIN: asin}, nil
					},
					discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
						audnexReads++
						return nil, "", nil
					},
				}
			}
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					mutations++
					return &hardcover.RegionalAudiobookResult{
						Status: hardcover.RegionalAudiobookCreated, BookID: 73, EditionID: 84,
						ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
						RegionalExternalID: input.ASIN + ":" + input.Region,
					}, nil
				}}
			}

			response := postEditionCreate(t, fixture, fixture.owner,
				`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":"B0OTHER123:ca"}`)

			require.Equal(t, test.wantStatus, response.Code, response.Body.String())
			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			_, associated := stored.GetAssociation("abs-item-1")
			if test.wantStatus == http.StatusConflict {
				require.Zero(t, absReads)
				require.Zero(t, audnexReads)
				require.Zero(t, fixture.hardcoverRequests.Load())
				require.Zero(t, mutations)
				require.False(t, associated)
				return
			}
			require.Equal(t, 1, absReads)
			require.Equal(t, 1, audnexReads)
			require.Equal(t, 1, mutations)
			require.True(t, associated)
			require.Equal(t, 1, mutations)
			require.True(t, associated)
		})
	}
}

func TestCreateUnanchoredAudibleImportUsesConfirmedRegionAndPersistsAssociation(t *testing.T) {
	for _, status := range []hardcover.RegionalAudiobookStatus{hardcover.RegionalAudiobookLoaded, hardcover.RegionalAudiobookCreated} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newAudibleImportCreateFixture(t)
			var gotASIN, gotRegion string
			var exactReads, discoveryReads, mutations int
			fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
				return editionCreateAudnexStub{
					getFn: func(_ context.Context, asin, region string) (*audnex.Book, error) {
						exactReads++
						gotASIN, gotRegion = asin, region
						return &audnex.Book{ASIN: asin, Title: "Confirmed title", ReleaseDate: "2024-05-06"}, nil
					},
					discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
						discoveryReads++
						return nil, "", nil
					},
				}
			}
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					mutations++
					require.Zero(t, input.BookID, "unanchored import must not inherit a Hardcover ID from the run")
					require.True(t, input.Unanchored)
					require.Equal(t, "B0OTHER123", input.ASIN)
					require.Equal(t, "ca", input.Region)
					return &hardcover.RegionalAudiobookResult{
						Status: status, BookID: 73, EditionID: 84,
						ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
						RegionalExternalID: "B0OTHER123:ca", BookTitle: "Imported Hardcover title",
					}, nil
				}}
			}

			response := postEditionCreate(t, fixture, fixture.owner,
				`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":" b0other123 : CA "}`)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, 1, exactReads)
			require.Equal(t, "B0OTHER123", gotASIN)
			require.Equal(t, "ca", gotRegion)
			require.Zero(t, discoveryReads, "create re-reads exactly the confirmed region")
			require.Equal(t, 1, mutations)
			var envelope struct {
				Data struct {
					Status             string `json:"status"`
					HardcoverBookID    string `json:"hardcover_book_id"`
					HardcoverEditionID string `json:"hardcover_edition_id"`
					HardcoverTitle     string `json:"hardcover_title"`
					RegionalExternalID string `json:"regional_external_id"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Equal(t, string(status), envelope.Data.Status)
			require.Equal(t, "73", envelope.Data.HardcoverBookID)
			require.Equal(t, "84", envelope.Data.HardcoverEditionID)
			require.Equal(t, "Imported Hardcover title", envelope.Data.HardcoverTitle)
			require.Equal(t, "B0OTHER123:ca", envelope.Data.RegionalExternalID)

			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			association, exists := stored.GetAssociation("abs-item-1")
			require.True(t, exists)
			require.Equal(t, "audible_import_unanchored", association.Provenance)
			require.Equal(t, "B0SOURCE12", association.SourceASIN)
			require.Equal(t, "B0OTHER123:ca", association.RegionalExternalID)
			require.Equal(t, "B0OTHER123:ca", association.Correction)
			require.Equal(t, "ca", association.AudnexusConfirmedRegion)
			require.False(t, association.AudnexusConfirmedAt.IsZero())
			require.Equal(t, "73", association.HardcoverBookID)
			require.Equal(t, "84", association.HardcoverEditionID)
			_, journalExists, err := fixture.multiUserService.GetEditionAction("draft-profile", "run-audible-import", "abs-item-1")
			require.NoError(t, err)
			require.False(t, journalExists, "a saved association clears its run-scoped action journal")

			details := fixture.request("/api/profiles/draft-profile/runs/run-audible-import/details", fixture.sessionCookie(t, fixture.owner))
			require.Equal(t, http.StatusOK, details.Code, details.Body.String())
			var detailsEnvelope struct {
				Data syncpkg.SyncSnapshot `json:"data"`
			}
			require.NoError(t, json.Unmarshal(details.Body.Bytes(), &detailsEnvelope))
			require.Len(t, detailsEnvelope.Data.BookOutcomes, 1)
			require.True(t, detailsEnvelope.Data.BookOutcomes[0].EditionAdded,
				"run details must recognize the saved unanchored association after its action journal is cleared")
		})
	}
}

func TestCreateUnanchoredAudibleImportHandlesAudnexMissAndRateLimitWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		getErr     error
		found      *audnex.Book
		wantStatus int
	}{
		{name: "confirmed record disappeared", getErr: audnex.ErrNotFound, wantStatus: http.StatusConflict},
		{name: "confirmed record returned a different ASIN", found: &audnex.Book{ASIN: "B0WRONG123"}, wantStatus: http.StatusConflict},
		{name: "audnexus rate limited", getErr: audnex.ErrRateLimited, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAudibleImportCreateFixture(t)
			var exactReads, discoveryReads, mutations int
			fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
				return editionCreateAudnexStub{
					getFn: func(_ context.Context, asin, region string) (*audnex.Book, error) {
						exactReads++
						require.Equal(t, "B0OTHER123", asin)
						require.Equal(t, "ca", region)
						if tc.found != nil {
							return tc.found, tc.getErr
						}
						return nil, tc.getErr
					},
					discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
						discoveryReads++
						return nil, "", nil
					},
				}
			}
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					mutations++
					return nil, nil
				}}
			}

			response := postEditionCreate(t, fixture, fixture.owner,
				`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":"B0OTHER123:ca"}`)
			require.Equal(t, tc.wantStatus, response.Code, response.Body.String())
			require.Equal(t, 1, exactReads)
			require.Zero(t, discoveryReads)
			require.Zero(t, mutations)
			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			_, associated := stored.GetAssociation("abs-item-1")
			require.False(t, associated)
		})
	}
}

func TestCreateUnanchoredAudibleImportDoesNotSaveConflictingHardcoverIdentity(t *testing.T) {
	fixture := newAudibleImportCreateFixture(t)
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{
			getFn: func(_ context.Context, asin, _ string) (*audnex.Book, error) {
				return &audnex.Book{ASIN: asin}, nil
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				t.Fatal("the confirmed region should be reread exactly")
				return nil, "", nil
			},
		}
	}
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			require.True(t, input.Unanchored)
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookLoaded, BookID: 73, EditionID: 84,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatEbook),
				RegionalExternalID: "B0OTHER123:ca",
			}, nil
		}}
	}

	response := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":"B0OTHER123:ca"}`)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, associated := stored.GetAssociation("abs-item-1")
	require.False(t, associated, "a wrong-format result must never become the local Audible association")
}

func TestCheckUnanchoredAudibleImportRecoveryIsIdempotentWithoutResubmitting(t *testing.T) {
	fixture := newAudibleImportCreateFixture(t)
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{
			getFn: func(_ context.Context, asin, region string) (*audnex.Book, error) {
				require.Equal(t, "B0OTHER123", asin)
				require.Equal(t, "ca", region)
				return &audnex.Book{ASIN: asin}, nil
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				t.Fatal("a confirmed Audible identifier must not repeat region discovery")
				return nil, "", nil
			},
		}
	}
	var mutations, checks int
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutations++
			require.Zero(t, input.BookID)
			require.True(t, input.Unanchored)
			return nil, hardcover.ErrRegionalAudiobookImportTimeout
		}}
	}
	created := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":" b0other123 : CA "}`)
	require.Equal(t, http.StatusServiceUnavailable, created.Code, created.Body.String())
	var createEnvelope struct {
		Data struct {
			AudibleIdentifier string `json:"audible_identifier"`
			HardcoverBookID   string `json:"hardcover_book_id"`
			RecoveryToken     string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createEnvelope))
	require.Equal(t, "B0OTHER123:ca", createEnvelope.Data.AudibleIdentifier)
	require.Empty(t, createEnvelope.Data.HardcoverBookID)
	require.NotEmpty(t, createEnvelope.Data.RecoveryToken)
	require.Equal(t, 1, mutations)
	profile, err := fixture.multiUserService.GetProfile("draft-profile")
	require.NoError(t, err)
	claims, valid := verifyEditionRecoveryToken(profile.HardcoverToken, createEnvelope.Data.RecoveryToken, editionRecoveryClaims{
		ProfileID: "draft-profile", RunID: "run-audible-import", ABSItemID: "abs-item-1",
		AudibleIdentifier: "B0OTHER123:ca",
	})
	require.True(t, valid, "the timeout token must contain the signed confirmation time")
	require.Equal(t, "B0OTHER123:ca", claims.Correction, "new recovery tokens must bind the canonical correction")
	legacyCorrection := " b0other123 : CA "
	legacyToken := signEditionRecoveryTokenAt(profile.HardcoverToken, editionRecoveryClaims{
		ProfileID: "draft-profile", RunID: "run-audible-import", ABSItemID: "abs-item-1",
		AudibleIdentifier: "B0OTHER123:ca", Correction: legacyCorrection,
	}, time.Now().UTC())
	legacyClaims, legacyValid := verifyEditionRecoveryToken(profile.HardcoverToken, legacyToken, editionRecoveryClaims{
		ProfileID: "draft-profile", RunID: "run-audible-import", ABSItemID: "abs-item-1",
		AudibleIdentifier: "B0OTHER123:ca",
	})
	require.True(t, legacyValid)
	require.Equal(t, legacyCorrection, legacyClaims.Correction)
	storedAction, found, err := fixture.multiUserService.GetEditionAction("draft-profile", "run-audible-import", "abs-item-1")
	require.NoError(t, err)
	require.True(t, found)
	storedAction.Data.RecoveryToken = legacyToken
	require.NoError(t, fixture.multiUserService.SaveEditionAction("draft-profile", "run-audible-import", "abs-item-1", *storedAction))

	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{checkFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
			checks++
			require.Zero(t, input.BookID, "recovery must derive the resolved book from Hardcover status")
			require.True(t, input.Unanchored)
			require.Equal(t, "B0OTHER123", input.ASIN)
			require.Equal(t, "ca", input.Region)
			if checks == 1 {
				// Recovery can happen well after the signed exact-region lookup.
				time.Sleep(1100 * time.Millisecond)
			}
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookCreated, BookID: 73, EditionID: 84,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: "B0OTHER123:ca", BookTitle: "Imported Hardcover title",
			}, true, nil
		}}
	}
	checkBody, err := json.Marshal(map[string]string{
		"run_id": "run-audible-import", "abs_item_id": "abs-item-1",
		"audible_identifier": " b0other123 : CA ",
		"recovery_token":     legacyToken,
	})
	require.NoError(t, err)
	checked := postEditionImportCheck(t, fixture, fixture.owner, string(checkBody))
	require.Equal(t, http.StatusOK, checked.Code, checked.Body.String())
	require.Equal(t, 1, checks)
	require.Equal(t, 1, mutations, "status recovery must never resubmit the Hardcover mutation")
	var checkEnvelope struct {
		Data struct {
			HardcoverBookID    string `json:"hardcover_book_id"`
			HardcoverEditionID string `json:"hardcover_edition_id"`
			HardcoverTitle     string `json:"hardcover_title"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(checked.Body.Bytes(), &checkEnvelope))
	require.Equal(t, "73", checkEnvelope.Data.HardcoverBookID)
	require.Equal(t, "84", checkEnvelope.Data.HardcoverEditionID)
	require.Equal(t, "Imported Hardcover title", checkEnvelope.Data.HardcoverTitle)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "audible_import_unanchored", association.Provenance)
	require.Equal(t, "B0SOURCE12", association.SourceASIN)
	require.Equal(t, "B0OTHER123:ca", association.RegionalExternalID)
	require.Equal(t, "B0OTHER123:ca", association.Correction)
	require.Equal(t, "73", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
	confirmedAt := association.AudnexusConfirmedAt
	require.Equal(t, time.Unix(legacyClaims.IssuedAt, 0).UTC(), confirmedAt,
		"recovered provenance must use the original signed Audnexus confirmation time")

	checkedAgain := postEditionImportCheck(t, fixture, fixture.owner, string(checkBody))
	require.Equal(t, http.StatusOK, checkedAgain.Code, checkedAgain.Body.String())
	require.Equal(t, 2, checks, "each retry must verify the existing Hardcover import")
	require.Equal(t, 1, mutations, "recovery retries must never resubmit the import mutation")
	storedAgain, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	associationAgain, exists := storedAgain.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, confirmedAt, associationAgain.AudnexusConfirmedAt, "a retry must preserve the originally saved confirmation time")
	require.Equal(t, association, associationAgain, "an idempotent retry must preserve the existing association provenance and identity")
}

func TestCreateUnanchoredAudibleImportRecoversWhenLocalAssociationSaveFails(t *testing.T) {
	fixture := newAudibleImportCreateFixture(t)
	statePath := editionCreateProfileStatePath(fixture)
	var importCalls, checkCalls, audnexReads atomic.Int32
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{
			getFn: func(_ context.Context, asin, region string) (*audnex.Book, error) {
				audnexReads.Add(1)
				require.Equal(t, "B0OTHER123", asin)
				require.Equal(t, "ca", region)
				return &audnex.Book{ASIN: asin}, nil
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				t.Fatal("confirmed-region import must not rediscover after a local save failure")
				return nil, "", nil
			},
		}
	}
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
				importCalls.Add(1)
				require.Zero(t, input.BookID)
				require.True(t, input.Unanchored)
				// Simulate the association store becoming unwritable after the
				// remote Hardcover operation has returned a verified result.
				require.NoError(t, os.Mkdir(statePath, 0700))
				return &hardcover.RegionalAudiobookResult{
					Status: hardcover.RegionalAudiobookCreated, BookID: 73, EditionID: 84,
					ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
					RegionalExternalID: "B0OTHER123:ca", BookTitle: "Imported Hardcover title",
				}, nil
			},
			checkFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
				checkCalls.Add(1)
				require.Zero(t, input.BookID)
				require.True(t, input.Unanchored)
				return &hardcover.RegionalAudiobookResult{
					Status: hardcover.RegionalAudiobookCreated, BookID: 73, EditionID: 84,
					ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
					RegionalExternalID: "B0OTHER123:ca", BookTitle: "Imported Hardcover title",
				}, true, nil
			},
		}
	}

	created := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audnexus_confirmed":true,"audible_identifier":"B0OTHER123:ca"}`)
	require.Equal(t, http.StatusBadGateway, created.Code, created.Body.String())
	require.Contains(t, created.Body.String(), "Hardcover returned a verified edition, but the local match could not be saved")
	require.Contains(t, created.Body.String(), "retrying may create another edition")
	require.EqualValues(t, 1, importCalls.Load(), "one remote mutation succeeded before the local persistence failure")
	var createEnvelope struct {
		Data struct {
			AudibleIdentifier string `json:"audible_identifier"`
			RecoveryToken     string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createEnvelope))
	require.Equal(t, "B0OTHER123:ca", createEnvelope.Data.AudibleIdentifier)
	require.NotEmpty(t, createEnvelope.Data.RecoveryToken, "a confirmed remote result can be checked without resubmitting it")
	require.NoError(t, os.Remove(statePath))
	unsaved, err := statepkg.LoadState(statePath)
	require.NoError(t, err)
	_, associated := unsaved.GetAssociation("abs-item-1")
	require.False(t, associated, "failed local persistence must not report a saved association")

	details := fixture.request("/api/profiles/draft-profile/runs/run-audible-import/details", fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, details.Code, details.Body.String())
	var detailsEnvelope struct {
		Data syncpkg.SyncSnapshot `json:"data"`
	}
	require.NoError(t, json.Unmarshal(details.Body.Bytes(), &detailsEnvelope))
	require.Len(t, detailsEnvelope.Data.BookOutcomes, 1)
	projectedAction := detailsEnvelope.Data.BookOutcomes[0].EditionAction
	require.NotNil(t, projectedAction)
	require.Equal(t, "73", projectedAction.Data.HardcoverBookID, "the action stores the resolved ID while the token remains bound to the source run's empty ID")
	require.Equal(t, createEnvelope.Data.RecoveryToken, projectedAction.Data.RecoveryToken,
		"run-details projection must validate an unanchored token against the run's original book ID")

	mismatchedRequest := postEditionImportCheck(t, fixture, fixture.owner, fmt.Sprintf(
		`{"run_id":"run-audible-import","abs_item_id":"abs-item-1","audible_identifier":"B0OTHER123:uk","recovery_token":%q}`,
		createEnvelope.Data.RecoveryToken))
	require.Equal(t, http.StatusConflict, mismatchedRequest.Code, mismatchedRequest.Body.String())
	storedAction, found, err := fixture.multiUserService.GetEditionAction("draft-profile", "run-audible-import", "abs-item-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, createEnvelope.Data.RecoveryToken, storedAction.Data.RecoveryToken,
		"a mismatched recovery request must not revoke the valid stored token")

	checkBody, err := json.Marshal(map[string]string{
		"run_id": "run-audible-import", "abs_item_id": "abs-item-1",
		"audible_identifier": createEnvelope.Data.AudibleIdentifier,
		"recovery_token":     createEnvelope.Data.RecoveryToken,
	})
	require.NoError(t, err)
	checked := postEditionImportCheck(t, fixture, fixture.owner, string(checkBody))
	require.Equal(t, http.StatusOK, checked.Code, checked.Body.String())
	require.EqualValues(t, 1, importCalls.Load(), "status recovery saves the match without replaying the create mutation")
	require.EqualValues(t, 1, checkCalls.Load())
	require.EqualValues(t, 1, audnexReads.Load(), "status recovery reuses the signed confirmed identifier and makes no Audnexus or discovery request")
	saved, err := statepkg.LoadState(statePath)
	require.NoError(t, err)
	association, exists := saved.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "audible_import_unanchored", association.Provenance)
	require.Equal(t, "73", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
}

func newAudibleImportCreateFixture(t *testing.T) *editionDraftTestFixture {
	t.Helper()
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"978-0-306-40615-7"},"duration":600}}`, "us")
	configureEditionCreateRoute(t, fixture)
	setAudibleImportABSItem(fixture, audibleImportABSItem("Reviewed title", "B0SOURCE12", "978-0-306-40615-7"))
	addCompletedNeedsReviewRun(t, fixture, "run-audible-import", audibleImportRunRecord())
	return fixture
}

func audibleImportRunRecord() syncpkg.BookOutcomeRecord {
	return syncpkg.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: syncpkg.OutcomeNeedsReview, Reason: mismatch.ReasonAudibleImportAvailable,
		Title: "Reviewed title", Author: "Author", Format: "Audiobook", ASIN: "B0SOURCE12",
		SourceASIN: "B0SOURCE12", ISBN: "978-0-306-40615-7", SourceISBN13: "9780306406157",
	}
}

func audibleImportABSItem(title, asin, isbn string) *models.AudiobookshelfBook {
	item := &models.AudiobookshelfBook{ID: "abs-item-1", MediaType: "book"}
	item.Media.Metadata.Title = title
	item.Media.Metadata.AuthorName = "Author"
	item.Media.Metadata.ASIN = asin
	item.Media.Metadata.ISBN = isbn
	item.Media.NumTracks = 1
	item.Media.Duration = 600
	return item
}

func setAudibleImportABSItem(fixture *editionDraftTestFixture, item *models.AudiobookshelfBook) {
	fixture.handler.editionCreateABSClientFactory = func(string, string, string) (editionCreateABSClient, error) {
		return editionCreateABSClientFunc(func(context.Context, string) (*models.AudiobookshelfBook, error) {
			return item, nil
		}), nil
	}
}
