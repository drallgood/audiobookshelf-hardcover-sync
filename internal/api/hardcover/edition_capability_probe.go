package hardcover

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// EditionCapabilityProbeState is the evidence returned by a safe catalogue
// write probe. Unverified means the response did not prove either capability
// or denial.
type EditionCapabilityProbeState string

const (
	EditionCapabilityProbeAllowed    EditionCapabilityProbeState = "allowed"
	EditionCapabilityProbeDenied     EditionCapabilityProbeState = "denied"
	EditionCapabilityProbeUnverified EditionCapabilityProbeState = "unverified"
)

const insertEditionCapabilityProbeMutation = `mutation ProbeInsertEditionCapability { insert_edition { id } }`

const editionCapabilityProbeMutationReserve = 10 * time.Millisecond
const upsertBookCapabilityProbeMutation = `mutation ProbeUpsertBookCapability { upsert_book { id } }`
const insertEditionCapabilityProbeMissingArgument = "missing required field 'book_id'"
const insertEditionCapabilityProbeMissingArgumentPath = "$.selectionSet.insert_edition.args.book_id"
const upsertBookCapabilityProbeMissingArgument = "missing required field 'book'"
const upsertBookCapabilityProbeMissingArgumentPath = "$.selectionSet.upsert_book.args.book"

// ProbeInsertEditionCapability checks ebook insert_edition capability by
// omitting the required book_id argument. Hardcover rejects this request during
// GraphQL validation, before a catalogue write can run. Only that exact
// validation response proves the mutation is available; the known HTTP 403
// scope response proves denial. Dry run short-circuits without a request.
func (c *Client) ProbeInsertEditionCapability(ctx context.Context) EditionCapabilityProbeState {
	if c.dryRun {
		return EditionCapabilityProbeAllowed
	}

	timeout := DefaultTimeout
	if c.httpClient != nil && c.httpClient.Timeout > 0 {
		timeout = c.httpClient.Timeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	probeCtx = WithMinimumMutationBudget(probeCtx, editionCapabilityProbeMutationReserve)

	var response struct{}
	err := c.GraphQLMutation(probeCtx, insertEditionCapabilityProbeMutation, nil, &response)
	if isExpectedInsertEditionCapabilityMissingArgument(err) {
		return EditionCapabilityProbeAllowed
	}
	if isEditionCapabilityScopeDenial(err, insertEditionCapabilityProbeMutation) {
		return EditionCapabilityProbeDenied
	}
	return EditionCapabilityProbeUnverified
}

// ProbeUpsertBookCapability checks audiobook upsert_book capability by
// omitting its required book argument. Hardcover rejects this request during
// GraphQL validation, before a catalogue write can run. Only that exact
// validation response proves the mutation is available; the known HTTP 403
// scope response proves denial. Dry run short-circuits without a request.
func (c *Client) ProbeUpsertBookCapability(ctx context.Context) EditionCapabilityProbeState {
	if c.dryRun {
		return EditionCapabilityProbeAllowed
	}

	timeout := DefaultTimeout
	if c.httpClient != nil && c.httpClient.Timeout > 0 {
		timeout = c.httpClient.Timeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	probeCtx = WithMinimumMutationBudget(probeCtx, editionCapabilityProbeMutationReserve)

	var response struct{}
	err := c.GraphQLMutation(probeCtx, upsertBookCapabilityProbeMutation, nil, &response)
	if isExpectedUpsertBookCapabilityMissingArgument(err) {
		return EditionCapabilityProbeAllowed
	}
	if isEditionCapabilityScopeDenial(err, upsertBookCapabilityProbeMutation) {
		return EditionCapabilityProbeDenied
	}
	return EditionCapabilityProbeUnverified
}

func isExpectedUpsertBookCapabilityMissingArgument(err error) bool {
	return errors.Is(err, errUpsertBookMissingRequiredArgument)
}

func isExpectedInsertEditionCapabilityMissingArgument(err error) bool {
	return errors.Is(err, errInsertEditionMissingRequiredArgument)
}

func isEditionCapabilityScopeDenial(err error, query string) bool {
	if err == nil || !errors.Is(err, ErrMutationScopeDenied) {
		return false
	}
	var httpErr *HTTPError
	return errors.As(err, &httpErr) &&
		httpErr.StatusCode == http.StatusForbidden &&
		knownMutationScopeHTTPDenial(mutationOperation, query, httpErr.StatusCode, httpErr.Body)
}
