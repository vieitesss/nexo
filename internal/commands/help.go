package commands

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Help describes the command-line surface of a command. Commands keep this
// metadata beside their parser so the help text cannot silently drift.
type Help struct {
	Summary     string
	Usage       string
	Description string
	Subcommands []SubcommandHelp
	Flags       []FlagHelp
	Examples    []string
	Aliases     []string
	Hidden      bool
}

type SubcommandHelp struct {
	Name        string
	Summary     string
	Usage       string
	Description string
	Subcommands []SubcommandHelp
	Flags       []FlagHelp
	Examples    []string
	Aliases     []string
	Hidden      bool
}

type FlagHelp struct {
	Short       string
	Long        string
	Value       string
	Description string
}

var globalFlags = []FlagHelp{
	{Long: "--backend", Value: "auto|tmux|herdr", Description: "Select the backend (default: auto-detect)."},
	{Short: "-c", Long: "--config-file", Value: "PATH", Description: "Read configuration from PATH."},
	{Long: "--json", Description: "Write structured output where supported."},
	{Short: "-h", Long: "--help", Description: "Show help for this command."},
	{Long: "--version", Description: "Print the nexo version."},
}

func commandHelp(command Command) (Help, bool) {
	if command == nil {
		return Help{}, false
	}
	return command.Help(), true
}

