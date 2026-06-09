package skillopt

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTrainInitConfigRenderParseRoundTrip(t *testing.T) {
	config := trainInitTestConfig()

	rendered, err := RenderTrainInitConfig(config)
	if err != nil {
		t.Fatalf("RenderTrainInitConfig: %v", err)
	}
	content := string(rendered)
	for _, want := range []string{
		`name = "smithyx-one-shot-voice"`,
		`template = "smithyx-x-posts"`,
		`template_version = "smithyx-x-posts@v3"`,
		`review_repo = "jerryfane/gitmoot-x-posts-smithyx"`,
		`mode = "explore"`,
		`exploration_level = "high"`,
		`options = 4`,
		`[generation]`,
		`source = "current_skill"`,
		`[evaluator]`,
		`mode = "judge"`,
		`dimensions = "auto"`,
		`[optimizer]`,
		`skill_update_mode = "full_rewrite_minibatch"`,
		`optimizer_views = 4`,
		`retry_optimizer_views = "auto"`,
		`noop_retry_budget = 1`,
		`gate_reject_retry_budget = 3`,
		`wrong_artifact_retry_budget = 1`,
		`target_artifact_retry_budget = 2`,
		`hard_failure_retry_budget = 3`,
		`optimizer_backend = "codex"`,
		`target_backend = "codex"`,
		`evaluator_backend = "codex"`,
		`internal_target_adapter = "codex_exec"`,
		`[final_evaluator]`,
		`enabled = false`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, content)
		}
	}

	parsed, err := ParseTrainInitConfig(rendered)
	if err != nil {
		t.Fatalf("ParseTrainInitConfig: %v", err)
	}
	if !reflect.DeepEqual(parsed, config) {
		t.Fatalf("round trip mismatch\nparsed: %#v\nconfig: %#v", parsed, config)
	}
}

func TestWriteTrainInitScaffold(t *testing.T) {
	root := t.TempDir()
	scaffold := TrainInitScaffold{
		Config:          trainInitTestConfig(),
		TaskMarkdown:    "# Improve Smithyx replies\n\nUse the current review feedback.",
		ReviewItemsYAML: []byte("run_id: smithyx-one-shot-voice-trial-001\nitems: []"),
	}

	paths, err := WriteTrainInitScaffold(root, scaffold)
	if err != nil {
		t.Fatalf("WriteTrainInitScaffold: %v", err)
	}
	wantRoot := filepath.Join(root, ".gitmoot", TrainInitScaffoldDirName, scaffold.Config.Name)
	if paths.Root != wantRoot {
		t.Fatalf("root = %q, want %q", paths.Root, wantRoot)
	}
	for _, path := range []string{paths.ConfigPath, paths.TaskPath, paths.ReviewItemsPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected scaffold file %s: %v", path, err)
		}
	}
	taskContent, err := os.ReadFile(paths.TaskPath)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !strings.HasSuffix(string(taskContent), "\n") {
		t.Fatalf("task content should end with newline: %q", string(taskContent))
	}
	reviewContent, err := os.ReadFile(paths.ReviewItemsPath)
	if err != nil {
		t.Fatalf("read review items: %v", err)
	}
	if !strings.HasSuffix(string(reviewContent), "\n") {
		t.Fatalf("review item content should end with newline: %q", string(reviewContent))
	}
	loaded, err := LoadTrainInitConfig(paths.ConfigPath)
	if err != nil {
		t.Fatalf("LoadTrainInitConfig: %v", err)
	}
	if !reflect.DeepEqual(loaded, scaffold.Config) {
		t.Fatalf("loaded config mismatch\nloaded: %#v\nconfig: %#v", loaded, scaffold.Config)
	}
}

func TestTrainInitRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", "x/../y", "/abs", "bad name", " run ", "x/y", `x\y`} {
		t.Run(name, func(t *testing.T) {
			if _, err := TrainInitScaffoldRoot(t.TempDir(), name); err == nil {
				t.Fatalf("expected unsafe name %q to be rejected", name)
			}
		})
	}
}

func TestRenderAndWriteTrainInitRejectPaddedNames(t *testing.T) {
	config := trainInitTestConfig()
	config.Name = " smithyx-one-shot-voice "
	if _, err := RenderTrainInitConfig(config); err == nil {
		t.Fatal("expected padded render name to be rejected")
	}
	if _, err := WriteTrainInitScaffold(t.TempDir(), TrainInitScaffold{
		Config:       config,
		TaskMarkdown: "Improve replies.",
	}); err == nil {
		t.Fatal("expected padded scaffold name to be rejected")
	}
}

