# Repository test-suite optimization

Branch: `test/suite-optimizer`. Scope: all discovered Go test packages, the Node web suite, shared fixtures, and the Helm chart smoke script. Application behavior and dependencies were unchanged.

The suite-proportionality and dedicated brittle-test gates each included a second audit after cleanup. A final discovery pass checked for further safe reductions, duplicate bodies, source inspection, private structure, exact logging, and presentation-only assertions. Large retained clusters protect durable reading state, real external writes, authorization, network trust, and concurrency; coverage percentages were not a retention target.

## Cleanup decisions

- Removed the legacy testutils replica suite and unused model/helper copies; only the consumed process-global logger helper remains. Those tests exercised their own stubs, literal data, or copied arithmetic rather than production code.
- Removed duplicate configuration, DTO, logger, and hand-built semaphore tests. Combined repeated Audnex retry cases and simplified single-case Audiobookshelf fixtures.
- Replaced constructor/private-field checks with requests, decoding, headers, and retry behavior. Narrowed edition lookup assertions from call order to normalized identifiers and actual creation outcomes.
- Removed fixed logging-message/level checks; retained admission diagnostics tied to query payloads and zero/one actual HTTP requests, plus the sync daily-quota diagnostic behavior described below.
- Retained the full cover-upload safety matrix in `creator_cover_test.go`: opt-in upload success and failure paths, image-format and size checks, Authorization scoping, plus disabled-mode no-request behavior. `EnableCoverUpload` has no production caller; the code documents its upload endpoint as outside Hardcover's documented API. Credential scoping, redirects, address policy, and TLS checks remain covered in `creator_token_test.go` and `creator_redirect_test.go`.
- Narrowed web presentation assertions to visible labels, semantic regions, field provenance, and linked action targets. Retained action attributes, input names, escaping, and the `.edition-form` selector actually used to collect edits.

## Scope and validation limits

The full gate is `make test-all test-web lint build`, using Go 1.26.8. `test-all` includes all discovered packages and uses the race detector with atomic coverage; the web suite has 75 tests. Tests use local HTTP fixtures, not external service calls. The final full gate passed on the final shared worktree.

The Helm smoke script remains a small validator of deployment artifacts, without a fake orchestration system. `bash -n helm/test-chart.sh` passed. Helm is unavailable on this host, so chart lint/render checks were not run. Docker and live Kubernetes validation were not used.

Package discovery also identified packages without standalone Go tests: `cmd/edition-tool`, `cmd/hardcover-lookup`, `internal/auth`, `internal/cache`, `internal/crypto`, `internal/database/migrations`, and the remaining `internal/testutils` helper. The optimization did not invent suites for these areas; they remain included in package compilation and relevant caller tests.

Counts are physical Go test-file lines and named top-level `Test*` declarations, not runnable cases or coverage estimates. Subsystem snapshots below document audit checkpoints; final repository counts supersede earlier snapshots.

## Final results

| Metric | Step 11 base (`31c2d24`) | Final suite |
|---|---:|---:|
| Go test files | 112 | 86 |
| Physical Go test-file lines | 42,762 | 37,813 |
| Named top-level Go `Test*` declarations | 828 | 714 |
| Passing Node tests | 75 | 75 |

Net reduction: 4,949 Go test lines, plus 902 lines of unused replica helpers/models. Counts do not imply equivalent coverage; removed replica coverage was deliberately judged disproportionate. No runtime-speed claim is made from cached final runs.

The final `make test-all test-web lint build` gate passed after all test changes, with Go 1.26.8 and all 75 web tests passing. Final discovery found no equivalent top-level Go test bodies using parsed/token-normalized bodies; structural candidate scans and the detailed second audits below found no further safe cleanup. Both mandatory gates pass with second no-op audits.

## Detailed cluster and brittle-candidate ledgers

---

## Legacy testutils: proportionality and brittle-test audit

Inventory: 25 test files, 95 top-level tests and 3 benchmarks, 4,135 test lines;
902 lines of unused replica helpers/models. Every test imports only the Go
standard library. None calls a production package. Repository-wide references
show only SetGlobalLogLevel is imported outside testutils (sync, util, Hardcover
client tests). The package README expressly described simplified/hardcoded copies.

| Cluster / files | Claimed protection | Actual observable failure, impact and recovery | Cost / disposition |
|---|---|---|---|
| main_test.go | ABS fetching, sync, reruns, dry run | Hardcoded local stubs, no-op log-only tests, copied threshold math; cannot catch application regressions | 754 lines, env pollution; remove |
| unit_conversion*, demo_unit_conversion, validation, progress_multiplication, progress_detection, status_determination | Progress integrity and finished-state handling | Only local replicas/arithmetic; production changes cannot fail these tests; some comparison conditions are impossible | 1,016 lines plus replicas; remove |
| data_loss_fix, reading_history_fix, edition_field_fix | Mutation field preservation, reading history | Test-constructed maps/structs and standard-library date formatting; no production mutation | 389 lines; remove |
| owned, owned_flag, want_to_read | Ownership and unstarted-book handling | Local environment parsers/copied branches; no actual Owned-list boundary | 357 lines; remove |
| cache, lookup, bulk_lookup, publisher_data_type, book_deduplication | Lookup, caching, publisher IDs, canonical IDs | Replica cache, literal validation, log-only and unconditional skipped cases; no client request | 862 lines, sleeps, goroutines, 3 benchmarks; remove |
| edition_creator, json_parsing | DTO validation, edition creation and JSON transport | Replica DTOs and hardcoded IDs; tests stdlib marshal or fixture wiring | 533 lines plus replica types; remove |
| format, image_duplicate, local_image_handler, local_url_integration | Formatting, local URL policy, image deduplication | Replica regex/formatting and always-success upload stubs; no production URL/security validation | 224 lines; remove |
| log_level.go | Shared logger isolation | Actual shared test helper, mutex protects process-global state and cleanup restores it | 27 lines; retain unchanged |

