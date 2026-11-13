package sessionname

import (
	"net/netip"
	"net/url"
	"strings"
	"unicode"
)

// parseLocation interprets only remote transport paths with exactly two or three components.
func parseLocation(location string, stripGitSuffix bool) (Values, bool) {
	var escaped string
	if strings.Contains(location, "://") {
		u, err := url.Parse(location)
		if err != nil || u.Hostname() == "" {
			return Values{}, false
		}
		switch u.Scheme {
		case "http", "https", "ssh", "git":
		default:
			return Values{}, false
		}
		escaped = strings.TrimPrefix(u.EscapedPath(), "/")
	} else {
		if i := strings.IndexAny(location, "?#"); i >= 0 {
			location = location[:i]
		}
		before, after, found := strings.Cut(location, ":")
		if strings.Contains(before, "[") {
			before, after, found = strings.Cut(location, "]:")
			before += "]"
		}
		if !found || before == "" || strings.ContainsAny(before, "/\\") || strings.TrimPrefix(before, "git@") == "" {
			return Values{}, false
		}
		host := before
		if _, h, hasUser := strings.Cut(before, "@"); hasUser {
			host = h
		}
		if host == "" || strings.Contains(host, "@") {
			return Values{}, false
		}
		if strings.ContainsAny(host, "[]") {
			addr, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
			if !strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") || err != nil || !addr.Is6() {
				return Values{}, false
			}
		} else if tail, _, closed := strings.Cut(after, "]:"); closed {
			// Prefer malformed IPv6 authority over an ambiguous numeric-host SCP path.
			if addr, err := netip.ParseAddr(host + ":" + tail); err == nil && addr.Is6() {
				return Values{}, false
			}
		}
		escaped = after
	}
	escaped = strings.TrimSuffix(escaped, "/")
	components := strings.Split(escaped, "/")
	if len(components) != 2 && len(components) != 3 {
		return Values{}, false
	}
	for i, component := range components {
		decoded, err := url.PathUnescape(component)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") {
			return Values{}, false
		}
		for _, r := range decoded {
			if unicode.IsControl(r) {
				return Values{}, false
			}
		}
		components[i] = decoded
	}
	if stripGitSuffix {
		components[len(components)-1] = strings.TrimSuffix(components[len(components)-1], ".git")
		if repo := components[len(components)-1]; repo == "" || repo == "." || repo == ".." {
			return Values{}, false
		}
	}
	if len(components) == 2 {
		return Values{Org: components[0], Repo: components[1]}, true
	}
	return Values{Org: components[0], Team: components[1], Repo: components[2]}, true
}
