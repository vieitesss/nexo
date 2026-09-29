package tmux

import (
	"regexp"
	"strings"
)

// The braille spinner used by the harnesses is deliberately kept to the
// common spinner sequence rather than treating every braille character as a
// live marker. This avoids matching braille used as content in a transcript.
const spinnerGlyphs = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

var claudeWorkingLine = regexp.MustCompile(`^[^\s]+\s+[[:alnum:]_-]+(?:…|\.\.\.)\s*\(`)
var squareProgress = regexp.MustCompile(`[▣■□▪▫●○█▓▒░▰▱](?:\s*[▣■□▪▫●○█▓▒░▰▱]){1,}`)

// MatchesWorkingIndicator reports whether screen contains the live marker for
// harness. Configured patterns take precedence for harnesses that do not have
// a built-in marker.
func MatchesWorkingIndicator(harness, screen string, configured map[string]string) bool {
	harness = strings.ToLower(harness)
	if pattern := configured[harness]; pattern != "" {
		return containsUnquoted(screen, pattern)
	}

	switch harness {
	case "pi", "codex":
		return containsSpinner(screen)
	case "claude":
		for _, line := range strings.Split(screen, "\n") {
			if !lineHasUnquotedMatch(line, claudeWorkingLine.MatchString) {
				continue
			}
			return true
		}
	case "opencode":
		for _, line := range strings.Split(screen, "\n") {
			if lineHasUnquotedMatch(line, squareProgress.MatchString) {
				return true
			}
		}
	case "hermes":
		return containsUnquoted(screen, "msg=interrupt")
	}
	return false
}

// IsWorkingIndicator is an alias kept as a concise matcher for callers that
// only need to inspect one screen.
func IsWorkingIndicator(harness, screen string, configured map[string]string) bool {
	return MatchesWorkingIndicator(harness, screen, configured)
}

func containsSpinner(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if lineHasUnquotedRune(line, func(r rune) bool { return strings.ContainsRune(spinnerGlyphs, r) }) {
			return true
		}
	}
	return false
}

func containsUnquoted(screen, needle string) bool {
	if needle == "" {
		return false
	}
	for _, line := range strings.Split(screen, "\n") {
		if lineHasUnquotedSubstring(line, needle) {
			return true
		}
	}
	return false
}

func lineHasUnquotedMatch(line string, match func(string) bool) bool {
	for _, segment := range unquotedSegments(line) {
		if match(segment) {
			return true
		}
	}
	return false
}

func lineHasUnquotedRune(line string, match func(rune) bool) bool {
	for _, segment := range unquotedSegments(line) {
		for _, r := range segment {
			if match(r) {
				return true
			}
		}
	}
	return false
}

func lineHasUnquotedSubstring(line, needle string) bool {
	for _, segment := range unquotedSegments(line) {
		if strings.Contains(segment, needle) {
			return true
		}
	}
	return false
}

// unquotedSegments removes transcript fragments enclosed in quotes. A live
// status line is never quoted, while copied assistant text often is. Escaped
// quote characters remain part of the surrounding segment.
func unquotedSegments(line string) []string {
	var segments []string
	start := 0
	quote := rune(0)
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote == 0 {
			if r == '"' || r == '\'' {
				if start < i {
					segments = append(segments, line[start:i])
				}
				quote = r
				start = i + len(string(r))
			}
			continue
		}
		if r == quote {
			quote = 0
			start = i + len(string(r))
		}
	}
	if quote == 0 && start < len(line) {
		segments = append(segments, line[start:])
	}
	return segments
}
