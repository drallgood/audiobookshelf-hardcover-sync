# Edition Creation Tool

The standalone `edition` command provides `prepopulate` templates and explicit
edition creation. Audiobooks with a valid ASIN use a region-qualified Audible
import. ISBN-backed audiobooks without a valid Audiobookshelf ASIN and ebooks
use Hardcover's format-aware edition insertion. Normal sync does not create
catalogue editions.

## Requirements

Build and run the command locally; the published Docker image contains only
the main `audiobookshelf-hardcover-sync` service.

```bash
go build -o edition ./cmd/edition
```

The command reads `config.yaml` by default or the file passed with `--config`.
The default file is optional; a file named with `--config` must exist. Set
`hardcover.token`. When associating an Audiobookshelf item, also set
`audiobookshelf.url` and `audiobookshelf.token`. Environment variables such as
`HARDCOVER_TOKEN`, `AUDIOBOOKSHELF_URL`, `AUDIOBOOKSHELF_TOKEN`,
`AUDIOBOOKSHELF_NETWORK_TRUST`, and `AUDIOBOOKSHELF_AUDNEXUS_REGION` override
the file, as they do for the sync service. Hardcover requests follow the
configured `rate_limit` settings, like sync. The command does not require the
sync service's Audiobookshelf settings and prints no configuration summary.

The Hardcover token needs `read:catalog` for book/edition reads and duplicate
checks, plus `write:catalog:append` or a broader catalogue-write scope for
creation. ASIN audiobook imports use `upsert_book`; ISBN-backed audiobooks and
ebooks use `insert_edition`. A missing catalogue-write scope leaves sync
available but makes the explicit create operation fail.

## Commands

```bash
./edition --config ./config.yaml prepopulate --book-id 12345 --output edition.json
# Choose the format when a book has both identifiers or should use another format.
./edition --config ./config.yaml prepopulate --book-id 12345 --reading-format ebook --output ebook.json
# Input has no abs_item_id, so this create saves no ABS association.
./edition --config ./config.yaml create --input edition.json
# Single-user sync: saves the match in the configured sync.state_file.
./edition --config ./config.yaml create --input edition.json --abs-item-id li_123
# Import by Audible ASIN: review the Audnexus preview, then confirm explicitly.
./edition --config ./config.yaml create --input audible.json --confirm-audnexus
# Web-service profile-123 with blank sync.state_file and paths.data_dir ./data.
./edition --config ./config.yaml create --input edition.json --abs-item-id li_123 --state-file ./data/sync_state.profile-123
# Dry run without an ABS association; input has no abs_item_id.
./edition --config ./config.yaml --dry-run create --input edition.json
```

`prepopulate` writes `reading_format` and a matching `edition_format` into its
JSON template. By default, it selects audiobook when Hardcover has an ASIN,
including when the book also has ISBNs; this preserves the prior audiobook
path for books with both identifiers. It selects ebook for an ISBN-only book.
Use `--reading-format audiobook` or `--reading-format ebook` to override that
inference. The selected format is written to the template, so `create` uses the
same format without an additional flag. Audiobook templates still need a valid
ASIN and a known or supplied Audible region before they can be imported.

The `--abs-item-id` and `--state-file` flags apply to `create`. An
`--abs-item-id` flag overrides `abs_item_id` in the input JSON. When an ABS
item ID is present in either place, the command saves the match in
`--state-file`, or in the configured `sync.state_file` when the flag is
omitted. That default suits the single-user sync.
If a submitted ASIN or ISBN differs from the fetched item's identifier,
the command stops before writing to Hardcover. Check the item and target book;
pass `--confirm-identifier-correction` to make a deliberate correction. The
JSON result includes a warning when a correction is confirmed.

For a web-service profile, pass `--state-file` with that profile's state
file. The multi-user service starts from the profile's
`SyncConfig.StateFile`, resolving relative paths against `paths.data_dir`;
when it is blank, the base path is `<data_dir>/sync_state.json`. It then
removes a trailing `.json` and appends `.<encoded-profile-id>`. Thus
`./data/sync_state.profile-123` applies only when `sync.state_file` is blank,
`paths.data_dir` is the default `./data`, and the profile ID is
`profile-123`. The CLI uses the path literally; it does not derive a
profile-specific filename.

## Audiobook input

Audiobook input accepts a bare ASIN of ten ASCII letters or digits.
`reading_format` defaults to `audiobook`. Input JSON is limited to 1 MiB.
Provide a positive Hardcover `book_id` to add an edition to that selected
Hardcover book. You may omit it only when the input JSON supplies a usable
audiobook ASIN, even if `abs_item_id` is provided. This starts
**Import by Audible ASIN**, which does not require selecting a
Hardcover book first. Review the Audnexus record and confirm it by typing `y`
or `yes`, or by passing `--confirm-audnexus`.

```json
{
  "asin": "B00XXXYYZZ",
  "reading_format": "audiobook",
  "asin_region": "uk",
  "abs_item_id": "li_123"
}
```

Add `"book_id": 12345` to add the edition to that selected Hardcover book.
Existing export files with `book_id` continue to add the regional edition to
the selected book.

`asin_region` is optional; `region` is an alias. If both are supplied they
must agree. A supplied region identifies the regional ASIN sent to Hardcover.
For **Import by Audible ASIN** (no `book_id`), `create` looks up that exact
ASIN and region in Audnexus, prints the record, and requires confirmation. For
a selected-book import, an explicit region is sent to Hardcover without an
Audnexus lookup. Without a region, the command discovers one by finding the
requested ASIN in Audnexus; it prefers `audiobookshelf.audnexus_region`
(default `us`) and does not infer a region from the ASIN. If discovery finds
no region or Audnexus is temporarily unavailable, the command stops before the
Hardcover import; retry later or supply `asin_region`.

