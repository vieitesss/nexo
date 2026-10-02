package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/remote"
	"github.com/vieitesss/nexo/internal/startup"
)

type listCommand struct{}

func init() {
	registerBuiltin(func() Command { return listCommand{} })
}

func (listCommand) Name() string { return "list" }

func (listCommand) Run(ctx context.Context, invocation Invocation) error {
	trace := invocation.StartupTrace
	if trace == nil {
		trace = startup.New("list", invocation.Stderr)
	}
	trace.Mark("list-start")
	if invocation.Stdout == nil {
		return errors.New("list command requires stdout")
	}
	placesOnly, err := parseListArgs(invocation.Args)
	if err != nil {
		return err
	}
	if placesOnly {
		return listPlaces(ctx, invocation, trace)
	}
	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	trace.Mark("projects-discovered")
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			if _, err := fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning); err != nil {
				return err
			}
		}
	}
	if invocation.JSON {
		err := json.NewEncoder(invocation.Stdout).Encode(discovery.Candidates)
		trace.Mark("encoded")
		return err
	}
	trace.Mark("encoded")
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

func listPlaces(ctx context.Context, invocation Invocation, trace *startup.Trace) error {
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	trace.Mark("places-start")
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	lister, ok := invocation.Adapter.(capabilities.ContainerLister)
	if !ok {
		return fmt.Errorf("backend %q does not support listing containers", invocation.Selection.Kind)
	}
	trace.Mark("remote-start")
	remoteResultsCh := make(chan []remote.MachineResult, 1)
	go func() {
		remoteResultsCh <- newRemoteOrchestrator(invocation, nil).List(listCtx, invocation.Config.Remote.ConfiguredMachines())
		trace.Mark("remote-done")
	}()
	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	trace.Mark("projects-discovered")
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			if _, err := fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning); err != nil {
				return err
			}
		}
	}
	containers, err := lister.ListContainers(listCtx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	trace.Mark("containers-listed")
	if reader, ok := invocation.Adapter.(capabilities.ScreenReader); ok {
		var screens sync.WaitGroup
		for index := range containers {
			if containers[index].ActivePaneID == "" {
				continue
			}
			screens.Add(1)
			go func(index int, paneID string) {
				defer screens.Done()
				preview, readErr := reader.ReadScreen(listCtx, paneID)
				if readErr == nil {
					containers[index].Preview = preview
				}
			}(index, containers[index].ActivePaneID)
		}
		screens.Wait()
		trace.Mark("screens-read")
	}
	catalog := picker.NewCatalog(picker.ProjectPlaces(discovery.Candidates), containers, nil)
	remoteResults := <-remoteResultsCh
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
		err := json.NewEncoder(invocation.Stdout).Encode(places)
		trace.Mark("encoded")
		return err
	}
	trace.Mark("encoded")
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
