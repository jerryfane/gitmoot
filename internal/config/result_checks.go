package config

import (
	"fmt"
	"os"
	"strings"
)

// ResultChecksMode is the resolved [workflow] result_checks policy (#526): the
// deterministic binary-checklist audit that runs after a job's gitmoot_result is
// parsed. It has three values:
//
//   - off   — the audit is disabled entirely. No checks run, no event is
//     recorded, no payload field is set, and no feed-forward row is written, so
//     behavior and wire output are BYTE-IDENTICAL to before this feature existed.
//   - warn  — checks run; any failures are recorded as a job event, surfaced in
//     the job detail, and stored for later SkillOpt consumption, but the job
//     still succeeds/blocks/fails on its own decision. This is the DEFAULT.
//   - block — checks run; a failure additionally maps the job onto the same
//     terminal contract-violation path a malformed result takes (the job fails).
//     Opt-in, for strict workflows.
type ResultChecksMode string

const (
	// ResultChecksOff disables the audit (byte-identical pre-#526 behavior).
	ResultChecksOff ResultChecksMode = "off"
	// ResultChecksWarn records failures without failing the job (the default).
	ResultChecksWarn ResultChecksMode = "warn"
	// ResultChecksBlock maps a failure onto the terminal contract-violation path.
	ResultChecksBlock ResultChecksMode = "block"
)

// DefaultResultChecksMode is the value used when the [workflow] section (or the
// result_checks key) is absent: warn-only, exactly as issue #526 asks. An
// operator restores the pre-feature behavior by explicitly setting
// result_checks = off.
const DefaultResultChecksMode = ResultChecksWarn

// LoadResultChecksMode resolves the [workflow] result_checks knob. An absent
// config file, an absent [workflow] section, or an absent result_checks key all
// yield the documented default (warn). A present-but-invalid value is rejected so
// a typo surfaces loudly rather than silently disabling the audit.
func LoadResultChecksMode(paths Paths) (ResultChecksMode, error) {
	content, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultResultChecksMode, nil
		}
		return "", err
	}
	mode := DefaultResultChecksMode
	current := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(stripConfigComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		if current != "workflow" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != "result_checks" {
			continue
		}
		parsed, err := ParseResultChecksMode(strings.TrimSpace(value))
		if err != nil {
			return "", err
		}
		mode = parsed
	}
	return mode, nil
}

// ParseResultChecksMode validates and normalizes a result_checks value. The empty
// string falls back to the default (warn) so an operator can clear the key; any
// other unrecognized value is an error.
func ParseResultChecksMode(value string) (ResultChecksMode, error) {
	switch ResultChecksMode(strings.ToLower(strings.TrimSpace(value))) {
	case "":
		return DefaultResultChecksMode, nil
	case ResultChecksOff:
		return ResultChecksOff, nil
	case ResultChecksWarn:
		return ResultChecksWarn, nil
	case ResultChecksBlock:
		return ResultChecksBlock, nil
	default:
		return "", fmt.Errorf("invalid [workflow] result_checks %q; expected one of off, warn, block", value)
	}
}