All removed clusters are test-only scaffolding, not supported product behavior.
There is no production confidence to preserve: testing more replica branches or
adding replacement assertions would deepen the same failure. Meaningful product
contracts remain tested in their owning packages. Removed unused test_helpers.go
and test_models.go after checking external references, keeping log_level.go.
Updated testutils README to document its remaining helper.

Dedicated brittle inventory: all 25 files removed. Specific misleading candidates
include book_deduplication log-only implementation claims; publisher_data_type
logging a literal GraphQL type; data_loss/edition_field maps constructed inside
tests; reading_history dates generated and reparsed in the test; replica cache
private stats/wiring; main placeholder success and skipped pseudo-integration
tests. None inspects or exercises a public/generated/security artifact; there is
no exact-contract justification for retention.

Second proportionality audit: the sole remaining executable file is log_level.go,
used at real tests in three packages; no replica suite remains. No further safe
reduction.
Second dedicated brittle audit: testutils has no test cases or replica types;
README describes only the consumed helper. No remaining brittle candidate.
Final discovery: no further cleanup in this scope. AST duplicate scan of the
remaining repository found no byte-identical top-level test bodies; behavioral
near-duplicates are handled by subsystem audits.

---

# API test-suite optimization audit

Scope: `internal/api/hardcover`, `internal/api/audiobookshelf`, and `internal/api/audnex` tests only. Production files were unchanged. Local HTTP fixtures only; no external service or Docker use.

## Proportionality inventory and second audit

| Test cluster | Protected behavior, importance, impact and recovery | Cost and disposition after second audit |
| --- | --- | --- |
| Hardcover client setup and transport: `client_config_test.go`, `transport_test.go` | Public defaults, endpoint selection, bearer/content headers, retry options, and the shared profile rate limiter. Auth or quota misconfiguration breaks every Hardcover request; excess traffic or missing credentials can fail sync broadly. Most failures are immediately visible and configuration can be corrected, but quota bypass can affect multiple profiles. | Small local request fixtures. **Reduce/retain:** replace private-field constructor assertions with GraphQL request-boundary checks; keep the provided limiter identity assertion because `ClientConfig.RateLimiter` explicitly documents sharing the limiter across profile clients. Keep the exact auth-header transport assertion as a wire contract. Remove direct logging-wrapper pass-through tests; configured GraphQL tests exercise decoding through that wrapper. |
| Hardcover GraphQL admission and transport: `client_graphql_test.go` | Retry/no-retry, HTTP 400/429/5xx, quota pacing and reset, mutation budget, timeout, permit release, concurrency, and truthful admission logging. This is core external I/O and mutation safety; replaying a mutation or releasing quota early can cause duplicate writes or provider throttling. Silent quota/accounting errors are hard to repair. | Largest cluster (about 900 lines) with deterministic local transports and synchronization fixtures; runtime is seconds. **Retain:** every scenario protects a distinct retry, concurrency, budget, or failure boundary. The log test now checks query payload visibility around actual zero/one HTTP requests rather than fixed message wording. |
| Hardcover dry-run and write capability: `dry_run_test.go`, `edition_capability_probe_test.go`, `regional_audiobook_import_test.go` | Dry-run’s concrete mutation boundary, probe response classification/timeouts, catalogue-write prevention, import polling budget, cancellation, and read-only completion verification. Accidental Hardcover writes and duplicate catalogue imports are externally visible and may be irreversible. | Multi-step local HTTP scripts are justified by the remote mutation lifecycle. **Retain** all success, no-op, timeout, and failure cases. |
| Hardcover reading state and owned-list lifecycle: `reading_progress_test.go`, `check_existing_read_test.go`, `update_user_book_status_test.go`, `check_book_ownership_test.go`, `mark_edition_as_owned_test.go`, `user_book_management_test.go` | Read creation/update/listing, finished/reread behavior, credentials, existing-read detection, Owned-list authorization, status/edition updates, current-user caching and concurrent fetch recovery. Wrong state can silently corrupt sync or duplicate reads; ownership errors cross an authorization boundary. Recovery may require manual data repair. | Rich response and error fixtures, including concurrency cases, are proportionate to core persistent data and permission behavior. **Retain.** |
| Hardcover identifier, edition, and reading-format lookup: `asin_lookup_test.go`, `legacy_asin_test.go`, `client_edition_isbn10_test.go`, `get_edition_by_asin_test.go`, `get_edition_test.go`, `get_user_book_test.go`, `search_identifier_test.go`, `search_isbn10_test.go`, `reading_format_test.go`, plus lookup methods in `client_methods_test.go` | ASIN regional mappings, legacy fallbacks, ISBN normalization, edition selection, cache bypass, format filtering, and response mapping. A false match can attach listening progress to the wrong book and persist incorrect state; a miss is visible and usually retryable. | Several wire-level cases, but independent identifiers/formats and competing editions are meaningful boundaries. **Retain.** API query assertions inspect actual GraphQL request payloads, not source text. |
| Hardcover title/person/publisher search: `search_extended_test.go`, `people_test.go` | Search result mapping, empty results, unsupported people types, and provider errors used during matching and edition creation. Incorrect matches can affect user-visible metadata; missed search is recoverable by manual review. | Moderately large API fixture set; response variants and public search endpoints differ. **Retain** success, miss, and error cases. |
| Hardcover error context: `errors_test.go` | Book ID context survives error wrapping and nil/empty inputs. This helps callers distinguish book-specific failures; user impact is supporting behavior and recovery is straightforward. | Small pure-unit tables with no mock infrastructure. **Retain** because wrapper, extractor, nil, and empty-ID paths are distinct API semantics. |
| Audiobookshelf REST client: `client_test.go` | Library/item/progress/session routes, bearer headers, expanded metadata, ebook detection, malformed/mismatched item IDs, non-200 response, and cancellation. Bad API decoding can silently select the wrong item or lose progress; HTTP failures are visible and retryable. | Reduced from elaborate one-row server-builder tables and Go-struct-generated responses to static JSON and assertions on requested query parameters and decoded fields. `GetLibraries` still covers success and HTTP 500. **Reduce; retain contracts.** |
| Audiobookshelf network trust: `network_policy_test.go` | URL validation, public/private address policy, DNS rebinding/mixed answers, metadata-address rejection, redirect origin/path credential stripping, connection reuse, and HTTP protocol constraints. A defect can permit SSRF or leak a bearer token; impact is security-critical and recovery may require credential rotation. | Security-focused IP and redirect matrices plus local resolver/server fakes are proportionate. **Retain**, including direct policy-helper tests because those helpers implement the exported trust modes and the tests also cover real request/redirect effects. |
| Audnex REST and discovery: `client_test.go` | Typed 404/rate-limit/transient responses, 400/403 non-retry behavior, 408/5xx retries, canonical ASIN safety, preferred-region ordering, exact ASIN response matching, stop-on-rate-limit, and body/overall deadlines. Wrong regional mapping can misidentify an audiobook; rate/deadline failures must not become false absence. | Real local HTTP cases are deterministic; retries have intentional backoff cost. **Reduce:** common transient-retry assertions are table-driven for distinct 408 and 502 branches; retain both cases and attempt-count checks. All other discovery branches remain distinct. |

