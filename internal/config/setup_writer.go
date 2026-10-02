package config

import (
	"bytes"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// SetupValues are the values setup may add to the shared config. Existing
// values are retained; selected remote machines and backends are appended or
// filled only when needed.
type SetupValues struct {
	Notify   *bool
	Machines []string
	Backends map[string]string
}

// MergeSetup updates setup-owned keys without decoding and re-encoding the
// whole TOML document. This deliberately uses the same atomic config writer as
// AddProjectPath, so comments and unrelated formatting survive. New sections
// are appended with a short comment describing setup's ownership.
func MergeSetup(path string, values SetupValues) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("config path is empty")
	}
	contents, mode, existed, err := readConfigFile(path)
	if err != nil {
		return false, err
	}
	if existed {
		var cfg Config
		if err := decode(contents, path, &cfg); err != nil {
			return false, err
		}
	}
	updated := append([]byte(nil), contents...)
	changed := false
	if values.Notify != nil {
		var replacement string
		if *values.Notify {
			replacement = "true"
		} else {
			replacement = "false"
		}
		var next []byte
		next, changed = upsertTableKey(updated, "agents", "notify", replacement, "# nexo setup manages this value")
		updated = next
	}
	if len(values.Machines) > 0 {
		for _, machine := range values.Machines {
			machine = strings.TrimSpace(machine)
			if machine == "" {
				continue
			}
			var next []byte
			next, changed = appendStringArrayValue(updated, "remote", "machines", machine, changed)
			updated = next
		}
	}
	backendMachines := make([]string, 0, len(values.Backends))
	for machine := range values.Backends {
		backendMachines = append(backendMachines, machine)
	}
	sort.Strings(backendMachines)
	for _, machine := range backendMachines {
		backend := values.Backends[machine]
		if strings.TrimSpace(machine) == "" || strings.TrimSpace(backend) == "" {
			continue
		}
		var next []byte
		next, changed = upsertTableKey(updated, "remote.backends", machine, strconv.Quote(strings.TrimSpace(backend)), "# nexo setup records the detected remote backend")
		updated = next
	}
	if !changed && bytes.Equal(contents, updated) {
		return false, nil
	}
	if err := writeConfigFile(path, updated, mode); err != nil {
		return false, err
	}
	return true, nil
}

func upsertTableKey(contents []byte, table, key, value, comment string) ([]byte, bool) {
	lines := strings.SplitAfter(string(contents), "\n")
	header := "[" + table + "]"
	start, end := -1, len(lines)
	for i, line := range lines {
		trim := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			if trim == header {
				start = i
				continue
			}
			if start >= 0 {
				end = i
				break
			}
		}
	}
	keyPrefix := key + " ="
	if start >= 0 {
		for i := start + 1; i < end; i++ {
			trim := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trim, keyPrefix) || trim == key {
				lineEnd := "\n"
				if !strings.HasSuffix(lines[i], "\n") {
					lineEnd = ""
				}
				newLine := key + " = " + value
				if comment != "" {
					newLine += "  " + comment
				}
				candidate := newLine + lineEnd
				if lines[i] == candidate {
					return contents, false
				}
				lines[i] = candidate
				return []byte(strings.Join(lines, "")), true
			}
		}
		insert := end
		line := key + " = " + value
		if comment != "" {
			line += "  " + comment
		}
		lines = append(lines, "")
		copy(lines[insert+1:], lines[insert:])
		lines[insert] = line + "\n"
		return []byte(strings.Join(lines, "")), true
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" && !strings.HasSuffix(lines[len(lines)-1], "\n") {
		lines[len(lines)-1] += "\n"
	}
	lines = append(lines, header+"\n")
	line := key + " = " + value
	if comment != "" {
		line += "  " + comment
	}
	lines = append(lines, line+"\n")
	return []byte(strings.Join(lines, "")), true
}

func appendStringArrayValue(contents []byte, table, key, value string, changed bool) ([]byte, bool) {
	lines := strings.SplitAfter(string(contents), "\n")
	header := "[" + table + "]"
	start, end := -1, len(lines)
	for i, line := range lines {
		trim := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			if trim == header {
				start = i
				continue
			}
			if start >= 0 {
				end = i
				break
			}
		}
	}
	if start < 0 {
		if len(lines) > 0 && lines[len(lines)-1] != "" && !strings.HasSuffix(lines[len(lines)-1], "\n") {
			lines[len(lines)-1] += "\n"
		}
		lines = append(lines, header+"\n", key+" = ["+strconv.Quote(value)+"]\n")
		return []byte(strings.Join(lines, "")), true
	}
	for i := start + 1; i < end; i++ {
		trim := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trim, key+" =") {
			continue
		}
		if strings.Contains(lines[i], strconv.Quote(value)) {
			return contents, changed
		}
		line := strings.TrimSuffix(lines[i], "\n")
		pos := strings.LastIndex(line, "]")
		if pos < 0 {
			continue
		}
		prefix, suffix := line[:pos], line[pos:]
		sep := ""
		if !strings.HasSuffix(strings.TrimSpace(prefix), "[") {
			sep = ", "
		}
		lines[i] = prefix + sep + strconv.Quote(value) + suffix
		if !strings.HasSuffix(lines[i], "\n") {
			lines[i] += "\n"
		}
		return []byte(strings.Join(lines, "")), true
	}
	// The table exists but not this key: insert before its next table.
	lines = append(lines, "")
	copy(lines[end+1:], lines[end:])
	lines[end] = key + " = [" + strconv.Quote(value) + "]\n"
	return []byte(strings.Join(lines, "")), true
}
