package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gitmoot/gitmoot/internal/config"
	"github.com/gitmoot/gitmoot/internal/db"
	"github.com/gitmoot/gitmoot/internal/pipeline"
	"github.com/gitmoot/gitmoot/internal/runtime"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

const pipelineEnvFileMode os.FileMode = 0o600

const (
	pipelineKeySourceOwn     = "own"
	pipelineKeySourceShared  = "shared"
	pipelineKeySourceDefault = "default"
)

type pipelineStageEnvAccess struct {
	File     string
	Keys     []string
	Defaults map[string]string
	Access   []workflow.PipelineKeyAccess
}

type pipelineEnvUnresolved struct {
	Stage    string
	Selector string
}

type pipelineEnvironmentResolution struct {
	Access     []workflow.PipelineKeyAccess
	Unresolved []pipelineEnvUnresolved
}

// resolvePipelineEnvironment is the single names-only projection used by add
// validation, run preflight, payload audit, and `pipeline show --json`.
func resolvePipelineEnvironment(ctx context.Context, store *db.Store, home string, spec pipeline.Spec) (pipelineEnvironmentResolution, error) {
	available := make(map[string]string, len(spec.Env))
	source := make(map[string]string, len(spec.Env))
	for name := range spec.Env {
		available[name] = ""
		source[name] = pipelineKeySourceDefault
	}
	if strings.TrimSpace(spec.EnvFile) != "" {
		own, err := loadValidatedPipelineEnvFile(ctx, store, home, spec.EnvFile)
		if err != nil {
			return pipelineEnvironmentResolution{}, err
		}
		for name := range own {
			available[name] = ""
			source[name] = pipelineKeySourceOwn
		}
	}

	var grants []db.KeychainGrant
	if strings.TrimSpace(spec.Name) != "" {
		var err error
		grants, err = store.ListKeychainGrantsForConsumer(ctx, db.KeychainConsumerPipeline, spec.Name)
		if err != nil {
			return pipelineEnvironmentResolution{}, err
		}
	}
	sharedCandidates := make(map[string]db.KeychainKey)
	for _, grant := range grants {
		if source[grant.KeyName] == pipelineKeySourceOwn || !pipelineSelectorUsed(spec, grant.KeyName) {
			continue
		}
		key, found, err := store.GetGrantedKey(ctx, db.KeychainConsumerPipeline, spec.Name, grant.KeyName)
		if err != nil {
			return pipelineEnvironmentResolution{}, err
		}
		if found && key.Mode == db.KeychainModeInjected {
			sharedCandidates[key.Name] = key
		}
	}
	if len(sharedCandidates) > 0 {
		_, shared, err := loadValidatedKeychainFile(ctx, store, home)
		if err != nil {
			return pipelineEnvironmentResolution{}, err
		}
		for name := range sharedCandidates {
			if strings.TrimSpace(shared[name]) == "" {
				continue
			}
			available[name] = ""
			source[name] = pipelineKeySourceShared
		}
	}

	resolution := pipelineEnvironmentResolution{}
	for _, stage := range spec.Stages {
		seen := make(map[string]struct{})
		for _, selector := range stage.EnvKeys {
			keys, err := pipeline.ResolveEnvKeys([]string{selector}, available)
			if err != nil {
				resolution.Unresolved = append(resolution.Unresolved, pipelineEnvUnresolved{Stage: stage.ID, Selector: selector})
				continue
			}
			for _, name := range keys {
				if _, ok := seen[name]; ok {
					continue
				}
				seen[name] = struct{}{}
				resolution.Access = append(resolution.Access, workflow.PipelineKeyAccess{
					Stage: stage.ID, Name: name, Source: source[name], Mode: db.KeychainModeInjected,
				})
			}
		}
	}
	return resolution, nil
}

func pipelineSelectorUsed(spec pipeline.Spec, name string) bool {
	for _, stage := range spec.Stages {
		for _, selector := range stage.EnvKeys {
			if selector == name {
				return true
			}
			if strings.ContainsAny(selector, "*?[") {
				matched, _ := path.Match(selector, name)
				if matched {
					return true
				}
			}
		}
	}
	return false
}

