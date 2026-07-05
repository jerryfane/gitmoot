package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/runtime"
)

func writeCLIConfig(t *testing.T, body string) config.Paths {
	t.Helper()
	dir := t.TempDir()
	paths := config.Paths{ConfigFile: filepath.Join(dir, "config.toml")}
	if err := os.WriteFile(paths.ConfigFile, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return paths
}

// TestResolveRuntimeRegistryDefaultByteIdentical proves that with no [runtimes.*]
// section the resolved registry equals the built-in one.
func TestResolveRuntimeRegistryDefaultByteIdentical(t *testing.T) {
	paths := writeCLIConfig(t, "[paths]\ndatabase = \"x\"\n")
	got, err := resolveRuntimeRegistry(paths)
	if err != nil {
		t.Fatalf("resolveRuntimeRegistry error: %v", err)
	}
	if !reflect.DeepEqual(got.All(), runtime.BuiltinRuntimeRegistry().All()) {
		t.Fatalf("resolved registry differs from built-in with no overrides")
	}
}

// TestResolveRuntimeRegistryMissingFile proves a missing config file resolves to
// the built-in registry rather than erroring.
func TestResolveRuntimeRegistryMissingFile(t *testing.T) {
	paths := config.Paths{ConfigFile: filepath.Join(t.TempDir(), "nope.toml")}
	got, err := resolveRuntimeRegistry(paths)
	if err != nil {
		t.Fatalf("resolveRuntimeRegistry error: %v", err)
	}
	if !reflect.DeepEqual(got.All(), runtime.BuiltinRuntimeRegistry().All()) {
		t.Fatalf("missing file should resolve to built-in registry")
	}
}

// TestResolveRuntimeRegistryOverride proves config overrides are applied on top of
// the built-ins.
func TestResolveRuntimeRegistryOverride(t *testing.T) {
	paths := writeCLIConfig(t, `
[runtimes.codex]
default_model = "gpt-5.5-codex"
models = ["gpt-5.5-codex"]
`)
	got, err := resolveRuntimeRegistry(paths)
	if err != nil {
		t.Fatalf("resolveRuntimeRegistry error: %v", err)
	}
	codex, ok := got.Metadata(runtime.CodexRuntime)
	if !ok {
		t.Fatal("codex missing")
	}
	if codex.DefaultModel != "gpt-5.5-codex" {
		t.Fatalf("DefaultModel = %q", codex.DefaultModel)
	}
	if !reflect.DeepEqual(codex.Models, []string{"gpt-5.5-codex"}) {
		t.Fatalf("Models = %v", codex.Models)
	}
}

// TestResolveRuntimeRegistryUnknownErrors proves an unknown runtime name in config
// surfaces the moat-preserving error.
func TestResolveRuntimeRegistryUnknownErrors(t *testing.T) {
	paths := writeCLIConfig(t, "[runtimes.gpt6]\ndefault_model = \"x\"\n")
	if _, err := resolveRuntimeRegistry(paths); err == nil {
		t.Fatal("expected error for unknown runtime")
	}
}

// TestRunRuntimeListText covers the human table output on the default registry.
func TestRunRuntimeListText(t *testing.T) {
	paths := writeCLIConfig(t, "[paths]\ndatabase = \"x\"\n")
	var stdout, stderr bytes.Buffer
	code := runRuntimeList([]string{"--home", homeFromConfig(t, paths)}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, name := range runtime.SupportedRuntimes() {
		if !strings.Contains(out, name) {
			t.Fatalf("table missing runtime %q:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "review,implement,ask") {
		t.Fatalf("table missing capabilities:\n%s", out)
	}
}

// TestRunRuntimeListJSON covers the JSON output shape.
func TestRunRuntimeListJSON(t *testing.T) {
	paths := writeCLIConfig(t, "[paths]\ndatabase = \"x\"\n")
	var stdout, stderr bytes.Buffer
	code := runRuntimeList([]string{"--home", homeFromConfig(t, paths), "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr.String())
	}
	var entries []runtimeListEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	if len(entries) != len(runtime.SupportedRuntimes()) {
		t.Fatalf("expected %d entries, got %d", len(runtime.SupportedRuntimes()), len(entries))
	}
	if entries[0].Name != runtime.CodexRuntime || !entries[0].Dispatchable {
		t.Fatalf("first entry wrong: %+v", entries[0])
	}
}

// TestRunRuntimeListUnknownRuntimeExits proves the command exits non-zero on a bad
// override.
func TestRunRuntimeListUnknownRuntimeExits(t *testing.T) {
	paths := writeCLIConfig(t, "[runtimes.gpt6]\ndefault_model = \"x\"\n")
	var stdout, stderr bytes.Buffer
	code := runRuntimeList([]string{"--home", homeFromConfig(t, paths)}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "gpt6") {
		t.Fatalf("stderr should mention the bad runtime: %s", stderr.String())
	}
}

// homeFromConfig writes the given config body into a home layout so a --home flag
// resolves ConfigFile to it. The CLI resolves --home via config.PathsForHome, so
// place the file at <home>/.gitmoot/config.toml.
func homeFromConfig(t *testing.T, src config.Paths) string {
	t.Helper()
	home := t.TempDir()
	paths := config.PathsForHome(home)
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := os.ReadFile(src.ConfigFile)
	if err != nil {
		t.Fatalf("read src: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return home
}
