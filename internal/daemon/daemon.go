package daemon

import "context"

type Daemon struct{}

func (Daemon) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