func TestParseTrainInitConfigPreservesExplicitZeroRetryBudgets(t *testing.T) {
	content := `
name = "zero-budget"
template = "smithyx-x-posts"
template_version = "smithyx-x-posts@v3"
review_repo = "jerryfane/gitmoot-x-posts-smithyx"
task_kind = "writing"
artifact_kind = "text_reply"
preview = "text"
mode = "explore"

[optimizer]
noop_retry_budget = 0
gate_reject_retry_budget = 0
wrong_artifact_retry_budget = 0
target_artifact_retry_budget = 0
hard_failure_retry_budget = 0
`
	config, err := ParseTrainInitConfig([]byte(content))
	if err != nil {
		t.Fatalf("ParseTrainInitConfig: %v", err)
	}
	if trainInitIntValue(config.Optimizer.NoopRetryBudget) != 0 ||
		trainInitIntValue(config.Optimizer.GateRejectRetryBudget) != 0 ||
		trainInitIntValue(config.Optimizer.WrongArtifactRetryBudget) != 0 ||
		trainInitIntValue(config.Optimizer.TargetArtifactRetryBudget) != 0 ||
		trainInitIntValue(config.Optimizer.HardFailureRetryBudget) != 0 {
		t.Fatalf("explicit zero retry budgets not preserved: %+v", config.Optimizer)
	}
	if config.Optimizer.SkillUpdateMode != TrainInitSkillUpdateModeFullRewrite {
		t.Fatalf("default skill update mode not filled: %+v", config.Optimizer)
	}
}

func TestRenderTrainInitConfigPreservesExplicitZeroRetryBudgets(t *testing.T) {
	config := trainInitTestConfig()
	config.Optimizer.NoopRetryBudget = trainInitIntPtr(0)
	config.Optimizer.GateRejectRetryBudget = trainInitIntPtr(0)
	config.Optimizer.WrongArtifactRetryBudget = trainInitIntPtr(0)
	config.Optimizer.TargetArtifactRetryBudget = trainInitIntPtr(0)
	config.Optimizer.HardFailureRetryBudget = trainInitIntPtr(0)

	rendered, err := RenderTrainInitConfig(config)
	if err != nil {
		t.Fatalf("RenderTrainInitConfig: %v", err)
	}
	for _, want := range []string{
		"noop_retry_budget = 0",
		"gate_reject_retry_budget = 0",
		"wrong_artifact_retry_budget = 0",
		"target_artifact_retry_budget = 0",
		"hard_failure_retry_budget = 0",
	} {
		if !strings.Contains(string(rendered), want) {
			t.Fatalf("rendered config missing %q:\n%s", want, string(rendered))
		}
	}

	parsed, err := ParseTrainInitConfig(rendered)
	if err != nil {
		t.Fatalf("ParseTrainInitConfig: %v", err)
	}
	if !reflect.DeepEqual(parsed, config) {
		t.Fatalf("zero-budget round trip mismatch\nparsed: %#v\nconfig: %#v", parsed, config)
	}
}

func TestRenderTrainInitConfigDefaultsRetryBudgetsForPartialConfig(t *testing.T) {
	config := TrainInitConfig{
		Name:            "partial-defaults",
		Template:        "smithyx-x-posts",
		TemplateVersion: "smithyx-x-posts@v3",
		ReviewRepo:      "jerryfane/gitmoot-x-posts-smithyx",
		TaskKind:        "writing",
		ArtifactKind:    "text_reply",
		Preview:         "text",
		Mode:            "explore",
	}

	rendered, err := RenderTrainInitConfig(config)
	if err != nil {
		t.Fatalf("RenderTrainInitConfig: %v", err)
	}
	for _, want := range []string{
		"noop_retry_budget = 1",
		"gate_reject_retry_budget = 3",
		"wrong_artifact_retry_budget = 1",
		"target_artifact_retry_budget = 2",
		"hard_failure_retry_budget = 3",
	} {
		if !strings.Contains(string(rendered), want) {
			t.Fatalf("rendered config missing default %q:\n%s", want, string(rendered))
		}
	}
}

