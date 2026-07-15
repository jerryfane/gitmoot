package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jerryfane/gitmoot/internal/runtime"
)

// claudeCredentialsFile is the cached-credential file inside a Claude config
// directory. Claude Code prefers it over the CLAUDE_CODE_OAUTH_TOKEN env var, so
// a child that can see it authenticates from the cached credential and ignores
// the gateway's placeholder (#936). Excluding exactly this file — and nothing
// else — is what forces the child onto the placeholder while keeping its
// settings, skills, and memory.
const claudeCredentialsFile = ".credentials.json"

// buildClaudeGatewayConfigDir returns a Claude config directory, under the
// gitmoot home, that mirrors the operator's real Claude config EXCEPT for the
// cached credential. Every other entry (settings, skills, commands, agents,
// CLAUDE.md, …) is symlinked through, so a gateway child keeps them but has no
// credential to prefer over the injected placeholder.
//
// It is idempotent: existing correct symlinks are left in place, stale ones are
// repaired, and real files Claude wrote into the dir on a prior run are never
// clobbered. The one hard guarantee is that the result never contains a
// .credentials.json.
func buildClaudeGatewayConfigDir(gitmootHome string) (string, error) {
	dest := filepath.Join(gitmootHome, "runtime", "claude-gateway-config")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", fmt.Errorf("create claude gateway config dir: %w", err)
	}

	// Guarantee no credential lives here — a stale symlink to the real one, or a
	// file Claude wrote itself after a refresh attempt.
	if err := os.RemoveAll(filepath.Join(dest, claudeCredentialsFile)); err != nil {
		return "", fmt.Errorf("remove cached credential from claude gateway config dir: %w", err)
	}

	source := realClaudeConfigDir()
	if source == "" || source == dest {
		return dest, nil
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		if os.IsNotExist(err) {
			// No real config yet: an empty dir (no credential) is exactly right.
			return dest, nil
		}
		return "", fmt.Errorf("read claude config dir %q: %w", source, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == claudeCredentialsFile {
			continue // the one thing we deny
		}
		link := filepath.Join(dest, name)
		target := filepath.Join(source, name)
		switch info, err := os.Lstat(link); {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			// Repair a symlink that points somewhere stale; leave a correct one.
			if current, _ := os.Readlink(link); current != target {
				if err := os.Remove(link); err != nil {
					return "", fmt.Errorf("replace stale claude config link %q: %w", link, err)
				}
				if err := os.Symlink(target, link); err != nil {
					return "", fmt.Errorf("relink claude config entry %q: %w", name, err)
				}
			}
		case err == nil:
			// A real file/dir Claude wrote here — do not clobber its runtime state.
		case os.IsNotExist(err):
			if err := os.Symlink(target, link); err != nil && !os.IsExist(err) {
				return "", fmt.Errorf("mirror claude config entry %q: %w", name, err)
			}
		default:
			return "", fmt.Errorf("inspect claude config entry %q: %w", link, err)
		}
	}
	return dest, nil
}

// realClaudeConfigDir is where Claude Code looks by default: $CLAUDE_CONFIG_DIR
// if set, otherwise ~/.claude. Empty when the home cannot be resolved (the caller
// then mirrors nothing, which still yields a credential-free dir).
func realClaudeConfigDir() string {
	if value := strings.TrimSpace(os.Getenv(runtime.ClaudeConfigDirEnv)); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
