// Package worktree owns the user-level worktree transaction shared by the
// command line and Projects tab. Backend adapters retain native Git and
// container operations; this package owns project-path registration and
// cleanup when that registration or output fails.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/destination"
	"github.com/vieitesss/nexo/internal/lifecycle"
)

// Request describes one worktree operation.
type Request struct {
	Repo         string
	Branch       string
	Destination  string
	ReturnBranch string
	NewBranch    bool
	AddParent    bool
	NoFocus      bool
}

// Options supplies the backend capabilities and config location for one
// operation. A remover is optional until cleanup is needed, so adapters that
// can create but cannot remove still report their creation result normally.
type Options struct {
	Creator            capabilities.WorktreeCreator
	Extractor          capabilities.WorktreeExtractor
	Remover            capabilities.WorktreeRemover
	ExtractionRollback capabilities.WorktreeExtractionRollback
	ConfigPath         string
	Paths              []string
	Home               string
}

var ErrDestinationRequired = errors.New("destination is required")

// Validate checks and normalizes the destination shared by the CLI and the
// Projects extraction form. Missing parents remain allowed because the native
// backend owns creating the worktree's destination tree.
func Validate(request Request, paths []string, home string) (Request, error) {
	destinationPath := strings.TrimSpace(request.Destination)
	if destinationPath == "" {
		return Request{}, ErrDestinationRequired
	}
	resolved, err := destination.New(destination.Options{
		Paths:              paths,
		Home:               home,
		AllowMissingParent: true,
	}).Resolve(destinationPath)
	if err != nil {
		return Request{}, err
	}
	request.Repo = strings.TrimSpace(request.Repo)
	request.Destination = resolved.Path
	request.ReturnBranch = strings.TrimSpace(request.ReturnBranch)
	return request, nil
}

// Result is a successful worktree operation. Rollback removes resources
// created by this invocation and restores a project-path update, when one was
// requested. Callers that have already presented the result should roll it
// back when their output sink fails.
type Result struct {
	capabilities.Worktree
	rollback func(context.Context) error
}

// Rollback removes this operation's resources and restores any config change.
// Cleanup is bounded and independent of caller cancellation because it is part
// of the worktree transaction's data-safety boundary.
func (r Result) Rollback(ctx context.Context) error {
	if r.rollback == nil {
		return errors.New("backend cannot roll back a worktree")
	}
	return r.rollback(ctx)
}

// Create creates a worktree through the selected backend and optionally adds
// its destination parent to project discovery. The config update happens only
// after native creation succeeds; a config failure rolls the native result
// back before returning the error.
func Create(ctx context.Context, options Options, request Request) (Result, error) {
	if options.Creator == nil {
		return Result{}, errors.New("worktree creation requires a creator")
	}
	request, err := Validate(request, options.Paths, options.Home)
	if err != nil {
		return Result{}, err
	}
	worktree, err := options.Creator.CreateWorktree(ctx, capabilities.WorktreeCreateOptions{
		Repo:        request.Repo,
		Branch:      request.Branch,
		Destination: request.Destination,
		NewBranch:   request.NewBranch,
		NoFocus:     request.NoFocus,
	})
	if err != nil {
		return Result{}, err
	}

	restore, err := addParent(options, request.Destination, worktree.Path, request.AddParent)
	if err != nil {
		return Result{}, errors.Join(err, rollbackCreated(ctx, options.Remover, worktree))
	}
	return newResult(worktree, func(cleanupCtx context.Context, result capabilities.Worktree) error {
		if options.Remover == nil {
			return errors.New("backend cannot roll back a created worktree")
		}
		return options.Remover.RemoveWorktree(cleanupCtx, result)
	}, restore), nil
}

// Extract moves the currently checked-out branch into a new worktree through
// the selected backend and optionally adds its destination parent to project
// discovery. The extractor owns Git preflight and branch restoration; this
// package owns the surrounding project-path transaction.
func Extract(ctx context.Context, options Options, request Request) (Result, error) {
	if options.Extractor == nil {
		return Result{}, errors.New("worktree extraction requires an extractor")
	}
	request, err := Validate(request, options.Paths, options.Home)
	if err != nil {
		return Result{}, err
	}
	worktree, err := options.Extractor.ExtractWorktree(ctx, capabilities.WorktreeExtractOptions{
		Repo:         request.Repo,
		Destination:  request.Destination,
		ReturnBranch: request.ReturnBranch,
		NoFocus:      request.NoFocus,
	})
	if err != nil {
		return Result{}, err
	}

	restore, err := addParent(options, request.Destination, worktree.Path, request.AddParent)
	if err != nil {
		return Result{}, errors.Join(err, rollbackExtracted(ctx, options.ExtractionRollback, worktree))
	}
	return newResult(worktree, func(cleanupCtx context.Context, result capabilities.Worktree) error {
		if options.ExtractionRollback == nil {
			return errors.New("backend cannot roll back an extracted worktree")
		}
		return options.ExtractionRollback.RollbackExtractWorktree(cleanupCtx, result)
	}, restore), nil
}

func newResult(worktree capabilities.Worktree, rollbacker func(context.Context, capabilities.Worktree) error, restore func() error) Result {
	return Result{
		Worktree: worktree,
		rollback: func(ctx context.Context) error {
			cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
			defer cancel()
			var rollbackErr error
			rollbackErr = rollbacker(cleanupCtx, worktree)
			if restore != nil {
				if configErr := restore(); configErr != nil {
					rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore config after output failure: %w", configErr))
				}
			}
			return rollbackErr
		},
	}
}

func rollbackCreated(ctx context.Context, remover capabilities.WorktreeRemover, worktree capabilities.Worktree) error {
	if remover == nil {
		return errors.New("backend cannot roll back a created worktree")
	}
	cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
	defer cancel()
	return remover.RemoveWorktree(cleanupCtx, worktree)
}

func rollbackExtracted(ctx context.Context, rollbacker capabilities.WorktreeExtractionRollback, worktree capabilities.Worktree) error {
	if rollbacker == nil {
		return errors.New("backend cannot roll back an extracted worktree")
	}
	cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
	defer cancel()
	return rollbacker.RollbackExtractWorktree(cleanupCtx, worktree)
}

func addParent(options Options, requested, actual string, enabled bool) (func() error, error) {
	if !enabled {
		return nil, nil
	}
	path := actual
	if path == "" {
		path = requested
	}
	home := options.Home
	if home == "" {
		home = os.Getenv("HOME")
	}
	restore, changed, err := config.AddProjectParent(options.ConfigPath, path, home)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return restore, nil
}
