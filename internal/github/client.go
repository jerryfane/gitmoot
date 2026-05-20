package github

import "context"

type Client interface {
	Ping(ctx context.Context) error
}

type NoopClient struct{}

func (NoopClient) Ping(context.Context) error {
	return nil
}
