# Source Draft Field Crosswalk

This crosswalk covers the read-only source draft returned by
`GET /api/profiles/{id}/edition-drafts/source/{itemID}`. It describes draft
data from Audiobookshelf (ABS) and optional Audnex region discovery. The route
makes no Hardcover requests and does not create an edition or mapping.

| Concern | Draft field | Source and treatment |
|---|---|---|
| Item and mode | `abs_item_id`, `reading_format`, `dry_run` | Item ID and reading format come from the expanded ABS item. `dry_run` reflects the profile's sync setting; this draft route is read-only in either setting. |
| R4: ASIN identity | `source_identifiers.asin`, `confirmed_region`, `region_status`, `audible_identifier_candidate` | `source_identifiers.asin` is the trimmed, bare ABS ASIN, without a region suffix. Audnex confirmation is reported separately: `confirmed_region` is present only when a response returns the same ASIN; `region_status` distinguishes `confirmed`, `unknown` after a complete miss, `temporarily_unavailable` after a rate limit, transient error, or 400/403 response, and `not_applicable` when an audiobook has no ASIN. Discovery checks the preferred Audnex region first (US when unset), then the remaining supported regions. The candidate's ASIN and region are separate; its region is empty when unconfirmed, and `correction_allowed` signals it may be corrected in a later add-edition flow. A later regional Audible identifier is not submitted by this GET route. It never writes `edition.asin` or `book_mappings`. |
| ISBN | `source_identifiers.isbn`, `isbn_10`, `isbn_13`, `isbn_10_valid`, `isbn_13_valid`, `corrected_isbn` | The source ISBN is trimmed but otherwise preserved. Candidate ISBNs remove separators and uppercase a trailing `x`. The ISBN parser accepts by shape and reports checksum validity separately; a counterpart is derived only when the source checksum permits it. Ebook drafts include an empty `corrected_isbn` slot. |
| Audiobook metadata | `metadata_preview` | Audiobook-only, read-only preview of title, subtitle, author, narrator, publisher, description, language, date, edition format and information, duration, and ISBN fields. It is not an editable request body. |
| Publication date | `metadata_preview.release_date`, `ebook_candidate.release_date` | For a confirmed audiobook ASIN, a usable Audnex `releaseDate` takes precedence. Otherwise both draft formats use ABS `publishedDate`, then `publishedYear`; a year-only value becomes January 1. Unusable dates produce a warning. |
| Ebook candidate | `ebook_candidate` | Ebook-only candidate with title, subtitle, author, source ASIN, normalized ISBN fields, validity flags, normalized ABS publication date/year, fixed `edition_format: "Ebook"`, and the empty `corrected_isbn` slot. The GET endpoint does not accept candidate edits or create an edition. |
| Eligibility and warnings | `eligible`, `ineligible_reason`, `warnings` | Eligible means a valid ASIN (exactly 10 ASCII letters or digits) or an ISBN accepted by shape is present; a malformed ASIN is kept visible with an `invalid_source_asin` warning but does not count. A bad-checksum ISBN remains eligible when its shape is supported. Author and date gaps are warnings, not eligibility gates. Warning objects contain a code and message; `retryable` is true for temporary Audnex unavailability. |

The response field definitions and warning codes are documented in
[`openapi.yaml`](../openapi.yaml) and are implemented in
`internal/api/handlers_edition_draft.go`. Audnex discovery is implemented in
`internal/api/audnex/client.go`.
