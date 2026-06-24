package workflow

type TaskState string

const (
	TaskPlanned          TaskState = "planned"
	TaskImplementing     TaskState = "implementing"
	TaskPullRequestOpen  TaskState = "pr_open"
	TaskReviewing        TaskState = "reviewing"
	TaskChangesRequested TaskState = "changes_requested"
	TaskReadyToMerge     TaskState = "ready_to_merge"
	TaskMerged           TaskState = "merged"
	TaskBlocked          TaskState = "blocked"
	// TaskAwaitingHuman is the resumable pause state a task enters when a
	// delegation fails under the escalate_human failure_policy (#340). Unlike
	// TaskBlocked (terminal), it is a durable human-in-the-loop pause: the tree
	// enqueues no continuation and consumes no compute until an operator resumes
	// it via `/gitmoot resume <jobID> retry|continue|abort`.
	TaskAwaitingHuman TaskState = "awaiting_human"
)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobBlocked   JobState = "blocked"
	JobFailed    JobState = "failed"
	JobSucceeded JobState = "succeeded"
	JobCancelled JobState = "cancelled"
)

type Task struct {
	ID     string
	Title  string
	State  TaskState
	Branch string
}