func pipelineEnvironmentResolutionError(spec pipeline.Spec, unresolved []pipelineEnvUnresolved) error {
	if len(unresolved) == 0 {
		return nil
	}
	item := unresolved[0]
	name := item.Selector
	if strings.ContainsAny(name, "*?[") {
		name = "<name>"
	}
	return fmt.Errorf("stage %q env_keys entry %q is unresolved; register a matching key and run gitmoot key grant %s --pipeline %s", item.Stage, item.Selector, name, spec.Name)
}

func resolvePipelineStageEnvAccess(ctx context.Context, store *db.Store, home string, spec pipeline.Spec, stage pipeline.Stage) (pipelineStageEnvAccess, error) {
	if len(stage.EnvKeys) == 0 {
		return pipelineStageEnvAccess{}, nil
	}
	resolutionSpec := spec
	foundStage := false
	for _, candidate := range resolutionSpec.Stages {
		if candidate.ID == stage.ID {
			foundStage = true
			break
		}
	}
	if !foundStage {
		resolutionSpec.Stages = append(append([]pipeline.Stage(nil), resolutionSpec.Stages...), stage)
	}
	resolution, err := resolvePipelineEnvironment(ctx, store, home, resolutionSpec)
	if err != nil {
		return pipelineStageEnvAccess{}, err
	}
	for _, unresolved := range resolution.Unresolved {
		if unresolved.Stage == stage.ID {
			return pipelineStageEnvAccess{}, pipelineEnvironmentResolutionError(spec, []pipelineEnvUnresolved{unresolved})
		}
	}
	access := pipelineStageEnvAccess{File: spec.EnvFile}
	for _, row := range resolution.Access {
		if row.Stage != stage.ID {
			continue
		}
		access.Access = append(access.Access, row)
		access.Keys = append(access.Keys, row.Name)
		if row.Source == pipelineKeySourceDefault {
			if access.Defaults == nil {
				access.Defaults = make(map[string]string)
			}
			access.Defaults[row.Name] = spec.Env[row.Name]
		}
	}
	return access, nil
}

func loadValidatedPipelineEnvFile(ctx context.Context, store *db.Store, home, declared string) (map[string]string, error) {
	return loadValidatedSecretEnvFile(ctx, store, home, "env_file", declared)
}

func loadValidatedKeychainFile(ctx context.Context, store *db.Store, home string) (string, map[string]string, error) {
	path, err := resolveKeychainPath(store, home)
	if err != nil {
		return "", nil, err
	}
	values, err := loadValidatedSecretEnvFile(ctx, store, home, "keychain", path)
	return path, values, err
}

func resolveKeychainPath(store *db.Store, home string) (string, error) {
	paths, err := configPathsForPipelineStore(store, home)
	if err != nil {
		return "", err
	}
	cfg, err := config.LoadCredentialsConfig(paths)
	if err != nil {
		return "", fmt.Errorf("load credentials config: %w", err)
	}
	if cfg.KeychainPath != "" {
		return cfg.KeychainPath, nil
	}
	baseHome := filepath.Dir(paths.Home)
	return filepath.Join(baseHome, ".config", "gitmoot", "keychain.env"), nil
}

func configPathsForPipelineStore(store *db.Store, home string) (config.Paths, error) {
	if strings.TrimSpace(home) != "" {
		return pathsFromFlag(home)
	}
	if store != nil {
		databasePath := strings.TrimSpace(store.DatabasePath())
		if databasePath != "" && databasePath != ":memory:" {
			gitmootHome := filepath.Dir(databasePath)
			return config.PathsForHome(filepath.Dir(gitmootHome)), nil
		}
	}
	return pathsFromFlag(home)
}

