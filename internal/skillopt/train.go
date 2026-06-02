package skillopt

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
)

const (
	TrainStateRequestConfirmed         = "request_confirmed"
	TrainStateWorkspaceReady           = "workspace_ready"
	TrainStateItemsReady               = "items_ready"
	TrainStateOptionsGenerated         = "options_generated"
	TrainStateReviewPublished          = "review_published"
	TrainStateFeedbackSynced           = "feedback_synced"
	TrainStateTrainingPackageCreated   = "training_package_created"
	TrainStateOptimizerCompleted       = "optimizer_completed"
	TrainStateCandidateCreated         = "candidate_created"
	TrainStateCandidateReviewPublished = "candidate_review_published"
	TrainStateCandidatePromoted        = "candidate_promoted"
	TrainStateCandidateRejected        = "candidate_rejected"
	TrainStateRunAbandoned             = "run_abandoned"
)

type TrainStatusCounts struct {
	ReviewItems          int
	FeedbackEvents       int
	RankedFeedbackEvents int
	PairwisePreferences  int
}

type TrainStatusSummary struct {
	SessionID        string
	IterationID      string
	CurrentPhase     string
	CompletedSteps   []string
	BlockedStep      string
	NextAction       string
	IssueURL         string
	PullRequestURL   string
	CandidateVersion string
	FeedbackCount    int
}

var orderedTrainStates = []string{
	TrainStateRequestConfirmed,
	TrainStateWorkspaceReady,
	TrainStateItemsReady,
	TrainStateOptionsGenerated,
	TrainStateReviewPublished,
	TrainStateFeedbackSynced,
	TrainStateTrainingPackageCreated,
	TrainStateOptimizerCompleted,
	TrainStateCandidateCreated,
	TrainStateCandidateReviewPublished,
}

var trainStateOrder = func() map[string]int {
	order := make(map[string]int, len(orderedTrainStates))
	for index, state := range orderedTrainStates {
		order[state] = index
	}
	return order
}()

func NormalizeTrainState(state string) string {
	return strings.TrimSpace(strings.ToLower(state))
}

func IsTerminalTrainState(state string) bool {
	switch NormalizeTrainState(state) {
	case TrainStateCandidatePromoted, TrainStateCandidateRejected, TrainStateRunAbandoned:
		return true
	default:
		return false
	}
}

func CanTransitionTrainIteration(from string, to string) error {
	from = NormalizeTrainState(from)
	to = NormalizeTrainState(to)
	if from == "" {
		from = TrainStateRequestConfirmed
	}
	if to == "" {
		return errors.New("target train state is required")
	}
	if from == to {
		return nil
	}
	if IsTerminalTrainState(from) {
		return fmt.Errorf("cannot transition train iteration from terminal state %s to %s", from, to)
	}
	if IsTerminalTrainState(to) {
		if to != TrainStateRunAbandoned && from != TrainStateCandidateReviewPublished {
			return fmt.Errorf("cannot transition train iteration from %s to %s before candidate review is published", from, to)
		}
		return nil
	}
	fromIndex, ok := trainStateOrder[from]
	if !ok {
		return fmt.Errorf("unknown source train state %q", from)
	}
	toIndex, ok := trainStateOrder[to]
	if !ok {
		return fmt.Errorf("unknown target train state %q", to)
	}
	if toIndex != fromIndex+1 {
		if fromIndex+1 >= len(orderedTrainStates) {
			return fmt.Errorf("cannot transition train iteration from %s to %s; promote, reject, or abandon the iteration", from, to)
		}
		return fmt.Errorf("cannot transition train iteration from %s to %s; next required state is %s", from, to, orderedTrainStates[fromIndex+1])
	}
	return nil
}

