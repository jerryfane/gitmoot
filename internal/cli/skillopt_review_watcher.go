package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jerryfane/gitmoot/internal/artifact"
	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/daemon"
	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/feedback"
	"github.com/jerryfane/gitmoot/internal/github"
	"github.com/jerryfane/gitmoot/internal/skillopt"
)

const skillOptReviewWatchErrorMarker = "<!-- gitmoot:skillopt-review-watch-error -->"
const skillOptReviewWatchSuccessMarker = "<!-- gitmoot:skillopt-review-watch-success -->"

func pollSkillOptReviewWatches(ctx context.Context, paths config.Paths, store *db.Store, blobStore artifact.Store, gh github.Client, stdout io.Writer, dryRun bool, home string) (int, error) {
	if store == nil {
		return 0, fmt.Errorf("store is required")
	}
	if gh == nil {
		return 0, fmt.Errorf("github client is required")
	}
	watches, err := store.ListSkillOptReviewWatches(ctx, db.SkillOptReviewWatchStatusWatching)
	if err != nil {
		return 0, err
	}
	importedWatches, err := store.ListSkillOptReviewWatches(ctx, db.SkillOptReviewWatchStatusImported)
	if err != nil {
		return 0, err
	}
	watches = append(watches, importedWatches...)
	polled := 0
	var pollErr error
	for _, watch := range watches {
		if dryRun {
			writeLine(stdout, "skillopt review watch dry_run repo=%s issue=%d run=%s", watch.Repo, watch.IssueNumber, watch.RunID)
			polled++
			continue
		}
		if err := pollSkillOptReviewWatch(ctx, paths, store, blobStore, gh, stdout, watch, home); err != nil {
			writeLine(stdout, "skillopt review watch %s#%d: %s", watch.Repo, watch.IssueNumber, err)
			pollErr = errors.Join(pollErr, err)
			continue
		}
		polled++
	}
	if len(watches) > 0 {
		writeLine(stdout, "polled %d skillopt review watches", polled)
	}
	return polled, pollErr
}

func pollSkillOptReviewWatch(ctx context.Context, paths config.Paths, store *db.Store, blobStore artifact.Store, gh github.Client, stdout io.Writer, watch db.SkillOptReviewWatch, home string) error {
	repo, err := daemon.ParseRepository(watch.Repo)
	if err != nil {
		return fmt.Errorf("skillopt review watch %s#%d: %w", watch.Repo, watch.IssueNumber, err)
	}
	comments, err := gh.ListIssueComments(ctx, repo, watch.IssueNumber)
	if err != nil {
		return fmt.Errorf("skillopt review watch %s#%d: list comments: %w", watch.Repo, watch.IssueNumber, err)
	}
	sort.SliceStable(comments, func(i, j int) bool {
		return comments[i].ID < comments[j].ID
	})
	successPosted := hasGitmootSkillOptReviewWatchSuccessComment(comments)
	if watch.Status == db.SkillOptReviewWatchStatusImported {
		result, err := skillOptReviewWatchImportResultFromStore(ctx, store, watch.RunID)
		if err != nil {
			return err
		}
		continuation := continueImportedSkillOptReviewWatchTraining(ctx, paths, store, watch, home)
		if err := acknowledgeAndCloseSkillOptReviewWatch(ctx, store, gh, repo, &watch, result, continuation, successPosted); err != nil {
			return err
		}
		writeLine(stdout, "acknowledged imported skillopt review feedback from %s#%d", watch.Repo, watch.IssueNumber)
		return nil
	}
	expected, err := skillOptReviewWatchExpectedItemIDs(watch)
	if err != nil {
		return fmt.Errorf("skillopt review watch %s#%d: %w", watch.Repo, watch.IssueNumber, err)
	}
	collector := feedback.GitHubCollector{BlobStore: blobStore, GitHub: gh}
	for _, comment := range comments {
		if comment.ID <= watch.LastSeenCommentID || isGitmootSkillOptReviewWatchComment(comment.Body) {
			continue
		}
		validation, err := feedback.ValidateGitHubReviewComment(comment.Body, watch.RunID, expected)
		if err != nil {
			if err := postSkillOptReviewWatchImportError(ctx, store, gh, repo, &watch, comment, err); err != nil {
				return err
			}
			continue
		}
		if !validation.Parseable {
			if err := store.UpsertSkillOptReviewWatch(ctx, watch); err != nil {
				return err
			}
			continue
		}
		result, err := collector.ImportComments(ctx, store, watch.RunID, []github.IssueComment{comment})
		if err != nil {
			if err := postSkillOptReviewWatchImportError(ctx, store, gh, repo, &watch, comment, err); err != nil {
				return err
			}
			continue
		}
		watch.Status = db.SkillOptReviewWatchStatusImported
		watch.LastSeenCommentID = comment.ID
		watch.LastImportErrorHash = ""
		if err := store.UpsertSkillOptReviewWatch(ctx, watch); err != nil {
			return err
		}
		continuation := continueSkillOptReviewWatchTraining(ctx, paths, store, watch, home)
		if err := acknowledgeAndCloseSkillOptReviewWatch(ctx, store, gh, repo, &watch, result, continuation, successPosted); err != nil {
			return err
		}
		writeLine(stdout, "imported %d skillopt review feedback events from %s#%d comment %d", result.Count(), watch.Repo, watch.IssueNumber, comment.ID)
		return nil
	}
	return store.UpsertSkillOptReviewWatch(ctx, watch)
}

