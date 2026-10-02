package commands

import "testing"

func TestSetupHelpDocumentsParserFlags(t *testing.T) {
	help := (setupCommand{}).Help()
	want := []string{
		"--backend", "--check", "--dry-run", "--fix-locale", "--local-only",
		"--machine", "--no-notify", "--no-remote-config", "--notify", "--path",
		"--remote-config", "--yes",
	}
	for _, flag := range want {
		found := false
		for _, documented := range help.Flags {
			if documented.Short == flag || documented.Long == flag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("setup help does not document %s", flag)
		}
	}
}