Second proportionality audit after edits: no remaining safe reduction found. Remaining clusters protect public GraphQL/REST behavior, persistent reading state, external writes, shared quota, or network-security boundaries. Search found no leftover `setupServer` one-case harness in the owned API tests; the remaining `expectError` tables have multiple distinct API cases. Time-based tests use clocks only for deadline/concurrency contracts; no runtime-generated response fixture remains in the edited Audiobookshelf cases.

## Dedicated brittle-test candidate inventory

- Hardcover constructor tests formerly asserted private `Client` fields (`baseURL`, token, HTTP client, logger, retry fields). Removed the `NewClient` structural test and replaced configured/nil-config checks with real GraphQL calls through a controlled RoundTripper/server, asserting the outgoing URL, auth/content headers, response decoding, and retry count. The retained shared-rate-limiter identity assertion is backed by the exported `ClientConfig.RateLimiter` documentation: the supplied instance is deliberately shared across profile clients to preserve common quota.
- `transport_test.go` formerly instantiated private `loggingRoundTripper` directly and asserted returned pointer/error identity. Removed these implementation-detail tests. Functional GraphQL tests call through `executeGraphQLOperation`, which wraps the configured transport and decodes actual responses. Retained `headerAddingTransport` coverage asserts the externally required Authorization and JSON headers.
- Audiobookshelf’s constructor test formerly inspected private client fields. Removed it; API request tests verify endpoint and bearer behavior. The request fixture refactors use static JSON and observable decoded values.
- Audnex 408 and 502 tests had the same setup/assertions with one changed status literal. Combined into one table while preserving status classification and three-attempt checks.
- The GraphQL daily-pause test formerly asserted exact JSON `message` strings. Narrowed to user-provided query data absent before admission and present after admission, alongside zero/one observed HTTP request assertions. The source comment documents the diagnostic contract: pre-admission “about to request” logs are misleading while quota holds the request.
- Exact GraphQL query fragments/variables remain only where the actual wire query is the contract: the repository instructions explicitly require preserving Hardcover query-shape behavior; reading-format and legacy ASIN filters affect which remote records can be selected. `CheckBookOwnership` tests the required `Owned` list. Audiobookshelf address/path/redirect assertions are retained as network-security policy contracts, not private source-shape checks.

Dedicated second brittle audit: searches across all owned `*_test.go` files for source-file reads, `runtime.Caller`, AST/parser inspection, subprocess execution, and source-text assertions returned no matches. The remaining direct helper tests assert network policy outcomes; remaining query-string checks inspect serialized HTTP requests. No safe brittle-test removal remains.

## Changes and validation

Changed API tests: `internal/api/audiobookshelf/client_test.go`, `internal/api/audnex/client_test.go`, `internal/api/hardcover/client_config_test.go`, `internal/api/hardcover/client_graphql_test.go`, `internal/api/hardcover/client_methods_test.go`, and `internal/api/hardcover/transport_test.go`.

Focused validation used Go 1.26.8 and the requested cache:

```text
GOCACHE=/private/tmp/abs-suite-optimizer-go-cache GOTOOLCHAIN=go1.26.8 go test ./internal/api/hardcover ./internal/api/audiobookshelf ./internal/api/audnex
ok   internal/api/hardcover       7.968s
ok   internal/api/audiobookshelf  0.189s
ok   internal/api/audnex          (cached)
```