func loadValidatedSecretEnvFile(ctx context.Context, store *db.Store, home, label, declared string) (map[string]string, error) {
	declared = strings.TrimSpace(declared)
	if !filepath.IsAbs(declared) {
		return nil, fmt.Errorf("%s %q must be absolute", label, declared)
	}
	file, err := os.Open(declared)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s %s does not exist", label, declared)
		}
		return nil, fmt.Errorf("open %s %s: %w", label, declared, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s %s: %w", label, declared, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", label, declared)
	}
	if info.Mode().Perm() != pipelineEnvFileMode {
		return nil, fmt.Errorf("%s %s has mode %04o; want 0600", label, declared, info.Mode().Perm())
	}
	owner, err := pipelineEnvOwnerUID(info)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", label, declared, err)
	}
	if owner != pipelineEnvCurrentUID() {
		return nil, fmt.Errorf("%s %s is owned by uid %d; want operator uid %d", label, declared, owner, pipelineEnvCurrentUID())
	}
	if err := validateSecretEnvFileLocation(ctx, store, home, label, declared); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", label, declared, err)
	}
	values, err := pipeline.ParseEnv(declared, data)
	if err != nil {
		return nil, err
	}
	for name := range values {
		if pipeline.ReservedEnvName(name) {
			return nil, fmt.Errorf("%s %s key %q uses reserved GITMOOT_* namespace", label, declared, name)
		}
	}
	return values, nil
}

func validatePipelineEnvFileLocation(ctx context.Context, store *db.Store, home, declared string) error {
	return validateSecretEnvFileLocation(ctx, store, home, "env_file", declared)
}

func validateSecretEnvFileLocation(ctx context.Context, store *db.Store, home, label, declared string) error {
	if store == nil {
		return fmt.Errorf("%s validation requires a store", label)
	}
	resolved, err := resolveProduceSafetyPath(declared)
	if err != nil {
		return fmt.Errorf("resolve %s %s: %w", label, declared, err)
	}
	gitmootHome := ""
	if databasePath := strings.TrimSpace(store.DatabasePath()); databasePath != "" && databasePath != ":memory:" {
		gitmootHome = filepath.Dir(databasePath)
	} else {
		paths, err := pathsFromFlag(home)
		if err != nil {
			return err
		}
		gitmootHome = paths.Home
	}
	protected := []struct{ label, path string }{{"Gitmoot home", gitmootHome}}
	repos, err := store.ListRepos(ctx)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		label := "managed checkout " + repo.Owner + "/" + repo.Name
		for _, checkout := range []string{repo.CheckoutPath, repo.PrimaryCheckoutPath} {
			if strings.TrimSpace(checkout) != "" {
				protected = append(protected, struct{ label, path string }{label, checkout})
			}
		}
	}
	for _, item := range protected {
		protectedPath, err := resolveProduceSafetyPath(item.path)
		if err != nil {
			return fmt.Errorf("resolve %s %q: %w", item.label, item.path, err)
		}
		if pathWithin(resolved, protectedPath) {
			return fmt.Errorf("%s %s resolves inside %s %q", label, declared, item.label, protectedPath)
		}
	}
	return nil
}

