package projects

import "testing"

func TestTargetNameUsesParentAndBaseAndIsTmuxSafe(t *testing.T) {
	if got, want := TargetName("/work/acme/my.project:dev"), "acme_my_project_dev"; got != want {
		t.Fatalf("TargetName() = %q, want %q", got, want)
	}
}
