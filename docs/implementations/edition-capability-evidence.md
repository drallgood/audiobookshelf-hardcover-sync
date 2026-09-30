# Edition capability evidence and probe dependency

This note supports the immediate-resync and edition-capability changes in
[upstream PR #212](https://github.com/drallgood/audiobookshelf-hardcover-sync/pull/212). It records the live evidence used by the classifier and its
limits; the capability result does not guarantee a successful edition create.

## Recorded live scope evidence (2026-09-27)

A limited token and an ordinary full token were each sent these argument-free
mutations, with no variables:

```graphql
mutation ProbeInsertEditionCapability { insert_edition { id } }
mutation ProbeUpsertBookCapability { upsert_book { id } }
```

The limited token received HTTP 403 for **both operations**, before GraphQL
validation, with the exact sanitized body:

```json
{"error":"insufficient_scope","error_description":"Missing scopes: write:catalog:append","scope":"write:catalog:append"}
```

The ordinary full token received HTTP 200 for both, with no `data` and exactly
one `validation-failed` error. For `insert_edition`, the message was
`missing required field 'book_id'` and the path was
`$.selectionSet.insert_edition.args.book_id`. For `upsert_book`, they were
`missing required field 'book'` and `$.selectionSet.upsert_book.args.book`.

These observations cover the tested credentials and argument-free requests
on September 27, 2026,
not valid-input creates, every denial variant, or future server behavior.
No new live request was made for this maintainer-feedback update.

## Cache and explicit refresh

Permission evidence is kept in memory for each profile and token until the
user requests a refresh or the token changes. View Details requests evidence
on demand; sync startup does not run probes. Allowed, denied, and unverified
results use the same lifetime. The write-authorized
`POST /api/profiles/{id}/edition-capability/refresh` route reruns both probes
without resetting the profile rate limiter. The View Details **Refresh
permissions** action allows recovery after token scopes are granted.

## Create-time denial handling

Both ebook insertion and regional audiobook import opt into the concrete
Hardcover client's minimum-mutation-budget boundary. The exact HTTP 403 body
above becomes `ErrMutationScopeDenied`, and the create API returns HTTP 403
with the no-edition-created message. This remains the backstop if permission
changes after a cached capability result.

The former `field 'insert_edition' not found in type: 'mutation_root'` matcher
is not supported by the recorded live denial evidence. Such unknown GraphQL
responses remain ambiguous for real creates and `unverified` for probes,
rather than being labeled a confirmed missing scope. Recognizing another
scope-denial shape requires additional runtime evidence. Existing HTTP/client
boundary tests cover both known denials and unknown-field responses.

## External validation dependency

These probes are mutation requests. Their safety depends on Hardcover keeping
`insert_edition`'s `book_id` and `edition` arguments and `upsert_book`'s `book`
argument required, and rejecting omitted required arguments before resolver
execution. The checked-in schema declares these arguments non-null, and the
recorded live responses confirm validation rejection for the tested requests.

If the server changes the required arguments or validation behavior, an
argument-free request could reach a mutation resolver. Strict response
matching does not prevent that execution: it only returns `unverified` for
unexpected responses, including successful responses. Reassess or disable the
probes if that dependency changes. Dry-run profiles send no probe requests.