type skillOptReviewWatchContinuation struct {
	SessionID string
	Phase     string
	Lines     []string
	Busy      bool
	Err       error
}

func continueSkillOptReviewWatchTraining(ctx context.Context, paths config.Paths, store *db.Store, watch db.SkillOptReviewWatch, home string) skillOptReviewWatchContinuation {
	iteration, err := store.GetSkillOptTrainIterationByEvalRun(ctx, watch.RunID)
	if err != nil {
		return skillOptReviewWatchContinuation{Err: fmt.Errorf("load train iteration for review run %s: %w", watch.RunID, err)}
	}
	output, err := continueSkillOptTrain(ctx, paths, store, skillOptTrainContinueRequest{
		Home:      home,
		SessionID: iteration.SessionID,
	})
	continuation := skillOptReviewWatchContinuation{
		SessionID: iteration.SessionID,
		Phase:     output.Summary.CurrentPhase,
		Lines:     append([]string(nil), output.Lines...),
		Err:       err,
	}
	if err != nil {
		continuation.Busy = isSkillOptTrainBusyError(err)
	}
	return continuation
}

func continueImportedSkillOptReviewWatchTraining(ctx context.Context, paths config.Paths, store *db.Store, watch db.SkillOptReviewWatch, home string) skillOptReviewWatchContinuation {
	iteration, err := store.GetSkillOptTrainIterationByEvalRun(ctx, watch.RunID)
	if err != nil {
		return skillOptReviewWatchContinuation{Err: fmt.Errorf("load train iteration for review run %s: %w", watch.RunID, err)}
	}
	if iteration.State == skillopt.TrainStateReviewPublished {
		return continueSkillOptReviewWatchTraining(ctx, paths, store, watch, home)
	}
	return skillOptReviewWatchContinuation{
		SessionID: iteration.SessionID,
		Phase:     iteration.State,
	}
}

func acknowledgeAndCloseSkillOptReviewWatch(ctx context.Context, store *db.Store, gh github.Client, repo github.Repository, watch *db.SkillOptReviewWatch, result feedback.ImportResult, continuation skillOptReviewWatchContinuation, successPosted bool) error {
	if !successPosted {
		body := skillOptReviewWatchSuccessComment(watch.RunID, result, continuation)
		if _, err := gh.PostIssueComment(ctx, repo, watch.IssueNumber, body); err != nil {
			return fmt.Errorf("skillopt review watch %s#%d: post import success: %w", watch.Repo, watch.IssueNumber, err)
		}
	}
	if !skillOptReviewWatchShouldCloseAfterContinuation(continuation) {
		return store.UpsertSkillOptReviewWatch(ctx, *watch)
	}
	if _, err := gh.CloseIssue(ctx, repo, watch.IssueNumber); err != nil {
		return fmt.Errorf("skillopt review watch %s#%d: close review issue: %w", watch.Repo, watch.IssueNumber, err)
	}
	watch.Status = db.SkillOptReviewWatchStatusClosed
	return store.UpsertSkillOptReviewWatch(ctx, *watch)
}

func skillOptReviewWatchImportResultFromStore(ctx context.Context, store *db.Store, runID string) (feedback.ImportResult, error) {
	events, err := store.ListFeedbackEvents(ctx, runID)
	if err != nil {
		return feedback.ImportResult{}, err
	}
	rankedEvents, err := store.ListRankedFeedbackEvents(ctx, runID)
	if err != nil {
		return feedback.ImportResult{}, err
	}
	return feedback.ImportResult{
		FeedbackEvents:       events,
		RankedFeedbackEvents: rankedEvents,
	}, nil
}

func skillOptReviewWatchExpectedItemIDs(watch db.SkillOptReviewWatch) ([]string, error) {
	content := strings.TrimSpace(watch.ExpectedItemIDsJSON)
	if content == "" {
		return nil, nil
	}
	var itemIDs []string
	if err := json.Unmarshal([]byte(content), &itemIDs); err != nil {
		return nil, fmt.Errorf("decode expected item ids: %w", err)
	}
	return itemIDs, nil
}

func isGitmootSkillOptReviewWatchComment(body string) bool {
	body = strings.TrimSpace(body)
	return strings.Contains(body, skillOptReviewWatchErrorMarker) ||
		strings.Contains(body, skillOptReviewWatchSuccessMarker) ||
		strings.Contains(body, "<!-- gitmoot:skillopt-feedback-packet -->") ||
		strings.HasPrefix(body, "# Gitmoot SkillOpt Feedback:")
}

func hasGitmootSkillOptReviewWatchSuccessComment(comments []github.IssueComment) bool {
	for _, comment := range comments {
		if strings.Contains(comment.Body, skillOptReviewWatchSuccessMarker) {
			return true
		}
	}
	return false
}

