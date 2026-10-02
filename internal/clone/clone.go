// Package clone owns the repository-clone transaction shared by the CLI and TUI.
//
// Callers provide the user-facing source and destination request; this package
// resolves the destination, runs Git, and optionally registers the destination
// parent as a project path. Neither caller has to repeat clone policy.
package clone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/destination"
	gitops "github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/runner"
)

var scpOperand = regexp.MustCompile(`^[^/@\s]+@[^:\s]+:.+$`)

// Request describes one clone operation. Prefix is used by the TUI's
// repository shorthand; a complete Git operand is always passed through.
type Request struct {
	Source      string
	Prefix      string
	Destination string
	AddParent   bool
}

// Options supplies the process seams and project-path snapshot for one clone.
type Options struct {
	Runner     runner.CommandRunner
	Paths      []string
	ConfigPath string
	Home       string
}

// Result is the resolved result of a successful clone.
type Result struct {
	Source      string
	Destination string
}

var (
	ErrSourceRequired      = errors.New("clone requires a source")
	ErrDestinationRequired = errors.New("destination is required")
)

// ValidateSource trims and validates the source shared by the CLI and the
// Projects form. It does not interpret the source; SourceOperand owns that
// conversion at execution time.
func ValidateSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", ErrSourceRequired
	}
	return source, nil
}

// Validate checks the explicit destination form of a clone request. The CLI
// derives its optional destination before calling this function; the TUI uses
// it directly so both surfaces share destination preflight and normalization.
func Validate(request Request, paths []string, home string) (Request, destination.Result, error) {
	source, err := ValidateSource(request.Source)
	if err != nil {
		return Request{}, destination.Result{}, err
	}
	destinationPath := strings.TrimSpace(request.Destination)
	if destinationPath == "" {
		return Request{}, destination.Result{}, ErrDestinationRequired
	}
	resolved, err := destination.New(destination.Options{Paths: paths, Home: home}).Resolve(destinationPath)
	if err != nil {
		return Request{}, destination.Result{}, err
	}
	request.Source = source
	request.Prefix = strings.TrimSpace(request.Prefix)
	request.Destination = resolved.Path
	return request, resolved, nil
}

// Clone resolves and executes one repository clone. The destination parent is
// added only after Git succeeds, so a failed clone never changes config.
func Clone(ctx context.Context, options Options, request Request) (Result, error) {
	if options.Runner == nil {
		return Result{}, errors.New("clone requires a command runner")
	}

	home := options.Home
	if home == "" {
		home = os.Getenv("HOME")
	}
	source, err := ValidateSource(request.Source)
	if err != nil {
		return Result{}, err
	}

	destinationPath := strings.TrimSpace(request.Destination)
	if destinationPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Result{}, fmt.Errorf("get working directory: %w", err)
		}
		destinationPath, err = destination.DefaultPath(source, cwd, home)
		if err != nil {
			return Result{}, err
		}
	}
	request.Source = source
	request.Destination = destinationPath
	request, resolved, err := Validate(request, options.Paths, home)
	if err != nil {
		return Result{}, err
	}

	operand := SourceOperand(request.Source, request.Prefix, home)
	if err := gitops.Clone(ctx, options.Runner, operand, resolved.Path); err != nil {
		if _, statErr := os.Lstat(resolved.Path); statErr == nil {
			return Result{}, fmt.Errorf("%w (partial destination %q remains; remove it before retrying)", err, resolved.Path)
		}
		return Result{}, err
	}
	if request.AddParent && resolved.AddParent {
		if _, _, err := config.AddProjectParent(options.ConfigPath, resolved.Path, home); err != nil {
			return Result{}, fmt.Errorf("clone succeeded but add parent: %w", err)
		}
	}

	return Result{Source: request.Source, Destination: resolved.Path}, nil
}

// SourceOperand converts the TUI's repository shorthand into a complete Git
// operand. Complete URLs, SCP operands, local paths, and bare .git operands
// are passed through (with explicit home expansion); all other sources use
// prefix as their shorthand prefix.
func SourceOperand(source, prefix, home string) string {
	source = strings.TrimSpace(source)
	prefix = strings.TrimSpace(prefix)
	if completeOperand(source) {
		return destination.ExpandHome(source, home)
	}
	return prefix + source
}

func completeOperand(source string) bool {
	if strings.Contains(source, "://") || scpOperand.MatchString(source) {
		return true
	}
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~") || strings.HasPrefix(source, "$HOME/") {
		return true
	}
	// A bare .git path is a local operand, not repository shorthand.
	return strings.HasSuffix(source, ".git") && !strings.Contains(source, "/")
}
