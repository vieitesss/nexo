package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
)

const PlacePickerCommandName = "__place-picker"
const pickerInlineEnv = "NEXO_PLACE_PICKER_INLINE"

type placePickerCommand struct{}

func init() {
	registerBuiltin(func() Command { return placePickerCommand{} })
}

// The internal command is appended by cli when no user subcommand is present;
// it is intentionally omitted from help output.
func (placePickerCommand) Name() string { return PlacePickerCommandName }

func (placePickerCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}

	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			if _, err := fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning); err != nil {
				return err
			}
		}
	}

	// Herdr 0.9 exposes popup.close but no request that opens a popup. The
	// absence of Popup deliberately selects the inline Bubble Tea path there.
	if popup, ok := invocation.Adapter.(capabilities.Popup); ok && os.Getenv(pickerInlineEnv) != "1" {
		return popup.ShowPopup(ctx, pickerCommand(string(invocation.Selection.Kind)))
	}

	theme := picker.ThemeFromTerminal(ctx, picker.ThemeQuery{Reader: os.Stdin, Writer: os.Stdout}, 100*time.Millisecond)
	model := picker.NewModel(picker.ProjectPlaces(discovery.Candidates), theme)
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	result, err := program.Run()
	if err != nil {
		return fmt.Errorf("run place picker: %w", err)
	}
	final, ok := result.(picker.Model)
	if !ok {
		return errors.New("place picker returned an invalid model")
	}
	place, ok := final.Selected()
	if !ok {
		return nil
	}
	if place.Kind != picker.ProjectKind {
		return fmt.Errorf("place kind %q is not openable yet", place.Kind)
	}
	opener, ok := invocation.Adapter.(capabilities.ProjectOpener)
	if !ok {
		return fmt.Errorf("backend %q does not support opening projects", invocation.Selection.Kind)
	}
	return opener.OpenProject(ctx, place.Path, capabilities.OpenOptions{})
}

func pickerCommand(kind string) string {
	binary := shellQuote(os.Args[0])
	return fmt.Sprintf("NEXO_PLACE_PICKER_INLINE=1 %s --backend=%s", binary, shellQuote(kind))
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// PlacePickerArgs reports whether args contain only global flags and, when
// they do, appends the internal picker command for normal dispatch.
func PlacePickerArgs(args []string) ([]string, bool) { return pickerArgs(args) }

// pickerArgs reports whether args contain only global flags. It appends the
// internal picker command so the normal Invocation path remains unchanged.
func pickerArgs(args []string) ([]string, bool) {
	out := append([]string(nil), args...)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return nil, false
		}
		if strings.HasPrefix(arg, "--backend=") || strings.HasPrefix(arg, "--config-file=") {
			continue
		}
		if arg == "--help" || arg == "-h" || arg == "--version" {
			return nil, false
		}
		if arg == "--json" {
			continue
		}
		if arg == "--backend" || arg == "--config-file" || arg == "-c" {
			index++
			if index >= len(args) {
				return nil, false
			}
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return nil, false
		}
		return nil, false
	}
	out = append(out, PlacePickerCommandName)
	return out, true
}