func sortedSubcommands(h Help) []SubcommandHelp {
	items := append([]SubcommandHelp(nil), h.Subcommands...)
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func sortedFlags(flags []FlagHelp) []FlagHelp {
	items := append([]FlagHelp(nil), flags...)
	sort.Slice(items, func(i, j int) bool { return flagLabel(items[i]) < flagLabel(items[j]) })
	return items
}

func flagLabel(flag FlagHelp) string {
	label := flag.Short
	if label != "" && flag.Long != "" {
		label += ", "
	}
	return label + flag.Long
}

// subcommandHelp converts nested metadata into the command-shaped renderer
// input used by the recursive resolver.
func subcommandHelp(sub SubcommandHelp) Help {
	return Help{
		Summary:     sub.Summary,
		Usage:       sub.Usage,
		Description: sub.Description,
		Subcommands: sub.Subcommands,
		Flags:       sub.Flags,
		Examples:    sub.Examples,
		Aliases:     sub.Aliases,
		Hidden:      sub.Hidden,
	}
}

// resolveHelpPath resolves every component of a nested help path. When a
// suffix is unknown it returns the deepest known parent, which is also the
// relevant usage to print for the error.
func resolveHelpPath(registry *Registry, name string, args []string) (Command, Help, string, bool) {
	command, ok := registry.Lookup(name)
	if !ok {
		return nil, Help{}, "", false
	}
	h, ok := commandHelp(command)
	if !ok {
		return command, Help{}, name, false
	}
	path := name
	for _, arg := range args {
		var found *SubcommandHelp
		for index := range h.Subcommands {
			sub := &h.Subcommands[index]
			if arg == sub.Name || contains(sub.Aliases, arg) {
				found = sub
				break
			}
		}
		if found == nil {
			return command, h, path, false
		}
		path += " " + found.Name
		h = subcommandHelp(*found)
	}
	return command, h, path, true
}

func helpPath(registry *Registry, name string, args []string) (Command, Help, string, bool) {
	return resolveHelpPath(registry, name, args)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// PrintHelp renders either the top-level or one command's help. It detects
// stdout's terminal width and falls back to 80 columns for pipes and tests.
func PrintHelp(stdout io.Writer, registry *Registry, name string, args []string) {
	PrintHelpWithWidth(stdout, registry, name, args, terminalWidth(stdout))
}

// PrintHelpWithWidth is the deterministic rendering seam used by tests and
// callers that already know their terminal width.
func PrintHelpWithWidth(stdout io.Writer, registry *Registry, name string, args []string, width int) {
	if stdout == nil {
		return
	}
	if width < 1 {
		width = 80
	}
	if name == "" {
		printTopHelp(stdout, registry, width)
		return
	}
	_, h, path, ok := helpPath(registry, name, args)
	if !ok {
		printTopHelp(stdout, registry, width)
		return
	}
	printCommandHelp(stdout, path, h, width)
}

func terminalWidth(stdout io.Writer) int {
	file, ok := stdout.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(file.Fd()) {
		return 80
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width <= 0 {
		return 80
	}
	return width
}

type tableRow struct {
	label       string
	description string
}

func printTopHelp(out io.Writer, registry *Registry, width int) {
	writeWrapped(out, "Usage: nexo [flags] <command> [args]", width, "       ")
	_, _ = fmt.Fprintln(out, "\nCommands:")
	if registry != nil {
		rows := make([]tableRow, 0, len(registry.Names()))
		for _, name := range registry.Names() {
			command, _ := registry.Lookup(name)
			h, _ := commandHelp(command)
			if h.Hidden {
				continue
			}
			label := name
			if len(h.Aliases) > 0 {
				label += " (aliases: " + strings.Join(h.Aliases, ", ") + ")"
			}
			rows = append(rows, tableRow{label: "  " + label, description: h.Summary})
		}
		printTable(out, rows, width)
	}
	_, _ = fmt.Fprintln(out, "\nGlobal flags:")
	rows := make([]tableRow, 0, len(globalFlags))
	for _, flag := range sortedFlags(globalFlags) {
		rows = append(rows, tableRow{label: "  " + flagLabel(flag) + valueSuffix(flag), description: flag.Description})
	}
	printTable(out, rows, width)
	_, _ = fmt.Fprintln(out)
	writeWrapped(out, "Run 'nexo <command> --help' for more.", width, "")
}

func printCommandHelp(out io.Writer, path string, h Help, width int) {
	writeWrapped(out, "Usage: "+h.Usage, width, "       ")
	writeWrapped(out, h.Summary, width, "")
	if h.Description != "" {
		_, _ = fmt.Fprintln(out)
		writeWrapped(out, h.Description, width, "")
	}
	if subs := sortedSubcommands(h); len(subs) > 0 {
		_, _ = fmt.Fprintln(out, "\nSubcommands:")
		rows := make([]tableRow, 0, len(subs))
		for _, sub := range subs {
			if sub.Hidden {
				continue
			}
			label := "  " + sub.Name
			if len(sub.Aliases) > 0 {
				label += " (aliases: " + strings.Join(sub.Aliases, ", ") + ")"
			}
			rows = append(rows, tableRow{label: label, description: sub.Summary})
		}
		printTable(out, rows, width)
	}
	if flags := sortedFlags(h.Flags); len(flags) > 0 {
		_, _ = fmt.Fprintln(out, "\nFlags:")
		rows := make([]tableRow, 0, len(flags))
		for _, flag := range flags {
			rows = append(rows, tableRow{label: "  " + flagLabel(flag) + valueSuffix(flag), description: flag.Description})
		}
		printTable(out, rows, width)
	}
	if len(h.Examples) > 0 {
		_, _ = fmt.Fprintln(out, "\nExamples:")
		for _, example := range h.Examples {
			writeWrapped(out, "  "+example, width, "  ")
		}
	}
	if len(h.Subcommands) > 0 {
		_, _ = fmt.Fprintln(out)
		writeWrapped(out, fmt.Sprintf("Run 'nexo %s <subcommand> --help' for more.", path), width, "")
	}
}

func valueSuffix(flag FlagHelp) string {
	if flag.Value == "" {
		return ""
	}
	return " " + flag.Value
}

func printTable(out io.Writer, rows []tableRow, width int) {
	if len(rows) == 0 {
		return
	}
	const gap = 2
	maxLabel := 0
	for _, row := range rows {
		if len(row.label) > maxLabel {
			maxLabel = len(row.label)
		}
	}
	if maxLabel+gap >= width {
		for _, row := range rows {
			writeWrapped(out, row.label, width, "  ")
			if row.description != "" {
				writeWrapped(out, row.description, width, "  ")
			}
		}
		return
	}
	indent := strings.Repeat(" ", maxLabel+gap)
	for _, row := range rows {
		if row.description == "" {
			_, _ = fmt.Fprintln(out, row.label)
			continue
		}
		_, _ = fmt.Fprintf(out, "%-*s%s%s\n", maxLabel, row.label, strings.Repeat(" ", gap), wrapText(row.description, width-maxLabel-gap, indent))
	}
}

func writeWrapped(out io.Writer, text string, width int, continuation string) {
	if text == "" {
		return
	}
	for _, paragraph := range strings.Split(text, "\n") {
		leading := paragraph[:len(paragraph)-len(strings.TrimLeft(paragraph, " "))]
		wrapped := wrapText(strings.TrimLeft(paragraph, " "), width-len(leading), continuation)
		if wrapped == "" {
			_, _ = fmt.Fprintln(out)
			continue
		}
		_, _ = fmt.Fprintln(out, leading+wrapped)
	}
}

func wrapText(text string, width int, indent string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	if width < 1 {
		width = 1
	}
	var lines []string
	line := ""
	for _, word := range words {
		for len(word) > width {
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, word[:width])
			word = word[width:]
		}
		if word == "" {
			continue
		}
		if line == "" {
			line = word
		} else if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
		} else {
			line += " " + word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"+indent)
}

// HelpPathExists reports whether name and all of its nested subcommands are
// documented.
func HelpPathExists(registry *Registry, name string, args []string) bool {
	_, _, _, ok := helpPath(registry, name, args)
	return ok
}

// HelpUsage returns the canonical usage line for an invocation path. For an
// unknown suffix it returns the deepest known parent's usage.
func HelpUsage(registry *Registry, name string, args []string) string {
	_, h, _, ok := helpPath(registry, name, args)
	if h.Usage == "" || (!ok && h.Summary == "") {
		return "Usage: nexo [flags] <command> [args]"
	}
	return ensureUsagePrefix(h.Usage)
}

// HelpCommandPath returns the command path used by the --help hint. For an
// unknown suffix it returns the deepest known parent's path.
func HelpCommandPath(registry *Registry, name string, args []string) string {
	_, _, path, ok := helpPath(registry, name, args)
	if path == "" || (!ok && path == "") {
		return "nexo"
	}
	return "nexo " + path
}

func ensureUsagePrefix(usage string) string {
	if strings.HasPrefix(usage, "Usage: ") {
		return usage
	}
	return "Usage: " + usage
}

func validateCommandHelp(name string, h Help) error {
	if strings.TrimSpace(h.Summary) == "" {
		return fmt.Errorf("command %q has no help summary", name)
	}
	if strings.TrimSpace(h.Usage) == "" {
		return fmt.Errorf("command %q has no help usage", name)
	}
	if err := validateFlags(name, h.Flags); err != nil {
		return err
	}
	if err := validateSubcommands(name, h.Subcommands); err != nil {
		return err
	}
	return nil
}

func validateFlags(path string, flags []FlagHelp) error {
	seen := make(map[string]struct{}, len(flags))
	for _, flag := range flags {
		label := flagLabel(flag)
		if label == "" || strings.TrimSpace(flag.Description) == "" {
			return fmt.Errorf("command %q has incomplete help flag %q", path, label)
		}
		if _, ok := seen[label]; ok {
			return fmt.Errorf("command %q repeats help flag %q", path, label)
		}
		seen[label] = struct{}{}
	}
	return nil
}

func validateSubcommands(path string, subs []SubcommandHelp) error {
	seen := make(map[string]struct{}, len(subs))
	for _, sub := range subs {
		if strings.TrimSpace(sub.Name) == "" || strings.TrimSpace(sub.Summary) == "" || strings.TrimSpace(sub.Usage) == "" {
			return fmt.Errorf("command %q has an incomplete help subcommand", path)
		}
		if _, ok := seen[sub.Name]; ok {
			return fmt.Errorf("command %q repeats help subcommand %q", path, sub.Name)
		}
		seen[sub.Name] = struct{}{}
		if err := validateFlags(path+" "+sub.Name, sub.Flags); err != nil {
			return err
		}
		if err := validateSubcommands(path+" "+sub.Name, sub.Subcommands); err != nil {
			return err
		}
	}
	return nil
}

func validateHelp(registry *Registry) error {
	for _, name := range registry.Names() {
		command, _ := registry.Lookup(name)
		h, ok := commandHelp(command)
		if !ok {
			return fmt.Errorf("command %q has no help metadata", name)
		}
		if err := validateCommandHelp(name, h); err != nil {
			return err
		}
	}
	return nil
}
