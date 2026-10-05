package setup

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines surround each changed run.
const diffContext = 2

// diffLimit bounds the line-comparison table. Larger files fall back to a
// whole-file replacement, which is still correct, only longer.
const diffLimit = 4_000_000

type diffOp struct {
	kind    byte // ' ', '-' or '+'
	text    string
	newLine int // 1-based line in the new file this op sits at
}

// FormatDiff renders only the lines a planned change touches, with a little
// context, so the user reviews what setup writes rather than the whole file.
func FormatDiff(change Change) string {
	if !change.Changed() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (%s)\n+++ %s\n", change.Path, change.Machine, change.Path)
	ops := lineDiff(diffLines(change.Before), diffLines(change.After))
	for _, hunk := range diffHunks(ops) {
		fmt.Fprintf(&b, "@@ line %d @@\n", max(1, hunk[0].newLine))
		for _, op := range hunk {
			b.WriteString(string(op.kind) + op.text + "\n")
		}
	}
	return b.String()
}

func diffLines(contents []byte) []string {
	text := strings.TrimSuffix(string(contents), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// lineDiff returns the edit script from before to after through their
// longest common subsequence of lines.
func lineDiff(before, after []string) []diffOp {
	n, m := len(before), len(after)
	if n*m > diffLimit {
		ops := make([]diffOp, 0, n+m)
		for _, line := range before {
			ops = append(ops, diffOp{kind: '-', text: line, newLine: 1})
		}
		for index, line := range after {
			ops = append(ops, diffOp{kind: '+', text: line, newLine: index + 1})
		}
		return ops
	}
	// common[i][j] is the LCS length of before[i:] and after[j:].
	common := make([][]int, n+1)
	for i := range common {
		common[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if before[i] == after[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	ops := make([]diffOp, 0, max(n, m))
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && before[i] == after[j]:
			ops = append(ops, diffOp{kind: ' ', text: before[i], newLine: j + 1})
			i++
			j++
		// On a tie, removals go first, as in a unified diff.
		case i < n && (j == m || common[i+1][j] >= common[i][j+1]):
			ops = append(ops, diffOp{kind: '-', text: before[i], newLine: j + 1})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', text: after[j], newLine: j + 1})
			j++
		}
	}
	return ops
}

// diffHunks keeps the changed ops plus diffContext unchanged lines around
// each run; runs whose context touches merge into one hunk.
func diffHunks(ops []diffOp) [][]diffOp {
	keep := make([]bool, len(ops))
	for index, op := range ops {
		if op.kind == ' ' {
			continue
		}
		for near := max(0, index-diffContext); near <= min(len(ops)-1, index+diffContext); near++ {
			keep[near] = true
		}
	}
	var hunks [][]diffOp
	var current []diffOp
	for index, op := range ops {
		if !keep[index] {
			if current != nil {
				hunks = append(hunks, current)
				current = nil
			}
			continue
		}
		current = append(current, op)
	}
	if current != nil {
		hunks = append(hunks, current)
	}
	return hunks
}