func pathWithin(path, directory string) bool {
	rel, err := filepath.Rel(directory, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type pipelineEnvDeliveryAdapter struct {
	inner        workflow.DeliveryAdapter
	store        *db.Store
	home         string
	pipelineName string
	access       []workflow.PipelineKeyAccess
	file         string
	keys         []string
	env          map[string]string
}

func wrapPipelineEnvDeliveryAdapter(store *db.Store, home string, payload workflow.JobPayload, inner workflow.DeliveryAdapter) workflow.DeliveryAdapter {
	if inner == nil || (len(payload.PipelineKeyAccess) == 0 && len(payload.PipelineEnvKeys) == 0) {
		return inner
	}
	return pipelineEnvDeliveryAdapter{
		inner: inner, store: store, home: home, pipelineName: payload.PipelineName,
		access: append([]workflow.PipelineKeyAccess(nil), payload.PipelineKeyAccess...),
		file:   payload.PipelineEnvFile, keys: append([]string(nil), payload.PipelineEnvKeys...), env: payload.PipelineEnv,
	}
}

func (a pipelineEnvDeliveryAdapter) Deliver(ctx context.Context, agent runtime.Agent, job runtime.Job) (runtime.Result, error) {
	if len(a.access) == 0 {
		return a.deliverLegacy(ctx, agent, job)
	}
	var own, shared map[string]string
	entries := make([]string, 0, len(a.access))
	for _, access := range a.access {
		if access.Mode != db.KeychainModeInjected {
			return runtime.Result{}, fmt.Errorf("load pipeline stage environment: key %q has unsupported mode %q", access.Name, access.Mode)
		}
		var (
			value string
			ok    bool
		)
		switch access.Source {
		case pipelineKeySourceOwn:
			if own == nil {
				var err error
				own, err = loadValidatedPipelineEnvFile(ctx, a.store, a.home, a.file)
				if err != nil {
					return runtime.Result{}, fmt.Errorf("load pipeline stage environment: %w", err)
				}
			}
			value, ok = own[access.Name]
		case pipelineKeySourceShared:
			key, granted, err := a.store.GetGrantedKey(ctx, db.KeychainConsumerPipeline, a.pipelineName, access.Name)
			if err != nil {
				return runtime.Result{}, fmt.Errorf("load pipeline stage environment: re-check grant for key %q: %w", access.Name, err)
			}
			if !granted || key.Mode != db.KeychainModeInjected {
				return runtime.Result{}, fmt.Errorf("load pipeline stage environment: grant for key %q was revoked or changed", access.Name)
			}
			if shared == nil {
				_, shared, err = loadValidatedKeychainFile(ctx, a.store, a.home)
				if err != nil {
					return runtime.Result{}, fmt.Errorf("load pipeline stage environment: %w", err)
				}
			}
			value, ok = shared[access.Name]
			ok = ok && strings.TrimSpace(value) != ""
		case pipelineKeySourceDefault:
			value, ok = a.env[access.Name]
		default:
			return runtime.Result{}, fmt.Errorf("load pipeline stage environment: key %q has unknown source %q", access.Name, access.Source)
		}
		if !ok {
			return runtime.Result{}, fmt.Errorf("load pipeline stage environment: key %q is no longer available from source %s", access.Name, access.Source)
		}
		entries = append(entries, access.Name+"="+value)
	}
	job.ShellEnv = prependPipelineEnvironment(entries, job.ShellEnv)
	return a.inner.Deliver(ctx, agent, job)
}

func (a pipelineEnvDeliveryAdapter) deliverLegacy(ctx context.Context, agent runtime.Agent, job runtime.Job) (runtime.Result, error) {
	available := make(map[string]string, len(a.env))
	for name, value := range a.env {
		available[name] = value
	}
	if strings.TrimSpace(a.file) != "" {
		values, err := loadValidatedPipelineEnvFile(ctx, a.store, a.home, a.file)
		if err != nil {
			return runtime.Result{}, fmt.Errorf("load pipeline stage environment: %w", err)
		}
		for name, value := range values {
			available[name] = value
		}
	}
	entries := make([]string, 0, len(a.keys))
	for _, key := range a.keys {
		value, ok := available[key]
		if !ok {
			return runtime.Result{}, fmt.Errorf("load pipeline stage environment: key %q is no longer available", key)
		}
		entries = append(entries, key+"="+value)
	}
	job.ShellEnv = prependPipelineEnvironment(entries, job.ShellEnv)
	return a.inner.Deliver(ctx, agent, job)
}

// prependPipelineEnvironment puts selected values before the existing shell
// environment. exec's last-wins behavior therefore preserves Gitmoot's own
// GITMOOT_* metadata even for a defense-in-depth, validation-bypassing payload.
func prependPipelineEnvironment(selected, internal []string) []string {
	merged := make([]string, 0, len(selected)+len(internal))
	merged = append(merged, selected...)
	return append(merged, internal...)
}
