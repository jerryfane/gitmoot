package db

import "context"

type Store interface {
	Close() error
	Ping(ctx context.Context) error
}

type NoopStore struct{}

func (NoopStore) Close() error {
	return nil
}

func (NoopStore) Ping(context.Context) error {
	return nil
}
