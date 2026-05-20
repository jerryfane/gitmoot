package prompts

type JobPrompt struct {
	Repo        string
	Branch      string
	PullRequest int
	Task        string
	Action      string
	Constraints []string
}
