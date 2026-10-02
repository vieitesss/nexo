package setup

import (
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/version"
)

func TestRunningRevisionNormalizesReleaseVersions(t *testing.T) {
	previous := version.Version
	t.Cleanup(func() { version.Version = previous })
	for _, test := range []struct {
		version string
		want    string
	}{
		{version: "1.2.3", want: "v1.2.3"},
		{version: "v1.2.3", want: "v1.2.3"},
		{version: "abcdef1234567890", want: "abcdef1234567890"},
		{version: "v0.0.0-20260220123456-abcdef123456", want: "v0.0.0-20260220123456-abcdef123456"},
	} {
		version.Version = test.version
		got, err := runningRevision()
		if err != nil || got != test.want {
			t.Errorf("runningRevision() for %q = %q, %v; want %q", test.version, got, err, test.want)
		}
	}
}

func TestRunningRevisionRejectsDevelopmentBuild(t *testing.T) {
	previous := version.Version
	t.Cleanup(func() { version.Version = previous })
	version.Version = "dev"
	if _, err := runningRevision(); err == nil {
		t.Fatal("runningRevision() succeeded for development build")
	} else if got := err.Error(); got == "" || !strings.Contains(got, "no usable release tag or commit") {
		t.Fatalf("runningRevision() error = %q, want a clear unusable revision message", got)
	}
}
