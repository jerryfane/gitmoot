package skillopt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const previewBundleRendererVueVite = TrainPreviewRendererVueVite
const previewBundleBuildCommand = "npm run build"
const previewBundlePackageBuildScript = "vite build"
const previewBundleDistDir = "dist"
const previewBundleTrustedIndexHTML = `<div id="app"></div><script type="module" src="/src/main.js"></script>`
const previewBundleTrustedMainJS = `import { createApp } from 'vue'; import App from './App.vue'; createApp(App).mount('#app');`

type PreviewBundle struct {
	Renderer     string              `json:"renderer"`
	Files        []PreviewBundleFile `json:"files"`
	BuildCommand string              `json:"build_command"`
	DistDir      string              `json:"dist_dir"`
}

type PreviewBundleFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type PreviewBundleMetadata struct {
	Renderer     string   `json:"renderer"`
	FileCount    int      `json:"file_count"`
	Files        []string `json:"files"`
	BuildCommand string   `json:"build_command"`
	DistDir      string   `json:"dist_dir"`
}

func ParsePreviewBundle(content []byte) (PreviewBundle, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return PreviewBundle{}, errors.New("preview bundle JSON is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var bundle PreviewBundle
	if err := decoder.Decode(&bundle); err != nil {
		return PreviewBundle{}, fmt.Errorf("decode preview bundle JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return PreviewBundle{}, errors.New("preview bundle JSON must contain exactly one object")
	}
	return validatePreviewBundle(bundle)
}

func (b PreviewBundle) Metadata() PreviewBundleMetadata {
	files := make([]string, 0, len(b.Files))
	for _, file := range b.Files {
		files = append(files, file.Path)
	}
	return PreviewBundleMetadata{
		Renderer:     b.Renderer,
		FileCount:    len(b.Files),
		Files:        files,
		BuildCommand: b.BuildCommand,
		DistDir:      b.DistDir,
	}
}

func validatePreviewBundle(bundle PreviewBundle) (PreviewBundle, error) {
	bundle.Renderer = strings.TrimSpace(strings.ToLower(bundle.Renderer))
	if bundle.Renderer == "" {
		return PreviewBundle{}, errors.New("preview bundle renderer is required")
	}
	if bundle.Renderer != previewBundleRendererVueVite {
		return PreviewBundle{}, fmt.Errorf("preview bundle renderer %q is not supported", bundle.Renderer)
	}
	bundle.BuildCommand = strings.TrimSpace(bundle.BuildCommand)
	if bundle.BuildCommand == "" {
		return PreviewBundle{}, errors.New("preview bundle build_command is required")
	}
	if bundle.BuildCommand != previewBundleBuildCommand {
		return PreviewBundle{}, fmt.Errorf("preview bundle build_command must be %q", previewBundleBuildCommand)
	}
	distDir, err := normalizePreviewBundlePath("dist_dir", bundle.DistDir)
	if err != nil {
		return PreviewBundle{}, err
	}
	bundle.DistDir = distDir
	if bundle.DistDir != previewBundleDistDir {
		return PreviewBundle{}, fmt.Errorf("preview bundle dist_dir must be %q", previewBundleDistDir)
	}
	if previewBundlePathHasSegment(distDir, "node_modules") || previewBundlePathHasSegment(distDir, ".git") || previewBundlePathIsSensitive(distDir) {
		return PreviewBundle{}, fmt.Errorf("preview bundle dist_dir %q must not point at dependency caches, git metadata, or secret/config credential paths", distDir)
	}
	if len(bundle.Files) == 0 {
		return PreviewBundle{}, errors.New("preview bundle files are required")
	}
	seen := map[string]struct{}{}
	required := map[string]bool{
		"package.json": false,
		"index.html":   false,
		"src/main.js":  false,
		"src/App.vue":  false,
	}
	for index, file := range bundle.Files {
		normalizedPath, err := normalizePreviewBundlePath("file path", file.Path)
		if err != nil {
			return PreviewBundle{}, fmt.Errorf("preview bundle file %d: %w", index+1, err)
		}
		if previewBundlePathHasSegment(normalizedPath, "node_modules") || previewBundlePathHasSegment(normalizedPath, ".git") {
			return PreviewBundle{}, fmt.Errorf("preview bundle file path %q must not include dependency caches or git metadata", normalizedPath)
		}
		if previewBundlePathIsSensitive(normalizedPath) {
			return PreviewBundle{}, fmt.Errorf("preview bundle file path %q looks like a secret/config credential file", normalizedPath)
		}
		if normalizedPath == distDir || strings.HasPrefix(normalizedPath, distDir+"/") {
			return PreviewBundle{}, fmt.Errorf("preview bundle file path %q must not include built output", normalizedPath)
		}
		if strings.TrimSpace(file.Content) == "" {
			return PreviewBundle{}, fmt.Errorf("preview bundle file %q content is required", normalizedPath)
		}
		if normalizedPath == "package.json" {
			if err := validatePreviewBundlePackageJSON(file.Content); err != nil {
				return PreviewBundle{}, err
			}
		}
		if normalizedPath == "index.html" && strings.TrimSpace(file.Content) != previewBundleTrustedIndexHTML {
			return PreviewBundle{}, errors.New("preview bundle index.html must use the trusted Gitmoot Vue mount scaffold")
		}
		if normalizedPath == "src/main.js" && strings.TrimSpace(file.Content) != previewBundleTrustedMainJS {
			return PreviewBundle{}, errors.New("preview bundle src/main.js must use the trusted Gitmoot Vue bootstrap")
		}
		if normalizedPath == "src/App.vue" {
			if err := validatePreviewBundleAppVue(file.Content); err != nil {
				return PreviewBundle{}, err
			}
		}
		if _, ok := seen[normalizedPath]; ok {
			return PreviewBundle{}, fmt.Errorf("preview bundle file path %q is duplicated", normalizedPath)
		}
		if _, ok := required[normalizedPath]; !ok {
			return PreviewBundle{}, fmt.Errorf("preview bundle file path %q is not supported", normalizedPath)
		}
		seen[normalizedPath] = struct{}{}
		if _, ok := required[normalizedPath]; ok {
			required[normalizedPath] = true
		}
		bundle.Files[index].Path = normalizedPath
	}
	for file, ok := range required {
		if !ok {
			return PreviewBundle{}, fmt.Errorf("preview bundle missing required file %q", file)
		}
	}
	return bundle, nil
}

func normalizePreviewBundlePath(kind string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("preview bundle %s is required", kind)
	}
	if strings.Contains(value, "\\") {
		return "", fmt.Errorf("preview bundle %s %q must use slash-separated relative paths", kind, value)
	}
	if strings.Contains(value, ":") {
		return "", fmt.Errorf("preview bundle %s %q must not include drive-qualified or URL-like paths", kind, value)
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("preview bundle %s %q must be relative", kind, value)
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", fmt.Errorf("preview bundle %s %q must not traverse directories", kind, value)
	}
	return normalized, nil
}

func validatePreviewBundlePackageJSON(content string) error {
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(content), &pkg); err != nil {
		return fmt.Errorf("preview bundle package.json: %w", err)
	}
	if strings.TrimSpace(pkg.Scripts["build"]) != previewBundlePackageBuildScript {
		return fmt.Errorf("preview bundle package.json scripts.build must be %q", previewBundlePackageBuildScript)
	}
	for name := range pkg.Scripts {
		if strings.TrimSpace(name) != "build" {
			return errors.New("preview bundle package.json scripts may only include build")
		}
	}
	if len(pkg.Dependencies) > 0 || len(pkg.DevDependencies) > 0 {
		return errors.New("preview bundle package.json dependencies are supplied by Gitmoot and must not be included")
	}
	return nil
}

func validatePreviewBundleAppVue(content string) error {
	lower := strings.ToLower(content)
	for _, blocked := range []string{
		"<script", "</script", "import ", "require(", "import.meta", "@import", "url(",
		"<iframe", "<object", "<embed", "<link", " src=", " srcset=", " href=", " xlink:href=",
		"http://", "https://", "javascript:", "data:",
	} {
		if strings.Contains(lower, blocked) {
			return fmt.Errorf("preview bundle src/App.vue must not include executable or external-loading construct %q", blocked)
		}
	}
	if !strings.Contains(lower, "<template") || !strings.Contains(lower, "</template>") {
		return errors.New("preview bundle src/App.vue must include a template")
	}
	return nil
}

func previewBundlePathHasSegment(value string, segment string) bool {
	for _, part := range strings.Split(value, "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func previewBundlePathIsSensitive(value string) bool {
	for _, part := range strings.Split(value, "/") {
		name := strings.ToLower(path.Base(part))
		if name == ".env" || strings.HasPrefix(name, ".env.") || name == ".npmrc" || name == ".yarnrc" {
			return true
		}
		if strings.Contains(name, "secret") || strings.Contains(name, "credential") {
			return true
		}
	}
	return false
}
