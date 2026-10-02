package logger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromContextWithoutLogger(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "nil", ctx: nil},
		{name: "background", ctx: context.Background()},
		{name: "todo", ctx: context.TODO()},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Nil(t, FromContext(test.ctx))
		})
	}
}

func TestContextHelpersIgnoreNilLogger(t *testing.T) {
	type contextValueKey struct{}

	for _, test := range []struct {
		name string
		wrap func(context.Context, *Logger) context.Context
	}{
		{name: "WithLogger", wrap: WithLogger},
		{name: "NewContext", wrap: NewContext},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextValueKey{}, "request-id"))
			defer cancel()

			result := test.wrap(ctx, nil)
			assert.Equal(t, "request-id", result.Value(contextValueKey{}))
			assert.Nil(t, FromContext(result))

			cancel()
			assert.ErrorIs(t, result.Err(), context.Canceled)
		})
	}
}
