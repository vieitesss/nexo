package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/navigator"
	"github.com/vieitesss/nexo/internal/picker"
)

func runAgentPicker(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	source, ok := invocation.Adapter.(capabilities.AgentSource)
	if !ok {
		return errors.New("selected backend does not support agents")
	}
	cfg := invocation.Config.Agents.WithDefaults()
	theme := picker.ThemeFromTerminal(ctx, picker.ThemeQuery{Reader: os.Stdin, Writer: os.Stdout}, 100*time.Millisecond)
	model := navigator.NewModel(source, theme, time.Duration(cfg.PollInterval)*time.Second)
	model.SetContext(ctx)
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	result, err := program.Run()
	if err != nil {
		return fmt.Errorf("run agent navigator: %w", err)
	}
	final, ok := result.(navigator.Model)
	if !ok {
		return errors.New("agent navigator returned an invalid model")
	}
	if final.Cancelled() {
		return nil
	}
	agent, ok := final.Selected()
	if !ok {
		return nil
	}
	focuser, ok := invocation.Adapter.(capabilities.PaneFocuser)
	if !ok {
		return fmt.Errorf("backend %q does not support focusing panes", invocation.Selection.Kind)
	}
	return focuser.FocusPane(ctx, agent.PaneID)
}
