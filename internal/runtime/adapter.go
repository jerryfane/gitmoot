package runtime

import "context"

type Agent struct {
	Name         string
	Role         string
	Runtime      string
	RuntimeRef   string
	RepoScope    string
	Capabilities []string
}

type Job struct {
	ID          string
	AgentName   string
	Action      string
	Prompt      string
	Repository  string
	PullRequest int
}

type Result struct {
	Decision string
	Summary  string
	Raw      string
}

type Adapter interface {
	Name() string
	Validate(ctx context.Context, agent Agent) error
	Deliver(ctx context.Context, agent Agent, job Job) (Result, error)
	Health(ctx context.Context, agent Agent) error
	Capabilities(ctx context.Context) ([]string, error)
}
