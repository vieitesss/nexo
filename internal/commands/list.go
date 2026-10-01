package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/remote"
)

type listCommand struct{}

func init() {
	registerBuiltin(func() Command { return listCommand{} })
}

func (listCommand) Name() string { return "list" }

func (listCommand) Run(ctx context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("list command requires stdout")
	}
	placesOnly, err := parseListArgs(invocation.Args)
	if err != nil {
		return err
	}
	if placesOnly {
		return listPlaces(ctx, invocation)
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

func parseListArgs(args []string) (bool, error) {
	places := false
	for _, arg := range args {
		if arg == "--places" {
			if places {
				return false, errors.New("--places may be provided only once")
			}
			places = true
			continue
		}
		return false, fmt.Errorf("unknown list option %q", arg)
	}
	return places, nil
}

func listPlaces(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	lister, ok := invocation.Adapter.(capabilities.ContainerLister)
	if !ok {
		return fmt.Errorf("backend %q does not support listing containers", invocation.Selection.Kind)
	}
	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			if _, err := fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning); err != nil {
				return err
			}
		}
	}
	containers, err := lister.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	if reader, ok := invocation.Adapter.(capabilities.ScreenReader); ok {
		for index := range containers {
			if containers[index].ActivePaneID == "" {
				continue
			}
			preview, readErr := reader.ReadScreen(ctx, containers[index].ActivePaneID)
			if readErr == nil {
				containers[index].Preview = preview
			}
		}
	}
	catalog := picker.NewCatalog(picker.ProjectPlaces(discovery.Candidates), containers, nil)
	catalog.SetRemoteMachines(invocation.Config.Remote.ConfiguredMachines())
	remoteResults := remote.NewLister(invocation.Runner, invocation.Selection.Kind, invocation.Config.Remote.SSHConfig).WithBackends(invocation.Config.Remote.Backends).List(ctx, invocation.Config.Remote.ConfiguredMachines())
	for _, result := range remoteResults {
		if result.Error != "" {
			if invocation.Stderr != nil {
				if _, err := fmt.Fprintf(invocation.Stderr, "nexo: warning: %s: %s\n", result.Machine, result.Error); err != nil {
					return err
				}
			}
			continue
		}
		catalog.ApplyRemote(picker.RemoteBatch{Machine: result.Machine, Places: picker.ContainerPlaces(result.Containers)})
	}
	places := catalog.Snapshot().Places
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(places)
	}
	return writePlaces(invocation.Stdout, places)
}

func writePlaces(stdout io.Writer, places []picker.Place) error {
	groups := make([]picker.GroupKey, 0)
	byGroup := make(map[picker.GroupKey][]picker.Place)
	for _, place := range places {
		key := picker.PlaceGroupKey(place)
		if _, ok := byGroup[key]; !ok {
			groups = append(groups, key)
		}
		byGroup[key] = append(byGroup[key], place)
	}
	for _, group := range groups {
		if _, err := fmt.Fprintf(stdout, "%s:\n", group.Name); err != nil {
			return err
		}
		for _, place := range byGroup[group] {
			line := "  " + place.DisplayRemainder
			if place.Kind == picker.ContainerKind && place.Preview != "" {
				line += "  " + previewLine(place.Preview)
			}
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func previewLine(preview string) string {
	for _, line := range strings.Split(preview, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
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
