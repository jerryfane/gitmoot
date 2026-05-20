package workflow

type TaskState string

const (
	TaskPlanned TaskState = "planned"
)

type Task struct {
	ID     string
	Title  string
	State  TaskState
	Branch string
}
