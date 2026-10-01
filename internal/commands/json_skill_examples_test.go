package commands

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/remote"
)

func TestJSONSkillExamplesDecodeIntoWireTypes(t *testing.T) {
	contents, err := os.ReadFile("../../skills/nexo/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := jsonFencedBlocks(string(contents))
	if len(blocks) != 10 {
		t.Fatalf("found %d JSON examples, want ten", len(blocks))
	}
	values := []any{
		&backend.Selection{},
		&backend.Selection{},
		&[]projects.Candidate{},
		&[]picker.Place{},
		&[]capabilities.Agent{},
		&[]remote.MachineResult{},
		&[]remote.MachineResult{},
		&openedContainerJSON{},
		&worktreeJSON{},
		&worktreeJSON{},
	}
	for index, block := range blocks {
		decoder := json.NewDecoder(strings.NewReader(block))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(values[index]); err != nil {
			t.Errorf("JSON example %d does not decode into its wire type: %v", index+1, err)
			continue
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			t.Errorf("JSON example %d has trailing data: %v", index+1, err)
		}
	}
}

func jsonFencedBlocks(contents string) []string {
	var blocks []string
	var current []string
	inJSON := false
	for _, line := range strings.Split(contents, "\n") {
		switch {
		case line == "```json" && !inJSON:
			inJSON = true
			current = nil
		case line == "```" && inJSON:
			blocks = append(blocks, strings.Join(current, "\n"))
			inJSON = false
		case inJSON:
			current = append(current, line)
		}
	}
	return blocks
}