func postSkillOptReviewWatchImportError(ctx context.Context, store *db.Store, gh github.Client, repo github.Repository, watch *db.SkillOptReviewWatch, comment github.IssueComment, importErr error) error {
	message := strings.TrimSpace(importErr.Error())
	if message == "" {
		message = "feedback comment could not be imported"
	}
	hash := skillOptReviewWatchImportErrorHash(watch.RunID, comment.ID, message)
	if hash != watch.LastImportErrorHash {
		body := skillOptReviewWatchImportErrorComment(watch.RunID, comment.ID, message)
		if _, err := gh.PostIssueComment(ctx, repo, watch.IssueNumber, body); err != nil {
			return fmt.Errorf("skillopt review watch %s#%d: post import error: %w", watch.Repo, watch.IssueNumber, err)
		}
		watch.LastImportErrorHash = hash
	}
	watch.Status = db.SkillOptReviewWatchStatusWatching
	return store.UpsertSkillOptReviewWatch(ctx, *watch)
}

func skillOptReviewWatchImportErrorHash(runID string, commentID int64, message string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(runID) + "\n" + fmt.Sprint(commentID) + "\n" + strings.TrimSpace(message)))
	return hex.EncodeToString(sum[:12])
}

func skillOptReviewWatchImportErrorComment(runID string, commentID int64, message string) string {
	var builder strings.Builder
	builder.WriteString(skillOptReviewWatchErrorMarker)
	builder.WriteString("\nGitmoot could not import the SkillOpt review feedback yet.\n\n")
	fmt.Fprintf(&builder, "- run_id: `%s`\n", strings.TrimSpace(runID))
	fmt.Fprintf(&builder, "- comment_id: `%d`\n", commentID)
	builder.WriteString("- error: ")
	builder.WriteString(strings.ReplaceAll(message, "\n", " "))
	builder.WriteString("\n\nPlease reply with a complete fenced `yaml` block for the expected `run_id` and all review `item_id` values.\n")
	return builder.String()
}

func skillOptReviewWatchSuccessComment(runID string, result feedback.ImportResult, continuation skillOptReviewWatchContinuation) string {
	var builder strings.Builder
	builder.WriteString(skillOptReviewWatchSuccessMarker)
	builder.WriteString("\nGitmoot imported the SkillOpt review feedback.\n\n")
	fmt.Fprintf(&builder, "- run_id: `%s`\n", strings.TrimSpace(runID))
	fmt.Fprintf(&builder, "- feedback_events: `%d`\n", result.Count())
	if items := importedSkillOptReviewWatchItemIDs(result); len(items) > 0 {
		fmt.Fprintf(&builder, "- item_ids: `%s`\n", strings.Join(items, ", "))
	}
	if continuation.SessionID != "" {
		fmt.Fprintf(&builder, "- train_session: `%s`\n", continuation.SessionID)
	}
	switch {
	case continuation.Err == nil:
		if continuation.Phase != "" {
			fmt.Fprintf(&builder, "- train_phase: `%s`\n", continuation.Phase)
		}
		builder.WriteString("- next: Gitmoot continued the train loop after import.\n")
	case continuation.Busy:
		builder.WriteString("- next: Feedback is imported. A train operation is already active; it can pick this up, or run `gitmoot skillopt train continue` again after it finishes.\n")
	default:
		builder.WriteString("- next: Feedback is imported, but automatic continuation failed. Run `gitmoot skillopt train continue` manually after checking daemon logs.\n")
		fmt.Fprintf(&builder, "- continue_error: `%s`\n", strings.ReplaceAll(continuation.Err.Error(), "`", "'"))
	}
	return builder.String()
}

func importedSkillOptReviewWatchItemIDs(result feedback.ImportResult) []string {
	seen := map[string]struct{}{}
	var itemIDs []string
	for _, event := range result.FeedbackEvents {
		itemID := strings.TrimSpace(event.ItemID)
		if itemID == "" {
			continue
		}
		if _, ok := seen[itemID]; !ok {
			seen[itemID] = struct{}{}
			itemIDs = append(itemIDs, itemID)
		}
	}
	for _, event := range result.RankedFeedbackEvents {
		itemID := strings.TrimSpace(event.ItemID)
		if itemID == "" {
			continue
		}
		if _, ok := seen[itemID]; !ok {
			seen[itemID] = struct{}{}
			itemIDs = append(itemIDs, itemID)
		}
	}
	sort.Strings(itemIDs)
	return itemIDs
}

func isSkillOptTrainBusyError(err error) bool {
	return errors.Is(err, errSkillOptTrainGenerationBusy) ||
		errors.Is(err, errSkillOptTrainReviewBusy) ||
		errors.Is(err, errSkillOptTrainOptimizerBusy) ||
		errors.Is(err, errSkillOptTrainCandidateReviewBusy) ||
		errors.Is(err, errSkillOptTrainStartNextBusy)
}

func skillOptReviewWatchShouldCloseAfterContinuation(continuation skillOptReviewWatchContinuation) bool {
	return continuation.Err == nil && continuation.Phase != skillopt.TrainStateReviewPublished
}
