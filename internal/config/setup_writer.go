package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/vieitesss/nexo/internal/destination"
)

// SetupValues are the explicit values setup may add to the shared config. A
// nil Notify retains the existing value.
type SetupValues struct {
	Backend  string
	Notify   *bool
	Machines []string
	Backends map[string]string
	Paths    []string
}

// PrepareSetup edits a TOML document in memory. It is the planning half of
// setup: no temporary files, directories, or other filesystem writes occur.
// The parser supplies exact value ranges, so comments, formatting, and
// unrelated tables remain byte-for-byte unchanged.
func PrepareSetup(contents []byte, path, home string, values SetupValues) ([]byte, error) {
	if path == "" {
		path = "<setup config>"
	}
	if len(contents) > 0 {
		var cfg Config
		if err := decode(contents, path, &cfg); err != nil {
			return nil, err
		}
	}
	updated := append([]byte(nil), contents...)
	for _, projectPath := range values.Paths {
		projectPath = strings.TrimSpace(projectPath)
		if projectPath == "" {
			continue
		}
		covered := false
		if len(updated) > 0 {
			var cfg Config
			if err := decode(updated, path, &cfg); err != nil {
				return nil, err
			}
			covered = destination.Covered(cfg.Paths, projectPath, home)
		}
		if !covered {
			var err error
			updated, err = appendProjectPath(updated, projectPath)
			if err != nil {
				return nil, fmt.Errorf("update config %s: %w", path, err)
			}
		}
	}
	if strings.TrimSpace(values.Backend) != "" {
		var err error
		updated, err = upsertTOMLValue(updated, path, "", "backend", strconv.Quote(strings.TrimSpace(values.Backend)))
		if err != nil {
			return nil, err
		}
	}
	if values.Notify != nil {
		replacement := "false"
		if *values.Notify {
			replacement = "true"
		}
		var err error
		updated, err = upsertTOMLValue(updated, path, "agents", "notify", replacement)
		if err != nil {
			return nil, err
		}
	}
	for _, machine := range uniqueStrings(values.Machines) {
		var err error
		updated, err = appendTOMLArrayValue(updated, path, "remote", "machines", machine)
		if err != nil {
			return nil, err
		}
	}
	machines := make([]string, 0, len(values.Backends))
	for machine := range values.Backends {
		machines = append(machines, machine)
	}
	sort.Strings(machines)
	for _, machine := range machines {
		backend := strings.TrimSpace(values.Backends[machine])
		if strings.TrimSpace(machine) == "" || backend == "" {
			continue
		}
		var err error
		updated, err = upsertTOMLValue(updated, path, "remote.backends", machine, strconv.Quote(backend))
		if err != nil {
			return nil, err
		}
	}
	if len(updated) > 0 {
		var cfg Config
		if err := decode(updated, path, &cfg); err != nil {
			return nil, fmt.Errorf("validate setup config: %w", err)
		}
	}
	return updated, nil
}

// MergeAgents copies the explicitly present keys from a local [agents] table
// into an existing remote config. It never replaces the document or touches
// unrelated tables. The source and result are both validated as TOML.
func MergeAgents(remoteContents, localContents []byte, path string) ([]byte, error) {
	if path == "" {
		path = "<remote config>"
	}
	var remoteConfig, localConfig Config
	if len(remoteContents) > 0 {
		if err := decode(remoteContents, path, &remoteConfig); err != nil {
			return nil, err
		}
	}
	if len(localContents) == 0 {
		return append([]byte(nil), remoteContents...), nil
	}
	if err := decode(localContents, "<local config>", &localConfig); err != nil {
		return nil, err
	}
	spans, err := tableValues(localContents, "agents")
	if err != nil {
		return nil, err
	}
	updated := append([]byte(nil), remoteContents...)
	if len(spans) == 0 {
		return updated, nil
	}
	for _, span := range spans {
		var valueErr error
		updated, valueErr = upsertTOMLValue(updated, path, "agents", span.key, string(span.value))
		if valueErr != nil {
			return nil, valueErr
		}
	}
	if len(updated) == 0 {
		return nil, errors.New("cannot merge agents into empty remote config")
	}
	var result Config
	if err := decode(updated, path, &result); err != nil {
		return nil, fmt.Errorf("validate remote config: %w", err)
	}
	_ = remoteConfig
	_ = localConfig
	return updated, nil
}

// MergeSetup updates a config file using the pure planner and the shared
// atomic config writer. Callers that need dry-run must use PrepareSetup.
func MergeSetup(path string, values SetupValues) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("config path is empty")
	}
	contents, mode, existed, err := readConfigFile(path)
	if err != nil {
		return false, err
	}
	updated, err := PrepareSetup(contents, path, "", values)
	if err != nil {
		return false, err
	}
	if bytes.Equal(contents, updated) {
		return false, nil
	}
	if err := WritePrepared(path, updated, mode); err != nil {
		return false, err
	}
	_ = existed
	return true, nil
}

