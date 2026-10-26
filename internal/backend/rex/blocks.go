package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

type processInfo struct {
	PID    int    `json:"pid"`
	Name   string `json:"name"`
	Script string `json:"script"`
	CWD    string `json:"cwd"`
}

// processResult is the block process method. A shell may expose a child, a
// program may only expose a foreground process, and ancestors can still own the
// directory a shell sits in, so every reported cwd counts.
type processResult struct {
	Child      *processInfo  `json:"child"`
	Foreground *processInfo  `json:"foreground"`
	Ancestors  []processInfo `json:"ancestors"`
}

type pwdResult struct {
	PWD   string       `json:"pwd"`
	Owner *processInfo `json:"owner"`
}

// statusOwner is the process that published a status record when the report
// arrived. Rex compares both fields because the system reuses process ids.
type statusOwner struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// statusRecord is one native program-status report (OSC 7501). A block can hold
// several records: the root report plus one per child id.
type statusRecord struct {
	State        string       `json:"state"`
	App          string       `json:"app"`
	EffectiveApp string       `json:"effective_app"`
	Owner        *statusOwner `json:"owner"`
}

type statusReport struct {
	Records []statusRecord `json:"records"`
}

type screenResult struct {
	Content string `json:"content"`
}

type blockMethodInput struct {
	SessionID string         `json:"session_id"`
	BlockID   string         `json:"block_id"`
	Method    string         `json:"method"`
	Args      map[string]any `json:"args"`
}

// terminalBlock reports whether a block owns a program nexo can inspect. An
// unknown flavor is inspected anyway so an unrecognised block cannot be
// mistaken for an idle one.
func terminalBlock(block block) bool {
	flavor := strings.TrimSpace(block.Flavor)
	return flavor == "" || strings.HasPrefix(flavor, terminalFlavor)
}

// localBlock reports whether a block runs on the server host. A block on
// another host keeps its own filesystem, so its paths prove nothing here.
func localBlock(block block, server identity) bool {
	host := strings.TrimSpace(block.HostID)
	return host == "" || host == strings.TrimSpace(server.HostID)
}

// blockPaths reports the local working directories a terminal block is using.
// A block with no readable directory produces an empty list: it is not
// evidence that the block is idle.
func (b *Backend) blockPaths(ctx context.Context, sessionID string, block block) ([]string, error) {
	live, err := b.blockProcess(ctx, sessionID, block.ID)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, process := range []*processInfo{live.Child, live.Foreground} {
		if process != nil && strings.TrimSpace(process.CWD) != "" {
			paths = append(paths, process.CWD)
		}
	}
	for _, process := range live.Ancestors {
		if strings.TrimSpace(process.CWD) != "" {
			paths = append(paths, process.CWD)
		}
	}

	probe, err := b.blockMethod(ctx, sessionID, block.ID, "pwd")
	if err != nil {
		return nil, err
	}
	var reported pwdResult
	if err := json.Unmarshal(probe, &reported); err != nil {
		return nil, fmt.Errorf("decode rex block %q pwd: %w", block.ID, err)
	}
	if path, ok := decodePwd(reported.PWD); ok {
		paths = append(paths, path)
	}
	return uniquePaths(paths), nil
}

func (b *Backend) blockMethod(ctx context.Context, sessionID, blockID, method string) ([]byte, error) {
	return b.blockCall(ctx, sessionID, blockID, method, map[string]any{})
}

func (b *Backend) blockCall(ctx context.Context, sessionID, blockID, method string, args map[string]any) ([]byte, error) {
	return b.api(ctx, "block.method", blockMethodInput{
		SessionID: sessionID,
		BlockID:   blockID,
		Method:    method,
		Args:      args,
	})
}

// blockProcess reports the programs one block owns, foreground and
// descendants alike.
func (b *Backend) blockProcess(ctx context.Context, sessionID, blockID string) (processResult, error) {
	output, err := b.blockMethod(ctx, sessionID, blockID, "process")
	if err != nil {
		return processResult{}, err
	}
	var live processResult
	if err := json.Unmarshal(output, &live); err != nil {
		return processResult{}, fmt.Errorf("decode rex block %q process: %w", blockID, err)
	}
	return live, nil
}

// programStatus reports the native state a block's programs publish for
// themselves. The records are generic: which of them names an agent is the
// adapter's policy, not a decoding concern.
func (b *Backend) programStatus(ctx context.Context, sessionID, blockID string) ([]statusRecord, error) {
	output, err := b.blockMethod(ctx, sessionID, blockID, "program_status")
	if err != nil {
		return nil, err
	}
	var report statusReport
	if err := json.Unmarshal(output, &report); err != nil {
		return nil, fmt.Errorf("decode rex block %q program_status: %w", blockID, err)
	}
	return report.Records, nil
}

// screen reads one block's visible text and scrollback as plain text.
func (b *Backend) screen(ctx context.Context, sessionID, blockID string) (string, error) {
	output, err := b.blockCall(ctx, sessionID, blockID, "format", map[string]any{"format": "text", "trim": true})
	if err != nil {
		return "", err
	}
	var decoded screenResult
	if err := json.Unmarshal(output, &decoded); err != nil {
		return "", fmt.Errorf("decode rex block %q format: %w", blockID, err)
	}
	return decoded.Content, nil
}

// BlockProcessPIDs reports the pids of one block's child and foreground
// programs. Detection compares their controlling terminal with the
// invocation's own, because the terminal API exposes no tty field.
func BlockProcessPIDs(ctx context.Context, commandRunner runner.CommandRunner, sessionID, blockID string) ([]int, error) {
	probe := New(commandRunner, nil)
	live, err := probe.blockProcess(ctx, sessionID, blockID)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, process := range []*processInfo{live.Child, live.Foreground} {
		if process != nil && process.PID > 0 {
			pids = append(pids, process.PID)
		}
	}
	if len(pids) == 0 {
		return nil, fmt.Errorf("rex block %q reports no running process", blockID)
	}
	return pids, nil
}

// decodePwd accepts a plain path or a percent-encoded file URL. A URL naming
// another machine is not a local checkout and is ignored.
func decodePwd(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if !strings.HasPrefix(raw, "file://") {
		return filepath.Clean(raw), true
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if host := parsed.Hostname(); host != "" && !localHostname(host) {
		return "", false
	}
	if parsed.Path == "" {
		return "", false
	}
	return filepath.Clean(parsed.Path), true
}

func localHostname(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	name, err := os.Hostname()
	if err != nil {
		return false
	}
	name = strings.ToLower(name)
	short, _, _ := strings.Cut(name, ".")
	return host == name || host == short
}

// uniquePaths drops duplicates and empties while keeping observation order.
func uniquePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		unique = append(unique, path)
	}
	return unique
}

// isSymlink reports whether the project root itself is a link, which decides
// how the shared path policy compares it with the resolved physical path.
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

var errUninspectableBlock = errors.New("rex block has no inspectable working directory")
