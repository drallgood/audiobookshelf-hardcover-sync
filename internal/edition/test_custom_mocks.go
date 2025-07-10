package edition

import (
	"context"
)

// TestResponseAdapter is a helper for testing that adapts GraphQL responses
// between implementation-specific response types and test mock expectations.
func TestResponseAdapter(ctx context.Context, client HardcoverClient, mutation string, variables map[string]interface{}, response interface{}) error {
	// Simply pass through to GraphQLMutation
	return client.GraphQLMutation(ctx, mutation, variables, response)
}
