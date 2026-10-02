package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/deletion"
	"github.com/vieitesss/nexo/internal/destination"
)

type deleteCommand struct{}
type deleteArgs struct {
	dryRun, permanent, force bool
	path                     string
}

func init()                        { registerBuiltin(func() Command { return deleteCommand{} }) }
func (deleteCommand) Name() string { return "delete" }

func (deleteCommand) Run(ctx context.Context, invocation Invocation) error {
	parsed, err := parseDeleteArgs(invocation.Args)
	if err != nil {
		return UsageError{Err: err}
	}
	path := destination.ExpandHome(parsed.path, os.Getenv("HOME"))
	transaction := newDeleteTransaction(invocation)
	plan, err := transaction.Preflight(ctx, path, "")
	if err != nil {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if invocation.Stdout != nil {
		if err := writeDeletePlan(invocation.Stdout, plan, parsed.permanent, invocation.JSON); err != nil {
			return err
		}
	}
	if parsed.dryRun {
		if len(plan.Blockers) > 0 {
			return fmt.Errorf("delete blocked: %s", deletion.FormatFindings(plan.Blockers))
		}
		return nil
	}
	if err := transaction.Execute(ctx, plan, parsed.force, parsed.permanent); err != nil {
		return err
	}
	if invocation.Stdout != nil && !invocation.JSON {
		_, _ = fmt.Fprintf(invocation.Stdout, "deleted %s\n", plan.Path)
	}
	return nil
}

func newDeleteTransaction(invocation Invocation) deletion.Transaction {
	checker, _ := invocation.Adapter.(capabilities.ProjectUseChecker)
	return deletion.New(deletion.Options{
		Projects:      invocation.Config.ProjectsConfig,
		Home:          os.Getenv("HOME"),
		Runner:        invocation.Runner,
		ActiveChecker: checker,
	})
}

func parseDeleteArgs(args []string) (deleteArgs, error) {
	var parsed deleteArgs
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			if parsed.dryRun {
				return deleteArgs{}, errors.New("--dry-run may be provided only once")
			}
			parsed.dryRun = true
		case "--permanent":
			if parsed.permanent {
				return deleteArgs{}, errors.New("--permanent may be provided only once")
			}
			parsed.permanent = true
		case "--force":
			if parsed.force {
				return deleteArgs{}, errors.New("--force may be provided only once")
			}
			parsed.force = true
		case "":
			return deleteArgs{}, errors.New("delete path may not be empty")
		default:
			if strings.HasPrefix(arg, "-") {
				return deleteArgs{}, fmt.Errorf("unknown delete option %q", arg)
			}
			if parsed.path != "" {
				return deleteArgs{}, errors.New("delete accepts exactly one PATH")
			}
			parsed.path = arg
		}
	}
	if parsed.path == "" {
		return deleteArgs{}, errors.New("delete requires PATH")
	}
	return parsed, nil
}

func writeDeletePlan(out interface{ Write([]byte) (int, error) }, plan deletion.Plan, permanent, jsonOutput bool) error {
	plan.Action = plan.EffectiveAction(permanent)
	if jsonOutput {
		return json.NewEncoder(out).Encode(plan)
	}
	if _, err := fmt.Fprintf(out, "preflight %s (%s)\n", plan.Path, plan.Action); err != nil {
		return err
	}
	for _, finding := range plan.Warnings {
		if _, err := fmt.Fprintf(out, "warning: %s\n", finding.Message); err != nil {
			return err
		}
	}
	for _, finding := range plan.Blockers {
		if _, err := fmt.Fprintf(out, "blocker: %s\n", finding.Message); err != nil {
			return err
		}
	}
	return nil
}
