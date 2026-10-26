package commands

import (
	"strings"
	"testing"
)

func documentedFlag(t *testing.T, registry *Registry, path, flag string) {
	t.Helper()
	name := path
	args := []string(nil)
	for i, part := range splitPath(path) {
		if i == 0 {
			name = part
			continue
		}
		args = append(args, part)
	}
	_, help, _, ok := helpPath(registry, name, args)
	if !ok {
		t.Fatalf("help path %q is not documented", path)
	}
	for _, entry := range help.Flags {
		if entry.Short == flag || entry.Long == flag {
			return
		}
	}
	t.Fatalf("parser accepts %s but help for %s does not document it", flag, path)
}

func splitPath(path string) []string { return strings.Fields(path) }

func TestEveryParsedFlagIsDocumented(t *testing.T) {
	registry := NewRegistry()
	cases := []struct {
		path  string
		flag  string
		parse func() error
	}{
		{path: "clone", flag: "--add-parent", parse: func() error {
			_, err := parseCloneArgs([]string{"--add-parent", "source"})
			return err
		}},
		{path: "delete", flag: "--dry-run", parse: func() error {
			_, err := parseDeleteArgs([]string{"--dry-run", "path"})
			return err
		}},
		{path: "delete", flag: "--force", parse: func() error {
			_, err := parseDeleteArgs([]string{"--force", "path"})
			return err
		}},
		{path: "delete", flag: "--permanent", parse: func() error {
			_, err := parseDeleteArgs([]string{"--permanent", "path"})
			return err
		}},
		{path: "list", flag: "--places", parse: func() error {
			_, err := parseListArgs([]string{"--places"})
			return err
		}},
		{path: "open", flag: "--no-focus", parse: func() error {
			_, err := parseOpenArgs([]string{"--no-focus", "path"})
			return err
		}},
		{path: "open", flag: "--container-id", parse: func() error {
			_, err := parseOpenArgs([]string{"--container-id", "id", "path"})
			return err
		}},
		{path: "open", flag: "--workspace-id", parse: func() error {
			_, err := parseOpenArgs([]string{"--workspace-id", "id", "path"})
			return err
		}},
		{path: "worktree create", flag: "--add-parent", parse: func() error {
			_, err := parseWorktreeCreateArgs([]string{"create", "--add-parent", "repo", "branch", "dest"})
			return err
		}},
		{path: "worktree create", flag: "--new-branch", parse: func() error {
			_, err := parseWorktreeCreateArgs([]string{"create", "--new-branch", "repo", "branch", "dest"})
			return err
		}},
		{path: "worktree create", flag: "--no-focus", parse: func() error {
			_, err := parseWorktreeCreateArgs([]string{"create", "--no-focus", "repo", "branch", "dest"})
			return err
		}},
		{path: "worktree extract", flag: "--add-parent", parse: func() error {
			_, err := parseWorktreeExtractArgs([]string{"extract", "--add-parent", "repo", "dest"})
			return err
		}},
		{path: "worktree extract", flag: "--no-focus", parse: func() error {
			_, err := parseWorktreeExtractArgs([]string{"extract", "--no-focus", "repo", "dest"})
			return err
		}},
		{path: "worktree extract", flag: "--return-branch", parse: func() error {
			_, err := parseWorktreeExtractArgs([]string{"extract", "--return-branch", "main", "repo", "dest"})
			return err
		}},
		{path: "remote attach", flag: "--from-env", parse: func() error {
			_, err := parseRemoteAttachOptions([]string{"attach", "--from-env"})
			return err
		}},
	}
	for _, test := range cases {
		t.Run(test.path+"/"+test.flag, func(t *testing.T) {
			if err := test.parse(); err != nil {
				t.Fatalf("parser rejected documented flag: %v", err)
			}
			documentedFlag(t, registry, test.path, test.flag)
		})
	}
}

func TestUndocumentedParserFlagIsRejected(t *testing.T) {
	if _, err := parseOpenArgs([]string{"--undocumented", "path"}); err == nil {
		t.Fatal("parseOpenArgs accepted an undocumented flag")
	}
	if _, err := parseWorktreeCreateArgs([]string{"create", "--undocumented", "repo", "branch", "dest"}); err == nil {
		t.Fatal("parseWorktreeCreateArgs accepted an undocumented flag")
	}
}
