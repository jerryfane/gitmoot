package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
)

// KillDelegationTree is the operator kill switch (#341). It marks the delegation
// tree rooted at rootJobID as killed so the engine and daemon stop expanding it,
// then returns the root job.
//
// The kill is graceful, not a hard stop: it does NOT cancel in-flight jobs. The
// flag takes effect at the two expansion points:
//
//   - the engine's dispatchDelegations sees IsRootJobKilled as its first backstop
//     and, on the coordinator's next continuation, routes through the #305 graceful
//     finalize continuation (synthesize what completed → stop) instead of
//     dispatching the next generation; and
//   - the daemon skips queued children of a killed root, so no new child starts.
//
// In-flight children run to completion normally. The root job must exist; an
// unknown id is an error so an operator typo does not silently no-op.
func KillDelegationTree(ctx context.Context, store *db.Store, rootJobID string) (db.Job, error) {
	if store == nil {
		return db.Job{}, fmt.Errorf("store is required")
	}
	rootJobID = strings.TrimSpace(rootJobID)
	if rootJobID == "" {
		return db.Job{}, fmt.Errorf("root job id is required")
	}
	root, err := store.GetJob(ctx, rootJobID)
	if err != nil {
		return db.Job{}, fmt.Errorf("kill delegation tree %s: %w", rootJobID, err)
	}
	if err := store.SetRootJobKilled(ctx, root.ID); err != nil {
		return db.Job{}, err
	}
	if err := store.AddJobEvent(ctx, db.JobEvent{
		JobID:   root.ID,
		Kind:    "delegation_killed",
		Message: fmt.Sprintf("operator killed delegation tree rooted at %s; new delegations and queued children stop, in-flight jobs finish", root.ID),
	}); err != nil {
		return db.Job{}, err
	}
	return store.GetJob(ctx, root.ID)
}