func CanStartNextTrainIteration(previous db.SkillOptTrainIteration) error {
	state := NormalizeTrainState(previous.State)
	switch state {
	case TrainStateCandidatePromoted, TrainStateRunAbandoned:
		return nil
	case TrainStateCandidateRejected:
		if strings.TrimSpace(previous.DecisionReason) == "" {
			return errors.New("cannot start next train iteration after candidate rejection without a decision reason")
		}
		return nil
	default:
		return fmt.Errorf("cannot start next train iteration while previous iteration %s is in state %s", previous.ID, state)
	}
}

func BuildTrainStatusSummary(session db.SkillOptTrainSession, iteration *db.SkillOptTrainIteration, counts TrainStatusCounts) TrainStatusSummary {
	summary := TrainStatusSummary{
		SessionID:     strings.TrimSpace(session.ID),
		CurrentPhase:  NormalizeTrainState(session.State),
		FeedbackCount: counts.FeedbackEvents + counts.RankedFeedbackEvents,
	}
	if summary.CurrentPhase == "" {
		summary.CurrentPhase = TrainStateRequestConfirmed
	}
	if iteration == nil {
		summary.CompletedSteps = completedTrainSteps(summary.CurrentPhase)
		summary.BlockedStep, summary.NextAction = trainBlockedStepAndAction(summary.CurrentPhase)
		return summary
	}
	summary.IterationID = strings.TrimSpace(iteration.ID)
	summary.CurrentPhase = NormalizeTrainState(iteration.State)
	if summary.CurrentPhase == "" {
		summary.CurrentPhase = TrainStateRequestConfirmed
	}
	summary.IssueURL = strings.TrimSpace(iteration.IssueURL)
	summary.PullRequestURL = strings.TrimSpace(iteration.PullRequestURL)
	summary.CandidateVersion = strings.TrimSpace(iteration.CandidateVersionID)
	summary.CompletedSteps = completedTrainSteps(summary.CurrentPhase)
	summary.BlockedStep, summary.NextAction = trainBlockedStepAndAction(summary.CurrentPhase)
	return summary
}

func completedTrainSteps(state string) []string {
	state = NormalizeTrainState(state)
	index, ok := trainStateOrder[state]
	if !ok {
		if IsTerminalTrainState(state) {
			if state == TrainStateRunAbandoned {
				return nil
			}
			return append([]string{}, orderedTrainStates...)
		}
		return nil
	}
	return append([]string{}, orderedTrainStates[:index+1]...)
}

func trainBlockedStepAndAction(state string) (string, string) {
	switch NormalizeTrainState(state) {
	case TrainStateRequestConfirmed:
		return TrainStateWorkspaceReady, "prepare or select the training workspace"
	case TrainStateWorkspaceReady:
		return TrainStateItemsReady, "add diverse training items"
	case TrainStateItemsReady:
		return TrainStateOptionsGenerated, "generate review options with temporary agents"
	case TrainStateOptionsGenerated:
		return TrainStateReviewPublished, "publish the human review packet"
	case TrainStateReviewPublished:
		return TrainStateFeedbackSynced, "sync human feedback from the review surface"
	case TrainStateFeedbackSynced:
		return TrainStateTrainingPackageCreated, "export the training package before running the optimizer"
	case TrainStateTrainingPackageCreated:
		return TrainStateOptimizerCompleted, "run the external gitmoot-skillopt optimizer"
	case TrainStateOptimizerCompleted:
		return TrainStateCandidateCreated, "import the optimizer candidate package"
	case TrainStateCandidateCreated:
		return TrainStateCandidateReviewPublished, "publish candidate diff and preview review"
	case TrainStateCandidateReviewPublished:
		return "candidate decision", "promote, reject with a reason, or abandon the iteration"
	case TrainStateCandidatePromoted:
		return "", "start the next iteration from the promoted candidate or stop"
	case TrainStateCandidateRejected:
		return "", "start another iteration from the previous base or stop"
	case TrainStateRunAbandoned:
		return "", "training run is abandoned"
	default:
		return "state", "inspect train session state"
	}
}
