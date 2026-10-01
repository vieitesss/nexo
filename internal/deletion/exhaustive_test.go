package deletion

import "testing"

func TestPreflightHardBlockersCannotBeOverridden(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Observation)
		code string
	}{
		{"not candidate", func(o *Observation) { o.Candidate = false }, "not-candidate"},
		{"outside configured paths", func(o *Observation) { o.Configured = false }, "outside-configured-paths"},
		{"active container", func(o *Observation) { o.ActiveContainer = true }, "active-container"},
		{"active check failure", func(o *Observation) { o.ActiveCheckError = errSentinel }, "active-check-failed"},
		{"changed path", func(o *Observation) { o.PathChanged = true }, "path-changed"},
		{"remote failure", func(o *Observation) { o.Git.RemoteCheckError = "offline" }, "remote-check-failed"},
		{"Git refusal", func(o *Observation) { o.Git.GitRefusal = "dirty worktree" }, "git-refusal"},
		{"dependent worktree", func(o *Observation) { o.Git.DependentWorktrees = []string{"/repo/wt"} }, "dependent-worktrees"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := Observation{Path: "/repo", Candidate: true, Configured: true, Exists: true, Directory: true,
				Git: GitState{IsRepository: true, RemoteChecked: true}}
			test.edit(&observation)
			plan := Preflight(observation)
			if plan.CanProceed || !hasCode(plan.Blockers, test.code) {
				t.Fatalf("plan = %#v, want non-overridable %s blocker", plan, test.code)
			}
		})
	}
}

func TestPreflightWarningsAreIndependentAndForceDoesNotChangePlan(t *testing.T) {
	plan := Preflight(Observation{Path: "/repo", Candidate: true, Configured: true, Exists: true, Directory: true,
		NonEmpty: true, Git: GitState{IsRepository: true, RemoteChecked: true, Unpushed: true, Stashes: true, Untracked: true, Dirty: true}})
	if !plan.CanProceed || len(plan.Warnings) != 2 || !hasCode(plan.Warnings, "non-empty-directory") || !hasCode(plan.Warnings, "local-git-data") {
		t.Fatalf("plan = %#v, want two overridable warnings", plan)
	}
}

var errSentinel = errorValue("check failed")

type errorValue string

func (e errorValue) Error() string { return string(e) }
