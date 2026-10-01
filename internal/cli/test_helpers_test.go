package cli

import "context"

type funcRunner func(context.Context, string, ...string) ([]byte, error)

func (f funcRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

type mapEnvironment map[string]string

func (m mapEnvironment) LookupEnv(key string) (string, bool) {
	value, ok := m[key]
	return value, ok
}
