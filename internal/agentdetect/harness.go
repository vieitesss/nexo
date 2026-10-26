package agentdetect

import (
	"path/filepath"
	"strings"
)

// interpreters are runtimes whose own name never identifies an agent; the
// harness is the script they execute instead.
var interpreters = map[string]struct{}{
	"node":    {},
	"bun":     {},
	"deno":    {},
	"python":  {},
	"python3": {},
}

// Harness returns the allowed harness named by a process's command line. Only
// the process identity counts: the basename of argv[0], or, when argv[0] is an
// interpreter, the basename of the script it runs.
func Harness(commandLine string, allowed map[string]struct{}) string {
	fields := strings.Fields(commandLine)
	if len(fields) == 0 {
		return ""
	}
	return HarnessOf(commandBase(fields[0]), scriptBase(fields[1:]), allowed)
}

// HarnessOf returns the allowed harness named by an executable and, for an
// interpreter, the script it runs. Names are compared as lowercased
// basenames, so a full path and a bare program name are equivalent.
func HarnessOf(executable, script string, allowed map[string]struct{}) string {
	name := base(executable)
	if _, ok := interpreters[name]; ok {
		name = base(script)
	}
	if name == "" {
		return ""
	}
	if _, ok := allowed[name]; ok {
		return name
	}
	return ""
}

func commandBase(field string) string {
	return base(strings.Trim(field, "\"'(),[]{}"))
}

// scriptBase returns the basename of the first non-flag argument, the script
// passed to an interpreter.
func scriptBase(args []string) string {
	for _, field := range args {
		field = strings.Trim(field, "\"'(),[]{}")
		if field == "" || strings.HasPrefix(field, "-") {
			continue
		}
		return base(field)
	}
	return ""
}

// base normalizes one executable or script name.
func base(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(name))
}
