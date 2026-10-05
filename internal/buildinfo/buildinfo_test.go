package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestReleaseModuleAndDevelopmentMetadata(t *testing.T) {
	oldVersion, oldRevision := Version, Revision
	t.Cleanup(func() { Version, Revision = oldVersion, oldRevision })
	for _, test := range []struct {
		version, revision, module, vcs, wantVersion, wantRevision string
		ok                                                        bool
	}{
		{"v1.2.3", "release-sha", "v1.0.0", "vcs-sha", "v1.2.3", "release-sha", true},
		{"", "", "v1.0.0", "vcs-sha", "v1.0.0", "vcs-sha", true},
		{"", "", "(devel)", "", "dev", "unknown", true},
		{"", "", "", "", "dev", "unknown", false},
	} {
		Version, Revision = test.version, test.revision
		info := &debug.BuildInfo{Main: debug.Module{Version: test.module}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: test.vcs}}}
		want := test.wantVersion + " revision=" + test.wantRevision + " (" + runtime.Version() + ")"
		if got := buildString(info, test.ok); got != want {
			t.Fatalf("%q != %q", got, want)
		}
		if got := tag(info, test.ok); got != test.wantVersion {
			t.Fatal(got)
		}
	}
	Version, Revision = "test-version", "test-revision"
	if Tag() != Version || !strings.HasPrefix(String(), Version+" revision="+Revision) {
		t.Fatal(String())
	}
}