For **Import by Audible ASIN** with an ABS item ID, the command also prints
its metadata beside the Audnexus values and a per-field comparison. Differences are informational;
review that the Audnexus record identifies the ABS item before confirming.
Release dates retain the precision supplied by Audnexus.
Unknown or unavailable Audnexus data cannot be confirmed. A corrected ASIN and
region can be supplied in the input for preview before creation. If an ABS
item is supplied and its identifiers conflict with the input, pass
`--confirm-identifier-correction` after reviewing the warning.

An ISBN-only audiobook uses the insertion fields to add an edition to the
selected Hardcover book while keeping `reading_format` set to `audiobook`:

```json
{
  "book_id": 12345,
  "title": "Audio Book Title",
  "isbn_13": "9781234567890",
  "author_ids": [1],
  "edition_format": "Audiobook",
  "reading_format": "audiobook",
  "abs_item_id": "li_123"
}
```

For selected-book input (`book_id` supplied), with no `abs_item_id`, a valid
submitted ASIN selects the regional Audible import, even when ISBNs are also
supplied. If the ASIN is missing or malformed, an ISBN selects
`insert_edition`; the malformed ASIN is treated as missing. For selected-book
input with `abs_item_id`, the fetched Audiobookshelf item decides which path
applies: a valid canonical item ASIN uses the regional import, including when
the input omits ASIN. If the item has no valid canonical ASIN, a valid
submitted ASIN can correct it and uses the regional import after
`--confirm-identifier-correction`; otherwise the input uses ISBN-backed
insertion. Thus omitting the input ASIN cannot bypass an ASIN that
Audiobookshelf reports.

For ISBN-backed audiobook insertion, include the edition metadata required by
Hardcover (`title` and at least one `author_ids` entry), plus `isbn_10` or
`isbn_13`. If an ABS item is supplied and the input ISBN differs from that
item's ISBN, creation stops until `--confirm-identifier-correction` is passed.

For regional audiobook imports, the command uses only `book_id`, `asin`,
region, `reading_format`, and the optional ABS item ID. Other JSON fields are
ignored, so existing mismatch export fields remain acceptable.

## Ebook input

Set `reading_format` to `ebook` and provide the ebook metadata used by
Hardcover insertion. The input requires a positive `book_id`, a `title`, at
least one `author_ids` entry, and at least one ASIN or ISBN. If supplied,
`release_date` must use `YYYY-MM-DD` format. For example:

```json
{
  "book_id": 12345,
  "title": "Book Title",
  "subtitle": "Digital Edition",
  "asin": "B00XXXYYZZ",
  "isbn_13": "9781234567890",
  "author_ids": [1],
  "publisher_id": 10,
  "release_date": "2023-01-01",
  "edition_format": "Ebook",
  "reading_format": "ebook"
}
```

The insertion path retains duplicate checks within the selected reading
format. An edition already present for the requested book is returned as
`existing`; the command does not resend its metadata. An existing edition
associated with another book is refused.

## Results and saved matches

Each Hardcover write is sent at most once and only when enough time remains to
confirm it. If a write fails, the error says whether Hardcover may have
processed it. When it says so, check the book in Hardcover before retrying. A
missing catalogue-write permission is reported as such, with no edition
created. Each invocation uses a fresh Hardcover client and does not read the
server's cached permission evidence, so no permission-refresh command is
needed. After correcting a confirmed permission denial, rerun with the current
token.

The command prints a JSON result with `success`, `status`, `book_id`,
`edition_id`, `image_id`, and `reading_format`; it may also include
`image_error`, `existing`, `abs_item_id`, and `association_saved`. An
**Import by Audible ASIN** result also includes `audnexus_region` and
`audnexus_record`, plus `audnexus_comparison` when an ABS item was fetched.
Regional audiobook imports use `loaded`, `created`, or `dry_run`; inserted
editions, including ISBN-backed audiobooks, and ebooks use `existing`,
`created`, or `dry_run`.
When an ABS item ID is supplied, the command fetches the item before making a
Hardcover change and requires its format to match the input. The saved match
keeps the item's own ASIN and ISBN; a different submitted ASIN is recorded as
your correction. ISBN corrections on ISBN-backed audiobooks also require
explicit confirmation. After verifying
the Hardcover result, it saves a local match in the configured state file
while holding the state-file lock. If another sync holds the lock, the command
returns an error before contacting Hardcover. A local save failure after a
successful Hardcover operation is reported separately; verify the Hardcover
result before retrying, because a retry may create another edition.
The lock covers the ABS fetch and Hardcover import or insertion as well as the
state save. A concurrent sync using the same state file can fail while create
holds it. The Hardcover operation alone can take roughly 65 seconds, in
addition to ABS fetch and region discovery.

Without an ABS item ID, no match is saved. A `loaded` audiobook import without
a saved mapping remains unresolved by later syncs. Dry run performs no
Hardcover mutation and saves no match. It can still fetch the ABS item and
check Audnex when those steps are requested by the input.

## Audiobookshelf URL trust

`audiobookshelf.network_trust` (or `AUDIOBOOKSHELF_NETWORK_TRUST`) controls
which addresses this command may contact for Audiobookshelf requests.
`allow_private` is the default for self-hosted addresses. `public_only`
requires HTTPS and public addresses. Both modes require an absolute
`http://` or `https://` base URL; invalid URLs or trust values stop the
command. See the [main configuration reference](../../README.md#configuration-reference)
for the deployment-wide setting.

For edition insertion, an `image_url` is not fetched or uploaded; the result
may include `image_error` for the unsupported cover upload.
