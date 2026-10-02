package commands

import (
	"fmt"
	"io"
	"sort"
	"strings"
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
	Name    string
	Summary string
	Usage   string
	Flags   []FlagHelp
	Aliases []string
	Hidden  bool
}

type FlagHelp struct {
	Short       string
	Long        string
	Value       string
	Description string
}

// HelpProvider is implemented by commands that expose their help metadata.
type HelpProvider interface{ Help() Help }

var globalFlags = []FlagHelp{
	{Long: "--backend", Value: "auto|tmux|herdr", Description: "Select the backend (default: auto-detect)."},
	{Short: "-c", Long: "--config-file", Value: "PATH", Description: "Read configuration from PATH."},
	{Long: "--json", Description: "Write structured output where supported."},
	{Short: "-h", Long: "--help", Description: "Show help for this command."},
	{Long: "--version", Description: "Print the nexo version."},
}

func commandHelp(command Command) (Help, bool) {
	provider, ok := command.(HelpProvider)
	if !ok {
		return Help{}, false
	}
	h := provider.Help()
	if h.Summary == "" {
		return Help{}, false
	}
	if h.Usage == "" {
		h.Usage = "nexo " + command.Name()
	}
	return h, true
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

func helpPath(registry *Registry, name string, args []string) (Command, Help, string, bool) {
	command, ok := registry.Lookup(name)
	if !ok {
		return nil, Help{}, "", false
	}
	h, ok := commandHelp(command)
	if !ok {
		return command, Help{}, name, false
	}
	path := name
	if len(args) > 0 {
		for _, sub := range h.Subcommands {
			if args[0] == sub.Name || contains(sub.Aliases, args[0]) {
				path += " " + sub.Name
				if sub.Usage != "" {
					h.Usage = sub.Usage
				}
				h.Summary = sub.Summary
				h.Description = ""
				h.Examples = nil
				if len(sub.Flags) > 0 {
					h.Flags = sub.Flags
				} else {
					h.Flags = nil
				}
				h.Subcommands = nil
				break
			}
		}
	}
	return command, h, path, true
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// PrintHelp renders either the top-level or one command's help. The renderer
// deliberately uses an 80-column width when stdout is not a terminal.
func PrintHelp(stdout io.Writer, registry *Registry, name string, args []string) {
	if stdout == nil {
		return
	}
	if name == "" {
		printTopHelp(stdout, registry)
		return
	}
	command, h, path, ok := helpPath(registry, name, args)
	if !ok || command == nil {
		printTopHelp(stdout, registry)
		return
	}
	printCommandHelp(stdout, path, h)
}

func printTopHelp(out io.Writer, registry *Registry) {
	_, _ = fmt.Fprintln(out, "Usage: nexo [flags] <command> [args]")
	_, _ = fmt.Fprintln(out, "\nCommands:")
	if registry != nil {
		for _, name := range registry.Names() {
			command, _ := registry.Lookup(name)
			h, ok := commandHelp(command)
			if !ok || h.Hidden {
				continue
			}
			label := name
			if len(h.Aliases) > 0 {
				label += " (aliases: " + strings.Join(h.Aliases, ", ") + ")"
			}
			printTableRow(out, "  "+label, h.Summary)
		}
	}
	_, _ = fmt.Fprintln(out, "\nGlobal flags:")
	for _, flag := range sortedFlags(globalFlags) {
		printTableRow(out, "  "+flagLabel(flag)+valueSuffix(flag), flag.Description)
	}
	_, _ = fmt.Fprintln(out, "\nRun 'nexo <command> --help' for more.")
}

func printCommandHelp(out io.Writer, path string, h Help) {
	_, _ = fmt.Fprintf(out, "Usage: %s\n\n%s\n", h.Usage, h.Summary)
	if h.Description != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", h.Description)
	}
	if subs := sortedSubcommands(h); len(subs) > 0 {
		_, _ = fmt.Fprintln(out, "\nSubcommands:")
		for _, sub := range subs {
			if sub.Hidden {
				continue
			}
			label := "  " + sub.Name
			if len(sub.Aliases) > 0 {
				label += " (aliases: " + strings.Join(sub.Aliases, ", ") + ")"
			}
			printTableRow(out, label, sub.Summary)
		}
	}
	if flags := sortedFlags(h.Flags); len(flags) > 0 {
		_, _ = fmt.Fprintln(out, "\nFlags:")
		for _, flag := range flags {
			printTableRow(out, "  "+flagLabel(flag)+valueSuffix(flag), flag.Description)
		}
	}
	if len(h.Examples) > 0 {
		_, _ = fmt.Fprintln(out, "\nExamples:")
		for _, example := range h.Examples {
			_, _ = fmt.Fprintf(out, "  %s\n", example)
		}
	}
	_, _ = fmt.Fprintf(out, "\nRun 'nexo %s --help' for more.\n", path)
}

func valueSuffix(flag FlagHelp) string {
	if flag.Value == "" {
		return ""
	}
	return " " + flag.Value
}

func printTableRow(out io.Writer, label, description string) {
	const width = 80
	const gap = 2
	if description == "" {
		_, _ = fmt.Fprintln(out, label)
		return
	}
	maxLabel := 28
	if len(label) > maxLabel {
		maxLabel = len(label)
	}
	if maxLabel+gap >= width {
		_, _ = fmt.Fprintf(out, "%s\n", label)
		return
	}
	_, _ = fmt.Fprintf(out, "%-*s%s\n", maxLabel, label, wrapText(description, width-maxLabel-gap, strings.Repeat(" ", maxLabel+gap)))
}

func wrapText(text string, width int, indent string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
		} else {
			line += " " + word
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n"+indent)
}

// HelpUsage returns the canonical usage line for an invocation path.
func HelpUsage(registry *Registry, name string, args []string) string {
	_, h, _, ok := helpPath(registry, name, args)
	if !ok {
		return "Usage: nexo [flags] <command> [args]"
	}
	return h.Usage
}

// HelpCommandPath returns the command path used by the --help hint.
func HelpCommandPath(registry *Registry, name string, args []string) string {
	_, _, path, ok := helpPath(registry, name, args)
	if !ok {
		return "nexo"
	}
	return "nexo " + path
}

func validateHelp(registry *Registry) error {
	for _, name := range registry.Names() {
		command, _ := registry.Lookup(name)
		h, ok := commandHelp(command)
		if !ok {
			return fmt.Errorf("command %q has no help summary", name)
		}
		for _, sub := range h.Subcommands {
			if sub.Name == "" || sub.Summary == "" {
				return fmt.Errorf("command %q has an incomplete help subcommand", name)
			}
		}
	}
	return nil
}
