package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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
	places := picker.ProjectPlaces(discovery.Candidates)
	var previewCmd func() tea.Msg
	if lister, ok := invocation.Adapter.(capabilities.ContainerLister); ok {
		containers, err := lister.ListContainers(ctx)
		if err != nil {
			return fmt.Errorf("list containers: %w", err)
		}
		places = append(places, picker.ContainerPlaces(containers)...)
		places = append(places, picker.MachinePlaces(containers)...)
		if reader, ok := invocation.Adapter.(capabilities.ScreenReader); ok {
			previewCmd = previewCommand(ctx, reader, containers)
		}
	}
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
		return popup.ShowPopup(ctx, pickerCommand(string(invocation.Selection.Kind), invocation.ConfigPath))
	}

	theme := picker.ThemeFromTerminal(ctx, picker.ThemeQuery{Reader: os.Stdin, Writer: os.Stdout}, 100*time.Millisecond)
	model := picker.NewModel(places, theme)
	if previewCmd != nil {
		model = model.WithPreviewCommand(previewCmd)
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	result, err := program.Run()
	if err != nil {
		return fmt.Errorf("run place picker: %w", err)
	}
	final, ok := result.(picker.Model)
	if !ok {
		return errors.New("place picker returned an invalid model")
	}
	if final.Cancelled() {
		return nil
	}
	if target, name, ok := final.RenameTargetRequest(); ok {
		if !isLocalContainer(target.Machine) {
			return fmt.Errorf("container machine %q is not supported", target.Machine)
		}
		renamer, supported := invocation.Adapter.(capabilities.ContainerRenamer)
		if !supported {
			return fmt.Errorf("backend %q does not support renaming containers", invocation.Selection.Kind)
		}
		return renamer.RenameContainer(ctx, target.ID, name)
	}
	if targets, ok := final.KillTargetsRequest(); ok {
		ids := make([]string, 0, len(targets))
		for _, target := range targets {
			if !isLocalContainer(target.Machine) {
				return fmt.Errorf("container machine %q is not supported", target.Machine)
			}
			ids = append(ids, target.ID)
		}
		if killer, supported := invocation.Adapter.(capabilities.ContainerBatchKiller); supported {
			// The batch capability receives the whole selection exactly once;
			// the backend may issue one native close command per container.
			return killer.KillContainers(ctx, ids)
		}
		killer, supported := invocation.Adapter.(capabilities.ContainerKiller)
		if !supported {
			return fmt.Errorf("backend %q does not support killing containers", invocation.Selection.Kind)
		}
		for _, id := range ids {
			if err := killer.KillContainer(ctx, id); err != nil {
				return err
			}
		}
		return nil
	}
	place, ok := final.Selected()
	if !ok {
		return nil
	}
	if place.OpenAction != nil {
		return place.OpenAction(ctx, place)
	}
	if place.Kind != picker.ProjectKind && place.Kind != picker.ContainerKind {
		return fmt.Errorf("place kind %q is not openable yet", place.Kind)
	}
	if place.Kind == picker.ContainerKind {
		if !isLocalContainer(place.Machine) {
			return fmt.Errorf("container machine %q is not supported", place.Machine)
		}
		if containerOpener, supported := invocation.Adapter.(capabilities.ContainerOpener); supported {
			return containerOpener.OpenContainer(ctx, place.ContainerID)
		}
		// Keep the existing project-opener seam usable for adapters that model
		// an existing container as an OpenOptions selection.
		if opener, supported := invocation.Adapter.(capabilities.ProjectOpener); supported {
			return opener.OpenProject(ctx, "", capabilities.OpenOptions{ContainerID: place.ContainerID})
		}
		return fmt.Errorf("backend %q does not support opening containers", invocation.Selection.Kind)
	}
	opener, ok := invocation.Adapter.(capabilities.ProjectOpener)
	if !ok {
		return fmt.Errorf("backend %q does not support opening projects", invocation.Selection.Kind)
	}
	return opener.OpenProject(ctx, place.Path, capabilities.OpenOptions{})
}

func isLocalContainer(machine string) bool {
	return machine == "" || machine == "local" || machine == picker.LocalMachine()
}

func previewCommand(ctx context.Context, reader capabilities.ScreenReader, containers []capabilities.Container) func() tea.Msg {
	return func() tea.Msg {
		type job struct {
			ref  picker.ContainerRef
			pane string
		}
		jobs := make(chan job)
		previews := make(map[picker.ContainerRef]string)
		var mu sync.Mutex
		var wg sync.WaitGroup
		workers := 4
		if len(containers) < workers {
			workers = len(containers)
		}
		if workers == 0 {
			return nil
		}
		worker := func() {
			defer wg.Done()
			for item := range jobs {
				readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				preview, err := reader.ReadScreen(readCtx, item.pane)
				cancel()
				if err == nil {
					mu.Lock()
					previews[item.ref] = preview
					mu.Unlock()
				}
			}
		}
		wg.Add(workers)
		for index := 0; index < workers; index++ {
			go worker()
		}
		for _, container := range containers {
			if container.ActivePaneID != "" {
				machine := container.Machine
				if machine == "" {
					machine = picker.LocalMachine()
				}
				jobs <- job{ref: picker.ContainerRef{Machine: machine, ID: container.ID}, pane: container.ActivePaneID}
			}
		}
		close(jobs)
		wg.Wait()
		if len(previews) == 0 {
			return nil
		}
		return picker.PreviewBatch{Previews: previews}
	}
}

func pickerCommand(kind, configPath string) string {
	binary := shellQuote(os.Args[0])
	command := fmt.Sprintf("NEXO_PLACE_PICKER_INLINE=1 %s --backend=%s", binary, shellQuote(kind))
	if configPath != "" {
		command += " --config-file=" + shellQuote(configPath)
	}
	return command
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
