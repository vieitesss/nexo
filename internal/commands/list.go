package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/vieitesss/nexo/internal/projects"
)

type listCommand struct{}

func init() {
	registerBuiltin(func() Command { return listCommand{} })
}

func (listCommand) Name() string { return "list" }

func (listCommand) Run(_ context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("list command requires stdout")
	}
	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			if _, err := fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning); err != nil {
				return err
			}
		}
	}
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(discovery.Candidates)
	}
	return writeCandidates(invocation.Stdout, discovery.Candidates)
}

func writeCandidates(stdout io.Writer, candidates []projects.Candidate) error {
	groups := make([]string, 0)
	byGroup := make(map[string][]projects.Candidate)
	for _, candidate := range candidates {
		if _, ok := byGroup[candidate.Group]; !ok {
			groups = append(groups, candidate.Group)
		}
		byGroup[candidate.Group] = append(byGroup[candidate.Group], candidate)
	}
	for _, group := range groups {
		if _, err := fmt.Fprintf(stdout, "%s:\n", group); err != nil {
			return err
		}
		for _, candidate := range byGroup[group] {
			line := "  " + candidate.Path
			if candidate.Git {
				line += " [git]"
			}
			if candidate.LinkedWorktree {
				line += " [linked worktree]"
			}
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return err
			}
		}
	}
	return nil
}
