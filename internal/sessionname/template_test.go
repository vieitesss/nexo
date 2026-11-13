package sessionname

import (
	"strings"
	"testing"
)

func TestRenderOptionalTeam(t *testing.T) {
	for _, tc := range []struct {
		template    string
		withTeam    string
		withoutTeam string
	}{
		{"{org}/{team}/{repo}", "acme/platform/api", "acme/api"},
		{"{org}-{team}-{repo}", "acme-platform-api", "acme-api"},
		{"{org}_{team}_{repo}", "acme_platform_api", "acme_api"},
		{"{org}/{team}-{repo}", "acme/platform-api", "acme-api"},
		{"{team}/{repo}", "platform/api", "api"},
		{"task-{team}", "task-platform", "task"},
		{"{team}", "platform", ""},
		{"literal--{org}/{repo}", "literal--acme/api", "literal--acme/api"},
		{"{org}/{team}...{repo}", "acme/platform___api", "acme___api"},
		{"{org}/{team}__{repo}", "acme/platform__api", "acme__api"},
		{"{team}/-_{repo}", "platform/-_api", "api"},
		{"{team}/{team}-{repo}", "platform/platform-api", "api"},
	} {
		t.Run(tc.template, func(t *testing.T) {
			for _, variant := range []struct {
				name   string
				values Values
				want   string
			}{
				{"team", Values{Org: "acme", Team: "platform", Repo: "api"}, tc.withTeam},
				{"no team", Values{Org: "acme", Repo: "api"}, tc.withoutTeam},
			} {
				t.Run(variant.name, func(t *testing.T) {
					got, err := Render(tc.template, variant.values)
					if variant.want == "" {
						if err == nil || !strings.Contains(err.Error(), "empty") {
							t.Fatalf("Render() = %q, %v; want empty-name error", got, err)
						}
						return
					}
					if err != nil || got != variant.want {
						t.Fatalf("Render() = %q, %v; want %q", got, err, variant.want)
					}
				})
			}
		})
	}
}

func TestRenderMissingTeamPreservesSubstitutedSeparators(t *testing.T) {
	for _, tc := range []struct {
		org  string
		want string
	}{
		{"acme-", "acme-api"},
		{"acme_", "acme_api"},
	} {
		t.Run(tc.org, func(t *testing.T) {
			got, err := Render("{org}{team}/{repo}", Values{Org: tc.org, Repo: "api"})
			if err != nil || got != tc.want {
				t.Fatalf("Render() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestValidateTemplate(t *testing.T) {
	for _, template := range []string{"literal-only", "{repo}", "{org}{repo}{org}", "task-{team}"} {
		if err := ValidateTemplate(template); err != nil {
			t.Errorf("ValidateTemplate(%q) = %v; want nil", template, err)
		}
	}
	for _, template := range []string{"", " \t ", "{branch}", "{ORG}", "{repo", "repo}", "{{repo}}", "{repo}}", "{org{repo}"} {
		if err := ValidateTemplate(template); err == nil {
			t.Errorf("ValidateTemplate(%q) = nil; want error", template)
		}
	}
}

func TestRenderNormalizesAndValidates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		template string
		values   Values
		want     string
		wantErr  string
	}{
		{"normalizes punctuation", "{org}/{repo}", Values{Org: "acme", Repo: "my.project:dev"}, "acme/my_project_dev", ""},
		{"trims outside", "  {org}/{repo}  ", Values{Org: "acme", Repo: "api"}, "acme/api", ""},
		{"preserves Unicode", "{org}/{repo}", Values{Org: "日本語", Repo: "café"}, "日本語/café", ""},
		{"braces are data", "{org}/{repo}", Values{Org: "{repo}", Repo: "{team}"}, "{repo}/{team}", ""},
		{"unused values may be empty", "{repo}", Values{Repo: "api"}, "api", ""},
		{"missing org", "{org}-{repo}", Values{Repo: "api"}, "", "org"},
		{"missing repo", "{repo}", Values{}, "", "repo"},
		{"NUL", "{repo}", Values{Repo: "a\x00b"}, "", "control"},
		{"newline", "{repo}", Values{Repo: "a\nb"}, "", "control"},
		{"leading newline", "{repo}", Values{Repo: "\napi"}, "", "control"},
		{"trailing newline", "{repo}", Values{Repo: "api\n"}, "", "control"},
		{"tab", "{repo}", Values{Repo: "a\tb"}, "", "control"},
		{"leading tab", "{repo}", Values{Repo: "\tapi"}, "", "control"},
		{"trailing tab", "{repo}", Values{Repo: "api\t"}, "", "control"},
		{"DEL", "{repo}", Values{Repo: "a\x7fb"}, "", "control"},
		{"whitespace result", "{team}", Values{Team: "   "}, "", "empty"},
		{"invisible result", "{team}", Values{Team: "\u200b"}, "", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.template, tc.values)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Render() = %q, %v; want error containing %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Render() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
