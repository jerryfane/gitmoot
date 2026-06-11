package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/github"
)

type decisionFakeGitHub struct {
	github.NoopClient
	postedRepo string
	postedNum  int64
	postedBody string
	closedNum  int64
	postErr    error
}

func (f *decisionFakeGitHub) PostIssueComment(_ context.Context, repo github.Repository, n int64, body string) (github.IssueComment, error) {
	if f.postErr != nil {
		return github.IssueComment{}, f.postErr
	}
	f.postedRepo, f.postedNum, f.postedBody = repo.FullName(), n, body
	return github.IssueComment{URL: "https://github.com/o/r/issues/2#issuecomment-1"}, nil
}

func (f *decisionFakeGitHub) CloseIssue(_ context.Context, _ github.Repository, n int64) (github.Issue, error) {
	f.closedNum = n
	return github.Issue{}, nil
}

func TestPostSkillOptTrainCandidateDecisionComment(t *testing.T) {
	iter := db.SkillOptTrainIteration{IssueRepo: "o/r", IssueNumber: 2}

	t.Run("promote comments and closes the issue", func(t *testing.T) {
		fake := &decisionFakeGitHub{}
		restore := replaceSkillOptGitHubClient(fake)
		defer restore()
		result := skillOptTrainCandidateDecisionResult{Decided: true, Decision: "promoted", CandidateVersionID: "agent@v4"}
		notice := postSkillOptTrainCandidateDecisionComment(context.Background(), iter, result, skillOptTrainOptimizerContinueRequestForTest("promote"))
		if fake.postedNum != 2 || fake.postedRepo != "o/r" {
			t.Fatalf("comment posted to %s#%d, want o/r#2", fake.postedRepo, fake.postedNum)
		}
		if !strings.Contains(fake.postedBody, skillOptTrainDecisionMarker) || !strings.Contains(fake.postedBody, "Promoted `agent@v4`") {
			t.Fatalf("body = %q", fake.postedBody)
		}
		if fake.closedNum != 2 {
			t.Fatalf("issue should be closed, closedNum=%d", fake.closedNum)
		}
		if !strings.Contains(notice, "closed it") {
			t.Fatalf("notice = %q", notice)
		}
	})

	t.Run("reject includes the reason", func(t *testing.T) {
		fake := &decisionFakeGitHub{}
		restore := replaceSkillOptGitHubClient(fake)
		defer restore()
		req := skillOptTrainContinueRequest{RejectCandidate: "agent@v4", DecisionReason: "weak hero copy"}
		result := skillOptTrainCandidateDecisionResult{Decided: true, Decision: "rejected", CandidateVersionID: "agent@v4"}
		postSkillOptTrainCandidateDecisionComment(context.Background(), iter, result, req)
		if !strings.Contains(fake.postedBody, "Rejected `agent@v4`") || !strings.Contains(fake.postedBody, "Reason: weak hero copy") {
			t.Fatalf("body = %q", fake.postedBody)
		}
	})

	t.Run("post error yields a warning, no panic", func(t *testing.T) {
		fake := &decisionFakeGitHub{postErr: errors.New("rate limited")}
		restore := replaceSkillOptGitHubClient(fake)
		defer restore()
		result := skillOptTrainCandidateDecisionResult{Decided: true, Decision: "promoted", CandidateVersionID: "agent@v4"}
		notice := postSkillOptTrainCandidateDecisionComment(context.Background(), iter, result, skillOptTrainOptimizerContinueRequestForTest("promote"))
		if !strings.Contains(notice, "warning") {
			t.Fatalf("notice = %q, want a warning", notice)
		}
		if fake.closedNum != 0 {
			t.Fatal("a failed comment must not close the issue")
		}
	})

	t.Run("no issue or PR → no post", func(t *testing.T) {
		fake := &decisionFakeGitHub{}
		restore := replaceSkillOptGitHubClient(fake)
		defer restore()
		result := skillOptTrainCandidateDecisionResult{Decided: true, Decision: "promoted", CandidateVersionID: "agent@v4"}
		notice := postSkillOptTrainCandidateDecisionComment(context.Background(), db.SkillOptTrainIteration{}, result, skillOptTrainOptimizerContinueRequestForTest("promote"))
		if notice != "" || fake.postedNum != 0 {
			t.Fatalf("no issue should mean no post: notice=%q postedNum=%d", notice, fake.postedNum)
		}
	})
}

func skillOptTrainOptimizerContinueRequestForTest(kind string) skillOptTrainContinueRequest {
	if kind == "promote" {
		return skillOptTrainContinueRequest{PromoteCandidate: "agent@v4"}
	}
	return skillOptTrainContinueRequest{RejectCandidate: "agent@v4"}
}

func TestReviewWatcherSkipsDecisionComment(t *testing.T) {
	body := skillOptTrainDecisionMarker + "\n✅ Promoted `agent@v4` from gitmoot."
	if !isGitmootSkillOptReviewWatchComment(body) {
		t.Fatal("the decision comment must be skipped by the review watcher (no re-import loop)")
	}
}