func TestParseTrainInitConfigAllowsInlineComments(t *testing.T) {
	content := `
name = "commented"
template = "smithyx#x-posts" # keep hash inside the string
template_version = "smithyx-x-posts@v3"
review_repo = "jerryfane/gitmoot-x-posts-smithyx"
task_kind = "writing"
artifact_kind = "text_reply"
preview = "text"
mode = "explore"
options = 4 # explore options

[optimizer]
noop_retry_budget = 0 # disabled intentionally
`
	config, err := ParseTrainInitConfig([]byte(content))
	if err != nil {
		t.Fatalf("ParseTrainInitConfig: %v", err)
	}
	if config.Template != "smithyx#x-posts" {
		t.Fatalf("template = %q", config.Template)
	}
	if config.Options != 4 {
		t.Fatalf("options = %d", config.Options)
	}
	if trainInitIntValue(config.Optimizer.NoopRetryBudget) != 0 {
		t.Fatalf("noop retry budget = %d", trainInitIntValue(config.Optimizer.NoopRetryBudget))
	}
}

func TestWriteTrainInitScaffoldRejectsSymlinkedOutput(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	base := filepath.Join(root, ".gitmoot", TrainInitScaffoldDirName)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "smithyx-one-shot-voice")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := WriteTrainInitScaffold(root, TrainInitScaffold{
		Config:       trainInitTestConfig(),
		TaskMarkdown: "Improve replies.",
	})
	if err == nil {
		t.Fatal("expected symlinked scaffold path to be rejected")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, TrainInitConfigFileName)); !os.IsNotExist(err) {
		t.Fatalf("config should not be written through symlink, stat err=%v", err)
	}
}

func TestWriteTrainInitScaffoldRejectsSymlinkedLeafFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-config.toml")
	scaffoldRoot := filepath.Join(root, ".gitmoot", TrainInitScaffoldDirName, "smithyx-one-shot-voice")
	if err := os.MkdirAll(scaffoldRoot, 0o700); err != nil {
		t.Fatalf("mkdir scaffold root: %v", err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(scaffoldRoot, TrainInitConfigFileName)); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := WriteTrainInitScaffold(root, TrainInitScaffold{
		Config:       trainInitTestConfig(),
		TaskMarkdown: "Improve replies.",
	})
	if err == nil {
		t.Fatal("expected symlinked scaffold file to be rejected")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside target: %v", err)
	}
	if string(content) != "outside" {
		t.Fatalf("outside target was overwritten: %q", string(content))
	}
}

func TestWriteTrainInitScaffoldRemovesStaleReviewItems(t *testing.T) {
	root := t.TempDir()
	scaffold := TrainInitScaffold{
		Config:          trainInitTestConfig(),
		TaskMarkdown:    "Improve replies.",
		ReviewItemsYAML: []byte("run_id: stale\nitems: []"),
	}
	paths, err := WriteTrainInitScaffold(root, scaffold)
	if err != nil {
		t.Fatalf("WriteTrainInitScaffold initial: %v", err)
	}
	if _, err := os.Stat(paths.ReviewItemsPath); err != nil {
		t.Fatalf("expected review items after initial write: %v", err)
	}

	scaffold.ReviewItemsYAML = nil
	if _, err := WriteTrainInitScaffold(root, scaffold); err != nil {
		t.Fatalf("WriteTrainInitScaffold rewrite: %v", err)
	}
	if _, err := os.Stat(paths.ReviewItemsPath); !os.IsNotExist(err) {
		t.Fatalf("stale review items should be removed, stat err=%v", err)
	}
}

func TestTrainInitConfigMissingRequiredFields(t *testing.T) {
	_, err := ParseTrainInitConfig([]byte(`name = "missing-fields"`))
	if err == nil {
		t.Fatal("expected missing required fields error")
	}
	for _, want := range []string{"template", "template_version", "review_repo", "task_kind", "artifact_kind", "preview", "mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing field %q", err.Error(), want)
		}
	}
}

func trainInitTestConfig() TrainInitConfig {
	config := DefaultTrainInitConfig()
	config.Name = "smithyx-one-shot-voice"
	config.Template = "smithyx-x-posts"
	config.TemplateVersion = "smithyx-x-posts@v3"
	config.ReviewRepo = "jerryfane/gitmoot-x-posts-smithyx"
	config.TaskKind = "writing"
	config.ArtifactKind = "text_reply"
	config.Preview = "text"
	return config
}
