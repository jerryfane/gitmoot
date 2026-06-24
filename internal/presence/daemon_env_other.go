//go:build !linux

package presence

// readProcessEnviron has no portable implementation outside Linux. Daemon-env
// auth detection is best-effort and OS-gated (issue #427 non-goal: no /proc read
// on Windows/macOS), mirroring the OS gating in internal/presence. Non-Linux
// builds always report detected=false so callers fall back to the shell-local
// check.
func readProcessEnviron(_ int) (func(string) (string, bool), bool) {
	return nil, false
}
