package sessionname

import "testing"

func TestParseLocation(t *testing.T) {
	cases := []struct {
		name, location string
		strip          bool
		want           Values
		ok             bool
	}{
		{"github scp", "git@github.com:acme/api.git", true, Values{Org: "acme", Repo: "api"}, true},
		{"scp query", "git@example.org:acme/api.git?token=secret", true, Values{Org: "acme", Repo: "api"}, true},
		{"scp fragment", "git@example.org:acme/api.git#fragment", true, Values{Org: "acme", Repo: "api"}, true},
		{"scp query fragment", "git@example.org:acme/api.git?token=secret#fragment", true, Values{Org: "acme", Repo: "api"}, true},
		{"scp fragment before query", "git@example.org:acme/api.git#fragment?token=secret", true, Values{Org: "acme", Repo: "api"}, true},
		{"scp query trailing slash", "git@example.org:acme/platform/api.git/?token=secret#fragment", true, Values{Org: "acme", Team: "platform", Repo: "api"}, true},
		{"scp hg query suffix", "example.org:acme/api.git?token=secret#fragment", false, Values{Org: "acme", Repo: "api.git"}, true},
		{"scp bracketed host", "git@[2001:db8::1]:acme/api.git", true, Values{Org: "acme", Repo: "api"}, true},
		{"scp bracketed host no user", "[2001:db8::1]:acme/platform/api.git", true, Values{Org: "acme", Team: "platform", Repo: "api"}, true},
		{"scp bracketed host query fragment", "git@[2001:db8::1]:acme/api.git?token=secret#fragment", true, Values{Org: "acme", Repo: "api"}, true},
		{"scp bracketed host missing close", "git@[2001:db8::1:acme/api.git", true, Values{}, false},
		{"scp bracketed host missing open", "git@2001]:acme/api.git", true, Values{}, false},
		{"scp ipv6 host missing open", "git@2001:db8::1]:acme/api.git", true, Values{}, false},
		{"scp ipv6 host missing open no user", "2001:db8::1]:acme/api.git", true, Values{}, false},
		{"scp path bracket colon", "git@example.org:acme]:platform/api.git", true, Values{Org: "acme]:platform", Repo: "api"}, true},
		{"scp numeric host path bracket colon", "git@2001:acme]:platform/api.git", true, Values{Org: "acme]:platform", Repo: "api"}, true},
		{"scp path ipv6 text after user marker", "example.org:acme@2001:db8::1]:platform/api.git", true, Values{Org: "acme@2001:db8::1]:platform", Repo: "api"}, true},
		{"scp repo ipv6 text", "git@example.org:acme/2001:db8::1]:api.git", true, Values{Org: "acme", Repo: "2001:db8::1]:api"}, true},
		{"https path ipv6 text", "https://example.org/db8::1]:acme/api.git", true, Values{Org: "db8::1]:acme", Repo: "api"}, true},
		{"scp bracketed host missing separator", "git@[2001:db8::1]acme/api.git", true, Values{}, false},
		{"scp bracketed host trailing junk", "git@[2001:db8::1]junk:acme/api.git", true, Values{}, false},
		{"scp bracketed host invalid address", "git@[2001:db8::zzz]:acme/api.git", true, Values{}, false},
		{"scp bracketed host empty", "git@[]:acme/api.git", true, Values{}, false},
		{"scp bracketed host repeated user", "git@other@[2001:db8::1]:acme/api.git", true, Values{}, false},
		{"github https", "https://github.com/acme/api.git", true, Values{Org: "acme", Repo: "api"}, true},
		{"gitlab port", "ssh://git@gitlab.example:2222/acme/platform/api.git", true, Values{Org: "acme", Team: "platform", Repo: "api"}, true},
		{"trailing slash", "git://example.org/acme/api.git/", true, Values{Org: "acme", Repo: "api"}, true},
		{"hg suffix", "https://example.org/acme/api.git", false, Values{Org: "acme", Repo: "api.git"}, true},
		{"single suffix", "https://example.org/acme/api.git.git", true, Values{Org: "acme", Repo: "api.git"}, true},
		{"userinfo query fragment", "https://u:secret@example.org/Acme/Api?token=secret#secret", true, Values{Org: "Acme", Repo: "Api"}, true},
		{"safe escape", "https://example.org/a%20b/r%7E1", true, Values{Org: "a b", Repo: "r~1"}, true},
		{"self hosted positional", "https://private.example/prefix/acme/api", true, Values{Org: "prefix", Team: "acme", Repo: "api"}, true},
		{"absolute", "/acme/api", true, Values{}, false},
		{"relative", "acme/api", true, Values{}, false},
		{"file", "file:///acme/api", true, Values{}, false},
		{"unknown", "ftp://example.org/acme/api", true, Values{}, false},
		{"one component", "https://example.org/api", true, Values{}, false},
		{"deep", "https://example.org/a/b/c/d", true, Values{}, false},
		{"dot", "https://example.org/./api", true, Values{}, false},
		{"dotdot", "https://example.org/a/../api", true, Values{}, false},
		{"empty", "https://example.org/a//api", true, Values{}, false},
		{"invalid escape", "https://example.org/a/%GG", true, Values{}, false},
		{"encoded slash", "https://example.org/a/a%2Fb", true, Values{}, false},
		{"encoded backslash", "https://example.org/a/a%5Cb", true, Values{}, false},
		{"encoded control", "https://example.org/a/a%00b", true, Values{}, false},
		{"empty suffix repo", "https://example.org/a/.git", true, Values{}, false},
		{"dot suffix repo", "https://example.org/a/..git", true, Values{}, false},
		{"dotdot suffix repo", "https://example.org/a/...git", true, Values{}, false},
		{"missing host", "ssh:///acme/api", true, Values{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseLocation(tc.location, tc.strip)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseLocation() = (%+v, %v), want (%+v, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
