package daemon

import (
	"context"
	"fmt"

	"github.com/gitmoot/gitmoot/internal/db"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

// rearmAutoMergeDisabledTasks restores only tasks that were deliberately parked
// by the default native auto_merge=false policy. The state transition is a CAS,
// so an enabled policy re-arms each task once and cannot revive a future
// leave-open reason (for example, branch-protection requirements).
func (d Daemon) rearmAutoMergeDisabledTasks(ctx context.Context) error {
	if d.AutoMergeEnabled == nil || !d.AutoMergeEnabled(d.Repo.FullName()) {
		return nil
	}
	tasks, err := d.Store.ListTasksByRepoState(ctx, d.Repo.FullName(), string(workflow.TaskAwaitingHumanMerge))
	if err != nil {
		return err
	}
	var firstErr error
	for _, task := range tasks {
		events, err := d.Store.ListTaskEvents(ctx, task.ID)
		if err != nil {
			err = fmt.Errorf("read auto-merge park events for task %s: %w", task.ID, err)
			d.logf("auto-merge re-arm skipped for task %s: %v", task.ID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !autoMergeDisabledParked(events) {
			continue
		}
		if _, _, err := d.Store.TransitionTaskStateWithEvent(ctx, task.ID,
			[]string{string(workflow.TaskAwaitingHumanMerge)}, string(workflow.TaskReadyToMerge),
			"task_awaiting_human_merge_rearmed", "native auto-merge enabled"); err != nil {
			err = fmt.Errorf("re-arm task %s: %w", task.ID, err)
			d.logf("auto-merge re-arm skipped for task %s: %v", task.ID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func autoMergeDisabledParked(events []db.TaskEvent) bool {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.ToState != string(workflow.TaskAwaitingHumanMerge) {
			continue
		}
		return event.Reason == workflow.MergeLeaveOpenAutoMergeDisabledReason
	}
	return false
}
