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
)

type processInfo struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	CWD  string `json:"cwd"`
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
	var paths []string
	probe, err := b.blockMethod(ctx, sessionID, block.ID, "process")
	if err != nil {
		return nil, err
	}
	var live processResult
	if err := json.Unmarshal(probe, &live); err != nil {
		return nil, fmt.Errorf("decode rex block %q process: %w", block.ID, err)
	}
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

	probe, err = b.blockMethod(ctx, sessionID, block.ID, "pwd")
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
	return b.api(ctx, "block.method", blockMethodInput{
		SessionID: sessionID,
		BlockID:   blockID,
		Method:    method,
		Args:      map[string]any{},
	})
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