// WritePrepared applies already-planned bytes through the shared atomic
// writer used by project-path edits.
func WritePrepared(path string, contents []byte, mode os.FileMode) error {
	return writeConfigFile(path, contents, mode)
}

type valueSpan struct {
	key   string
	value []byte
}

type expression struct {
	table string
	key   string
	value unstable.Range
}

func parseExpressions(contents []byte) ([]expression, error) {
	parser := unstable.Parser{}
	parser.Reset(contents)
	table := ""
	var expressions []expression
	for parser.NextExpression() {
		expr := parser.Expression()
		switch expr.Kind {
		case unstable.Table, unstable.ArrayTable:
			table, _ = keyInfo(expr.Key())
		case unstable.KeyValue:
			key, raw := keyInfo(expr.Key())
			value, err := tomlValueRange(contents, raw)
			if err != nil {
				return nil, err
			}
			expressions = append(expressions, expression{table: table, key: key, value: value})
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	return expressions, nil
}

func keyInfo(key unstable.Iterator) (string, unstable.Range) {
	var parts []string
	var raw unstable.Range
	first := true
	for key.Next() {
		if first {
			raw = key.Node().Raw
			first = false
		}
		parts = append(parts, string(key.Node().Data))
	}
	return strings.Join(parts, "."), raw
}

func tomlValueRange(contents []byte, key unstable.Range) (unstable.Range, error) {
	start := int(key.Offset + key.Length)
	for start < len(contents) && (contents[start] == ' ' || contents[start] == '\t') {
		start++
	}
	if start >= len(contents) || contents[start] != '=' {
		return unstable.Range{}, errors.New("expected character =")
	}
	start++
	for start < len(contents) && (contents[start] == ' ' || contents[start] == '\t') {
		start++
	}
	end := start
	if start < len(contents) && (contents[start] == '[' || contents[start] == '{') {
		end, ok := matchingTOMLDelimiter(contents, start)
		if !ok {
			return unstable.Range{}, errors.New("TOML value is not closed")
		}
		return unstable.Range{Offset: uint32(start), Length: uint32(end + 1 - start)}, nil
	}
	if start < len(contents) && (contents[start] == '\'' || contents[start] == '"') {
		if next, ok := skipTOMLString(contents, start); ok {
			end = next
			return unstable.Range{Offset: uint32(start), Length: uint32(end - start)}, nil
		}
	}
	for end < len(contents) && contents[end] != '\n' && contents[end] != '\r' {
		end++
	}
	for end > start && (contents[end-1] == ' ' || contents[end-1] == '\t') {
		end--
	}
	line := stripTOMLComment(contents[start:end])
	end = start + len(line)
	for end > start && (contents[end-1] == ' ' || contents[end-1] == '\t') {
		end--
	}
	return unstable.Range{Offset: uint32(start), Length: uint32(end - start)}, nil
}

func matchingTOMLDelimiter(contents []byte, start int) (int, bool) {
	open := contents[start]
	close := byte(']')
	if open == '{' {
		close = '}'
	}
	depth := 0
	for i := start; i < len(contents); i++ {
		if next, ok := skipTOMLString(contents, i); ok {
			i = next - 1
			continue
		}
		if contents[i] == '#' {
			for i < len(contents) && contents[i] != '\n' {
				i++
			}
			continue
		}
		switch contents[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func tableValues(contents []byte, table string) ([]valueSpan, error) {
	expressions, err := parseExpressions(contents)
	if err != nil {
		return nil, err
	}
	values := []valueSpan{}
	for _, expr := range expressions {
		if expr.table != table || expr.key == "" {
			continue
		}
		start := int(expr.value.Offset)
		end := start + int(expr.value.Length)
		if start < 0 || end > len(contents) || start > end {
			return nil, errors.New("TOML value range is unavailable")
		}
		values = append(values, valueSpan{key: expr.key, value: append([]byte(nil), contents[start:end]...)})
	}
	return values, nil
}

func upsertTOMLValue(contents []byte, path, table, key, value string) ([]byte, error) {
	expressions, err := parseExpressions(contents)
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	for _, expr := range expressions {
		if expr.table != table || expr.key != key {
			continue
		}
		start := int(expr.value.Offset)
		end := start + int(expr.value.Length)
		if start < 0 || end > len(contents) || start > end {
			return nil, errors.New("TOML value range is unavailable")
		}
		result := make([]byte, 0, len(contents)-end+start+len(value))
		result = append(result, contents[:start]...)
		result = append(result, value...)
		result = append(result, contents[end:]...)
		return result, nil
	}
	return appendTOMLKey(contents, table, key, value), nil
}

func appendTOMLArrayValue(contents []byte, path, table, key, value string) ([]byte, error) {
	expressions, err := parseExpressions(contents)
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	quoted := strconv.Quote(value)
	for _, expr := range expressions {
		if expr.table != table || expr.key != key {
			continue
		}
		start := int(expr.value.Offset)
		end := start + int(expr.value.Length)
		if start < 0 || end > len(contents) || start > end {
			return nil, errors.New("TOML array range is unavailable")
		}
		array := contents[start:end]
		if !bytes.HasPrefix(bytes.TrimSpace(array), []byte("[")) {
			return nil, fmt.Errorf("%s.%s must be an array", table, key)
		}
		if arrayHasString(array, value) {
			return contents, nil
		}
		updated := appendArrayValue(contents, start, end-1, quoted)
		return updated, nil
	}
	return appendTOMLKey(contents, table, key, "["+quoted+"]"), nil
}

func arrayHasString(array []byte, value string) bool {
	quoted := strconv.Quote(value)
	return bytes.Contains(array, []byte(quoted))
}

func appendTOMLKey(contents []byte, table, key, value string) []byte {
	line := formatTOMLKey(key) + " = " + value + "\n"
	if table == "" {
		if expressions, err := parseExpressions(contents); err == nil {
			insert := -1
			for _, expr := range expressions {
				if expr.table == "" {
					insert = int(expr.value.Offset + expr.value.Length)
				}
			}
			if insert < 0 {
				parser := unstable.Parser{}
				parser.Reset(contents)
				for parser.NextExpression() {
					expr := parser.Expression()
					if expr.Kind != unstable.Table && expr.Kind != unstable.ArrayTable {
						continue
					}
					_, raw := keyInfo(expr.Key())
					insert = int(raw.Offset)
					if lineStart := bytes.LastIndexByte(contents[:insert], '\n'); lineStart >= 0 {
						insert = lineStart + 1
					} else {
						insert = 0
					}
					return appendAt(contents, insert, line)
				}
			}
			if insert >= 0 {
				for insert < len(contents) && contents[insert] != '\n' {
					insert++
				}
				if insert < len(contents) {
					insert++
				}
				return appendAt(contents, insert, line)
			}
		}
	}
	if table != "" {
		if expressions, err := parseExpressions(contents); err == nil {
			insert := -1
			for _, expr := range expressions {
				if expr.table != table {
					continue
				}
				insert = int(expr.value.Offset + expr.value.Length)
			}
			if insert >= 0 {
				for insert < len(contents) && contents[insert] != '\n' {
					insert++
				}
				if insert < len(contents) {
					insert++
				}
				return appendAt(contents, insert, line)
			}
		}
		if start, ok := tableHeaderEnd(contents, table); ok {
			return appendAt(contents, start, line)
		}
	}
	var b strings.Builder
	b.Write(contents)
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		b.WriteByte('\n')
	}
	if table != "" {
		b.WriteByte('[')
		b.WriteString(table)
		b.WriteString("]\n")
	}
	b.WriteString(line)
	return []byte(b.String())
}

func appendAt(contents []byte, offset int, line string) []byte {
	result := make([]byte, 0, len(contents)+len(line))
	result = append(result, contents[:offset]...)
	result = append(result, line...)
	result = append(result, contents[offset:]...)
	return result
}

func tableHeaderEnd(contents []byte, table string) (int, bool) {
	parser := unstable.Parser{}
	parser.Reset(contents)
	for parser.NextExpression() {
		expr := parser.Expression()
		if expr.Kind != unstable.Table && expr.Kind != unstable.ArrayTable {
			continue
		}
		name, raw := keyInfo(expr.Key())
		if name != table {
			continue
		}
		start := int(raw.Offset)
		if lineStart := bytes.LastIndexByte(contents[:start], '\n'); lineStart >= 0 {
			start = lineStart + 1
		} else {
			start = 0
		}
		if lineEnd := bytes.IndexByte(contents[start:], '\n'); lineEnd >= 0 {
			return start + lineEnd + 1, true
		}
		return len(contents), true
	}
	return 0, false
}

func formatTOMLKey(key string) string {
	if key == "" {
		return key
	}
	for index, char := range key {
		validLetter := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
		validDigit := index > 0 && char >= '0' && char <= '9'
		if char != '_' && char != '-' && !validLetter && !validDigit {
			return strconv.Quote(key)
		}
	}
	return key
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