After the final log-assertion narrowing, `go test ./internal/api/hardcover -run '^TestGraphQLRequestLogsWaitForAdmissionDuringDailyPause$' -count=1` passed in 0.236s with the same Go/cache environment. `gofmt` and `git diff --check` passed. The final `make test-all test-web lint build` gate then passed on the final shared worktree.

---

# Test suite optimizer audit: owned sync and catalog packages

## Scope and measurement

Scope: `internal/sync` (including `state`), `internal/mismatch`, `internal/edition`, `internal/models`, `internal/isbn`, and `internal/audnexregion`. Production files were not changed. The initial worktree was clean. Counts below are named top-level Go test functions and physical test-file lines after the cleanup; subtests are included in the line count but not counted as separate test functions. They are comparison signals, not coverage targets.

Final discovery after the restored cover matrix and daily-pause diagnostic test enumerated 34 test files, 286 named tests, and 15,440 physical test lines. The earlier full focused test command completed in about 5.0 seconds wall time (mismatch's httptest cases account for about 4.3 seconds); the final changed edition package rerun completed in 0.483 seconds. File counts and line counts were gathered from the final owned test files.

## Complete proportionality inventory

| Test cluster | Size | Protected behavior, importance, and failure impact | Detection / recovery and cost | Disposition |
|---|---:|---|---|---|
| Sync association and identifier matching (`association_matching_test.go`, `association_confirmation_test.go`, `isbn_matching_test.go`) | 36 tests / 1,647 lines | Core identity and ownership behavior: ASIN/ISBN matching, saved association validation, ownership reconciliation, identifier changes, dry-run staging, and stale matches. A wrong association can silently attach progress or ownership to the wrong book and can persist across incremental runs. | Contract assertions sit at the service/client boundary and inspect persisted state or external mutation outcomes. Recovery may require correcting a bad association and resyncing. Interface fakes are substantial but do not emulate a remote service. | retain |
| Sync progress, finished state, rereads, and DNF (`handle_in_progress_book_test.go`, `handle_finished_book_test.go`, `dnf_test.go`, `dry_run_state_test.go`) | 47 / 2,669 | Core data integrity: read selection, progress updates, finished-state preservation, reread creation and cleanup, DNF, status failures, and dry-run state. Bugs can overwrite or duplicate reading history, or advance checkpoints before Hardcover accepted a mutation. | The tests assert resulting read mutations and retryable state at the service boundary. Incorrect history may need manual repair. Mock setup is detailed because each branch represents a distinct user-data outcome. | retain |
| User-book creation and edition correction (`find_or_create_user_book_id_test.go`, `sync_book_test.go`) | 19 / 876 | Core mutation and recovery behavior for finding or creating a user book, handling edition corrections, and retrying after failed create/update operations. Wrong IDs or skipped retries can create persistent association errors. | Tests observe resulting client writes, errors, and state invalidation. Repair may require a later resync or manual correction. Fakes are moderate. | retain |
| Hardcover search and found-book processing (`find_book_by_title_author_test.go`, `service_test.go`) | 9 / 1,341 | Core lookup and ownership flow, progress enrichment, unread skipping, and found/not-found paths. Failures can choose the wrong candidate or skip a required status/ownership mutation. | Mocked client boundary distinguishes ordinary misses from technical failures and asserts no unintended writes. A bad match can persist; a lookup failure can be retried. | retain |
| Sync outcomes and snapshots (`outcomes_test.go`, `snapshot_test.go`) | 34 / 1,362 | Core result classification and observable run snapshots: candidate totals, processed/unattempted counts, failure versus not-found, mismatch details, URLs, and format. Misclassification can hide required work or report misleading progress. | Assertions inspect snapshot values and exported mismatch outcomes. Operators can retry technical failures; incorrect persisted outcomes are harder to diagnose. The number of cases follows distinct outcomes and failure paths. | retain |
| Run lifecycle, library orchestration, and checkpointing (`sync_test.go`, `lifecycle_test.go`, `new_service_test.go`) | 24 / 1,041 | Core cancellation/finalization, checkpoint saves, library retries, symlink-retarget safety, and service initialization. Failures can leave incomplete work marked complete, lose progress, or report a misleading run state. | Tests use temporary state files and service/client boundaries, with cancellation and save-failure cases. State can often be retried, but a false checkpoint can silently skip work. | retain |
| Status determination and mismatch bridge (`determine_book_status_test.go`, `mismatch_collector_test.go`) | 2 / 110 | Supporting status mapping and correct collector ownership from the sync service. A wrong status is visible in later syncs; mixed collectors could combine records from profiles. | Small interface tests; state is easy to inspect and rebuild, while cross-profile mixing has a larger impact. | retain |
| State serialization and updates (`state/state_test.go`) | 27 / 654 | Core persisted format, version migration, checkpoint/association updates, dirty tracking, no-op timestamps, atomic-save failure preservation, and symlink resolution. Bad saves can corrupt or redirect the sync state file and cause skipped or repeated work. | Tests read actual temporary state files and verify sentinel targets remain untouched. Recovery from wrong-file writes may require backup restoration. File fixtures are moderate and each symlink boundary covers a different path-resolution hazard. | retain |
| State process locking (`state/file_lock_test.go`) | 7 / 233 | Core concurrency/data-integrity boundary: sidecar identity, symlink behavior, competing processes, and lock recovery after a killed process. A failure permits concurrent writers or leaves sync blocked. | A real subprocess holds the OS lock; the parent verifies exclusion and recovery. This costs setup but is the only direct evidence for the cross-process contract. | retain |
| Daily-quota request-intent diagnostics (`daily_pause_logging_test.go`) | 1 / 63 | Ensures the sync diagnostic preserves caller-provided message fields when not paused and suppresses the pre-admission intent while daily quota is paused, as documented by `Service.debugRequestIntent`. | A focused logger test exercises paused, unpaused, and clients without the optional pause capability. It does not pin production search wording. | retain |
| Mismatch enrichment and export (`mismatch/mismatch_test.go`, `mismatch/reading_format_test.go`) | 20 / 1,408 | Supporting user-visible import data: IDs, ISBN forms/checksums, publisher/date/region fallback, ebook/audiobook labels, abridged state, and saved JSON. Missing or wrong export metadata can cause an incorrect manual import. | Tests inspect returned records and actual JSON files; the focused package uses small local HTTP servers for external request behavior. A bad export can be regenerated before import. Case count reflects distinct source metadata and fallback outcomes. | retain |
| Mismatch collector isolation (`mismatch/collector_test.go`) | 1 / 40 | Profile/data-isolation boundary under concurrent writes. Mixing collectors can expose or combine another profile's records. | Concurrent goroutines exercise independent collectors; tests run with the race detector in the project gate. Failure is detectable but can disclose records. | retain |
| Edition creation, DTO, validation, and duplicate recovery (`edition/creator_test.go`, `creator_dto_test.go`, `creator_reuse_test.go`) | 28 / 2,760 | Core Hardcover mutation contract: required/optional DTO fields, format routing, duplicate lookup across IDs, same-book adoption, conflict rejection, error recovery, and mutation reserve. A wrong insertion or adoption may be difficult to undo. | Tests inspect serialized mutation inputs, call the creator through client boundaries, and assert mutation/no-mutation outcomes. The lookup fake is moderate; the broad cases guard irreversible external writes. | retain, with duplicate/brittle reductions below |
| Edition cover content and upload behavior (`creator_cover_test.go`) | 7 / 397 | Covers opt-in upload success and failure paths, image-format and size limits, Authorization scoping, and the default disabled behavior. Production callers leave upload disabled. | Local transports exercise download, credential, storage, image-record, and attach outcomes; verify rejected images never reach credential or storage requests; and verify disabled mode makes no request. Retain the full matrix because credential disclosure and partial remote writes have meaningful impact. | retain |
| Cover token scope, redirects, and network trust (`creator_token_test.go`, `creator_redirect_test.go`) | 11 / 558 | Security boundary for Audiobookshelf credentials and cover fetching: off-origin redirect, HTTPS downgrade, configured-base scope, and private-address policy. A regression could leak credentials or allow an unsafe fetch. | Local HTTP servers and deterministic transport fakes check headers and rejected destinations. Credential leakage is high impact; these integration tests are proportionate even though production callers leave cover upload disabled. | retain |
| Audiobookshelf model decoding and reading format (`models/audiobookshelf_test.go`, `models/reading_format_test.go`) | 6 / 114 | Supporting behavior that distinguishes ebooks from audiobooks, decodes abridged metadata, and normalizes reading format/context. A format error can route matching or creation to the wrong Hardcover edition. | Compact JSON and in-memory fixtures; failure is usually visible at the next lookup but can produce a wrong match. Each test exercises a different input boundary. | retain |
| ISBN normalization and parsing (`isbn/isbn_test.go`) | 4 / 96 | Core identity utility: valid/invalid checksums, ISBN-10/13 conversion, normalization, X check digit, and unsupported 979 conversion. A bad parse can silently select a wrong edition or lose a valid identifier. | Deterministic examples with direct expected forms; cheap to repair, but high downstream matching impact justifies edge cases. | retain |
| Audnex region list and validation (`audnexregion/regions_test.go`) | 3 / 71 | Supporting configuration/fallback behavior. Wrong membership can reject a configured region or use a different catalogue during lookup. | Small fixed-list and predicate checks; failures are quickly visible and corrected by configuration/code update. The list order also determines fallback sweep order. | retain |

The largest clusters are sync and edition, where production behavior coordinates external writes with durable state or credentials. The sync package currently has 9,996 test lines, including 887 lines for state; its non-state tests total 9,109 lines against 6,075 non-state production lines. The edition package currently has 3,715 test lines against 1,474 lines in `creator.go`. These ratios prompted review, not automatic retention; the remaining breadth protects durable state, external mutations, credential handling, and recovery paths. The state cluster is intentionally detailed because path resolution can redirect durable writes. The small utility suites remain compact.

## Changes and first brittle-candidate audit

- Removed `TestCreateEdition_DTOTitleIsSent`: its only assertion duplicated the title assertion already present in `TestCreateEdition_DTOMinimalInputSendsOnlyRequiredKeys`.
- Replaced `TestCreateEdition_LooksUpEachIdentifierOnceInOrder` with a normalized-identifier count assertion and a successful result assertion. The required behavior is checking each distinct identifier once; the literal call sequence has no documented contract.
- Kept a single 300 ms lookup delay in the mutation-reserve test. It starts with a 450 ms deadline and a 250 ms reserve, then verifies the lookup completed and no insert was sent after the lookup consumed the reserve. This timing cost catches a real boundary regression: checking only at entry could permit an insert after the lookup has used the protected budget. The matching-edition case has no delay and verifies lookup/reuse still occurs with a 450 ms deadline and 1 minute reserve.
- Removed `TestNewCreator`, which used `unsafe` and reflection to inspect private constructor fields. Constructor behavior is exercised by request/token, redirect, and creator operation tests; no observable requirement was unique to reading those private values.
- In `creator_token_test.go`, removed the two duplicated empty-base rows already covered by token scoping and retained whitespace-only cases. Replaced private `TLSClientConfig` inspection with a local TLS request proving verified TLS rejects the self-signed server by default and `EnableInsecureTLS` permits the request to reach the handler. The handler counter is atomic; invalid image bytes stop the flow before any subsequent upload endpoint access.
- Narrowed `internal/sync/daily_pause_logging_test.go` from assertions tied to particular search phrases to the `Service.debugRequestIntent` behavior documented in `service.go`: preserve caller-provided message fields when not paused and suppress the pre-admission intent while daily quota is paused. The test message is a sentinel input, not a pinned production search phrase.

Changed test files: `internal/edition/creator_cover_test.go` (full seven-test safety matrix restored), `creator_dto_test.go`, `creator_reuse_test.go`, `creator_test.go`, `creator_token_test.go`, and `internal/sync/daily_pause_logging_test.go` (narrowed diagnostic contract retained). No production files changed.

## Dedicated second proportionality audit

After the final edits, the 286 named tests across 34 files map to the 18 clusters above. I challenged the largest clusters and the test-to-production line ratios. No remaining cluster protects ancillary code with core-runtime-level enumeration, and no cluster emulates a remote service broadly enough to justify a cheaper replacement. The concurrency and filesystem fixtures exercise real OS boundaries; the HTTP fixtures stay local; the sync fakes assert API results and mutation effects. No additional safe reduction, duplicate removal, combination, or parameterization was identified. Gate: **pass, no-op after the described changes**.

## Dedicated first brittle-test inventory

| Candidate and requirement | Failure it could catch | Disposition and evidence |
|---|---|---|
| `sync/daily_pause_logging_test.go`: daily-quota request-intent diagnostic | Suppressing this pre-admission intent while quota pauses requests avoids saying an API request is about to be sent when it is waiting for reset | narrow; keep the optional pause-capability, caller-field, and paused/unpaused behavior checks without pinning production search phrases |
| `edition/creator_test.go::TestNewCreator`: private fields via reflection and unsafe | Constructor failed to retain client/mode/token/client pointer | remove; those values are exercised through public creator/request outcomes; private layout is not an interface |
| `edition/creator_token_test.go`: duplicate empty-base rows and private `TLSClientConfig` field assertions | Token could be sent without a configured base; default TLS verification or explicit opt-in could stop working | remove duplicate rows because token-scoping already covers empty base; replace private transport inspection with a local TLS request that verifies rejection by default and handler reachability after opt-in |
| `edition/creator_reuse_test.go::TestCreateEdition_LooksUpEachIdentifierOnceInOrder`: exact lookup sequence | Missing or repeated normalized identifier check, plus incidental sequence change | narrow; count every normalized identifier exactly once and require successful creation |
| `edition/creator_dto_test.go::TestCreateEdition_DTOTitleIsSent`: title value in DTO | Missing title | remove; the minimal required DTO test already asserts `"title": "Only Title"` at the actual mutation input |
| `edition/creator_reuse_test.go` reserve timing | Insert begins without reserve remaining after a real lookup | keep as explicit safety contract; only one real delay remains and verifies lookup-before-boundary behavior |
| `sync/lifecycle_test.go` and `sync/state/state_test.go` short sleeps around timestamp comparisons | Snapshot activity/processed timestamps or state updates fail to advance | keep as observable status/state behavior; sleeps ensure distinct clock ticks, and no clock injection exists in the assigned test-only scope |
| `sync/state/file_lock_test.go` child-process wait/poll sleeps | Lock process fails to initialize, exclusion or crash recovery fails | keep; these sleeps synchronize an actual subprocess and do not assert a particular duration |
| `mismatch/reading_format_test.go` regexp over the actual Hardcover request query | ASIN-only edition filter no longer constrains format/mapping behavior | keep as external GraphQL query behavior; test sends/receives a real local HTTP request and asserts audiobook versus ebook outcomes. Repository guidance explicitly preserves Hardcover query shape |
| `edition/creator_test.go` reflection helper/matchers for mutation result values | Mock may fail to supply the mutation result expected by the creator | keep as test-fixture glue for the public GraphQL response shape, not source inspection. The checked-in schema defines `EditionIdType` with `id` and `errors`, `ImageIdType` with `id`, and exposes `insert_edition`, `insert_image`, and `update_edition` (`internal/api/hardcover/hardcover-schema.graphql`, lines 5,565, 7,884, 9,927, 9,958, 10,104) |
| State/export tests using `os.ReadFile` and `ReadDir` | State version/content, export JSON, or unrelated sentinel target changes | keep; files are actual temporary outputs and persisted state, both supported behavior contracts rather than source text |
| Mock `AssertNotCalled` and method expectations in sync/edition tests | Unintended Hardcover writes or omitted external effects | keep; assertions concern externally visible client operations, not internal call ordering |

## Dedicated second brittle-test audit

After edits, I rescanned owned test files for source reads/AST parsing, unsafe field access, exact log text, strict lookup sequence comparisons, reflection field checks, regex query checks, private TLS transport inspection, and timing-based checks. The unsafe constructor inspection, exact log phrase assertion, exact lookup-order comparison, duplicate empty-base cases, and private TLS field inspection are gone. The TLS assertion now observes the request boundary. Remaining reflection is limited to the edition GraphQL mock fixture/matchers described above; its fields correspond to the checked-in external schema. Remaining file reads inspect temporary state/export artifacts. Remaining sleeps either enforce the important post-lookup mutation-reserve boundary or synchronize/compare observable timestamp/process behavior. The regex applies to an actual GraphQL request and protects the format-selection contract. There is no remaining safe brittle-test cleanup candidate. The parent also reports its final AST duplicate and unsafe/source-inspection scans returned no candidates. Gate: **pass, second audit no-op**.

## Final discovery and validation

A final discovery after both gates found no new test cluster or brittle assertion to disposition. Focused validation passed with:

```
GOTOOLCHAIN=go1.26.8 GOCACHE=/private/tmp/abs-suite-optimizer-go-cache go test -count=1 ./internal/sync ./internal/sync/state ./internal/mismatch ./internal/edition ./internal/models ./internal/isbn ./internal/audnexregion

ok internal/sync
ok internal/sync/state
ok internal/mismatch
ok internal/edition
ok internal/models
ok internal/isbn
ok internal/audnexregion
```

`gofmt` and `git diff --check` passed. The parent reports the final full gates passed: `make test-all`, `make test-web`, `make lint`, and `make build`.

### Final cover and TLS audit

The final owned-package inventory retains all seven `creator_cover_test.go` test functions (397 lines), covering opt-in uploads and failures, format and size checks, request authorization, and both disabled-mode no-request contracts. Production callers leave `EnableCoverUpload` off. Token scoping, redirects, address policy, and TLS verification remain exercised through local request boundaries. The second proportionality and brittle-test audits found no further safe reduction; final discovery enumerated 34 owned test files, 286 named tests, and 15,440 test lines.

---

# Test-suite optimization audit: service, commands, web, and Helm

Audit date: 2026-10-01. Scope is root `internal/api` handler tests, `internal/server`, `internal/multiuser`, `internal/database`, `internal/config`, `internal/util`, `internal/logger`, `cmd`, `web/app.test.js`, and `helm/test-chart.sh`. No production files were changed.

## Proportionality inventory

| Cluster (current test lines) | Behavior / importance and impact | Detection and recovery represented | Runtime cost / disposition |
|---|---|---|---|
| Root API handlers (5,148; 2,439 production lines) | Status, edition draft/create, idempotency, capability/permission, request validation, dry-run, stale data, Hardcover and Audiobookshelf failure outcomes. High impact: incorrect handling can duplicate a remote import, persist an unverified association, or expose a privileged mutation. | Handler-level HTTP requests with local fake services test auth and role boundaries, changed source data, remote writes/readback, retry classifications, malformed envelopes, cancellation, and durable result state. Unknown outcomes stay recoverable without resubmitting. | Uncached suite measured 161.406 s. Retained despite cost: this is the most consequential route/mutation matrix. Handler fixtures set limiter pacing to 1 ns where applicable; remaining cost is suite breadth and HTTP/state fixtures. No broad timing/concurrency deletion was justified. |
| Server (974; 321 production lines) | Route wiring, profile authorization, viewer/read-only decisions, token redaction, and capability refresh. High security and data-isolation impact. | Request/response boundary checks distinguish unauthorized, forbidden, and concealed foreign-profile results and verify redacted output; a wrong route/role has a directly visible response. | Retained: cases cover distinct role and ownership rules rather than duplicate implementation paths. Whole non-API package gate peaked at 22.9 s per parent’s baseline. |
| Multiuser (3,548) | Profile persistence, shared per-profile/per-token rate admission, create and capability cache behavior, refresh coalescing, cancellation, locking, path/symlink safety, and dry-run. High data-integrity, cross-profile, and concurrency impact. | SQLite/temp-directory fixtures inspect persisted data and exercise operations concurrently through local HTTP servers. Failure recovery checks ensure state is unchanged or explicitly recoverable. | Retained distinct isolation and cancellation cases. Replaced three limiter pointer-identity checks with observable HTTP admission; focused cases pass in 0.364 s. The 16-second TTL test uses `testing/synctest` virtual time, not a wall-clock sleep. |
| Database (511) | Repository transactions, schema compatibility, run/checkpoint retention, profile scoping, and migrations. Persistent schema errors can corrupt or strand user state. | Reopen/query persisted state, inspect migration effects and supported dialect mapping; failures surface as repository errors or missing/incorrect durable values. | Retained: persistence and migration contracts are independent. Large-text dialect assertions intentionally protect supported-engine schema compatibility. |
| Config (354; was 436) | YAML/environment precedence, defaults, validation, URL normalization, and user-facing run/report/tool options. Bad config prevents startup or can silently change external-service access. | Load real temp config plus environment values and inspect effective config; invalid values return errors at load. | Removed one exact 82-line duplicate `TestLoadConfig`; `TestLoadConfigFromFile` already covered the same YAML/environment/load behavior. Remaining cases cover distinct defaults, precedence, and validation. |
| Rate limiter (1,189; was 1,336) | Header parsing, Retry-After and daily quota admission, cancellation, permit release, concurrency cap, and schedule changes. Incorrect pacing risks provider throttling or skipped requests. | Local response headers, channels, and `synctest` exercise backoff, reset, blocked/canceled acquisition, and ordinary-versus-daily pacing. Some deadline/cap checks inspect limiter state because reproducing long provider delays via public waits would make tests slow and nondeterministic. | Removed the handmade semaphore suite (81 lines), duplicate concurrent-access test, and arbitrary rate-limiter log text/level assertions; kept the real production permit and cancellation tests. One 1.1-second real reset boundary remains because it tests a real elapsed-time contract. |
| Logger (819; was 983; 447 production lines) | Configured emitted levels/fields/formats, context propagation, request IDs, and middleware response behavior. Logs and request correlation are operator-facing diagnostics. | Assert emitted records and middleware-visible headers/body/error propagation, including a handler whose `ResponseWriter.Write` returns an error. | Removed duplicate context/background/field cases, invalid identity-only nil/background assertion, private wrapper status-field testing, and duplicate logging tests. Kept observable logger output contracts. Net 164 test lines removed. |
| Commands (1,834; edition cluster was 1,759; 857 production lines) | CLI parsing/config/output and especially edition prepopulation/import, format and identifier selection, remote mutations, readback/association, and retry safety. A mistake can create a wrong or duplicate remote edition. | Invoke command code with fake Hardcover/Audiobookshelf boundaries; read generated temp JSON templates as user artifacts, not source text. Invalid inputs must not write templates or submit mutations. | Retained: destructive CLI paths need explicit coverage. The 53-line main CLI and 22-line image-tool tests cover their exposed command behavior. `cmd/edition-tool` (74-line TODO-oriented stub) and `cmd/hardcover-lookup` (472 lines) have no direct test files; `make build` covers compilation, while shared Hardcover/config packages have their own tests. This is a direct CLI coverage gap, not a reason to add tests in this optimization pass. |
| Web (`web/app.test.js`, 2,026; was 2,006) | Session races, permission refresh, create/forget restrictions, durable pending-import recovery, timeout/unknown outcomes, XSS escaping, keyboard focus, and external destinations. High risk around duplicate external writes and unsafe untrusted text. | Stubbed network/storage boundaries exercise reloads, delayed responses, malformed results and auth expiry; semantic labels, action ownership, href destinations, and escaping are asserted. | Retained core workflow matrix. Replaced strong/span/CSS-class assumptions with visible text, semantic `<details>`/candidate region, and action/destination association. Kept `class="edition-form"` because production `web/static/app.js:2407` selects that class to read form fields. |
| Helm smoke (`helm/test-chart.sh`, 111) | Chart structure, default/production/development/custom rendering, optional client-side Kubernetes validation, and empty-secret hint. Chart errors affect deployment operability. | The script renders local templates and optionally runs `kubectl --dry-run=client`; expected failure is a lint/template/manifest error before deployment. | No content change. Script was inspected, not executed per task constraint; `bash -n helm/test-chart.sh` passed. |

No direct test files exist for `cmd/edition-tool` or `cmd/hardcover-lookup` as noted above. Other clusters in scope have direct tests. Current scoped inventory is 16,403 Go and web test lines; line ratios were used only to trigger review, not as deletion criteria.

## Brittle-test inventory and changes

- Logger context tests had pointer-identity checks (including a `context.Background()` identity assumption that is not valid for its implementation) and duplicate nil/background/field cases. The remaining nil-logger boundary asserts preserved caller value/cancellation and nil `FromContext`, not object identity; a smoke test also verifies `With` on a nil receiver returns a usable logger.
- Logger HTTP tests directly asserted the private response-writer status field. Replaced that with middleware-boundary body/status coverage already present plus `TestHTTPMiddlewarePropagatesResponseWriteError`, which checks that a handler receives the actual write error.
- Rate-limiter tests asserted arbitrary internal log wording/levels; removed those assertions while retaining rate/pause state behavior. API-level Logger emitted-output assertions remain because they describe observable logger output.
- A handmade channel semaphore test and a duplicate rate-limiter concurrency test did not exercise the production limiter; removed them. The actual `RateLimiter.Acquire` blocked/release test remains.
- Config had identical YAML/environment load assertions in two tests; removed the duplicate only.
- Multiuser limiter tests read the private limiter map and used `Same`/`NotSame`. Same-token serialization remains asserted by requests withheld at an HTTP handler, while a rotated-token request can enter concurrently. A client created for the original token after rotation must still wait on that token’s active permit. Config-only update keeps its request-level serialization assertion without pointer access.
- Web tests previously matched `<strong>`, a `edition-note` span, CSS class ordering, `btn-primary`, and unrelated link text/destination independently. They now check visible text and semantic regions, and the report-problem label is extracted from the anchor whose href names the expected edition. The only exact style class retained is `.edition-form`, proven live by the production selector at `web/static/app.js:2407`. `data-edition-*` selectors remain because production event/action handling uses them.
- No scoped test reads Go/JS source files or asserts source-line absence. `os.ReadFile` occurrences are persisted-state verification in `multiuser` and temp JSON generated by the CLI under test. The dedicated scan found no remaining `Same`/`NotSame`, `<strong>`, `edition-note`, or `btn-primary` assertions in scope.

## Final second audits and validation

After the last edits, reran proportionality review of each retained large cluster and the brittle scans over every owned directory. No duplicate config/logger/rate-limiter clusters or safe remaining markup/source/private-identity assertions were found. The one private pointer candidate discovered during this second scan was replaced behaviorally before declaring the scan clear. Final web review confirmed the exact `.edition-form` assertion remains tied to a production selector. Final Helm review was inspection-only; shell syntax validation passed.

Validation evidence: `node --test web/app.test.js` passed 75/75 after final semantic/link changes (57.3 ms); focused Go test `GOCACHE=/private/tmp/abs-suite-optimizer-go-cache GOTOOLCHAIN=go1.26.8 go test ./internal/multiuser -run 'TestCapabilityAndProfileHardcoverClientsShareLimiter|TestConfigOnlyProfileUpdatePreservesLimiterForExistingClient' -count=1` passed (0.364 s); `bash -n helm/test-chart.sh` passed. The final `make test-all test-web lint build` gate passed on the final shared worktree with Go 1.26.8.
