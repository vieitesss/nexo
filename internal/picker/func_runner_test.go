package picker

import "context"

type funcRunner func(context.Context, string, ...string) ([]byte, error)

func (f funcRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}
