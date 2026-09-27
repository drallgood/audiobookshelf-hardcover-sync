package hardcover

import (
	"context"
	"errors"
	"net/http"
	"strings"
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

const insertEditionCapabilityProbeMutation = `
mutation ProbeInsertEditionCapability {
	insert_edition(book_id: -1, edition: {dto: {title: "Capability probe", edition_format: "ebook", reading_format_id: 4}}) {
		id
		errors
	}
}`

const insertEditionCapabilityProbeNotFound = "GraphQL error: Couldn't find Book"
const editionCapabilityProbeMutationReserve = 10 * time.Millisecond

// ProbeInsertEditionCapability checks ebook insert_edition capability using
// Hardcover's observed pre-execution behavior for the impossible book ID -1.
// It only reports allowed for the exact expected book-not-found response and
// denied for the known insufficient_scope HTTP 403 response. Every other
// response is unverified. Dry run short-circuits as allowed without a request.
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

	var response struct {
		InsertEdition struct {
			ID     *int     `json:"id"`
			Errors []string `json:"errors"`
		} `json:"insert_edition"`
	}
	err := c.GraphQLMutation(probeCtx, insertEditionCapabilityProbeMutation, nil, &response)
	if isExpectedInsertEditionCapabilityNotFound(err) {
		return EditionCapabilityProbeAllowed
	}
	if isInsertEditionCapabilityScopeDenial(err) {
		return EditionCapabilityProbeDenied
	}
	return EditionCapabilityProbeUnverified
}

func isExpectedInsertEditionCapabilityNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.TrimSpace(err.Error())
	if message == insertEditionCapabilityProbeNotFound {
		return true
	}
	return message == ErrMutationOutcomeAmbiguous.Error()+": "+insertEditionCapabilityProbeNotFound
}

func isInsertEditionCapabilityScopeDenial(err error) bool {
	if err == nil || !errors.Is(err, ErrMutationScopeDenied) {
		return false
	}
	var httpErr *HTTPError
	return errors.As(err, &httpErr) &&
		httpErr.StatusCode == http.StatusForbidden &&
		knownMutationScopeHTTPDenial(mutationOperation, insertEditionCapabilityProbeMutation, httpErr.StatusCode, httpErr.Body)
}
