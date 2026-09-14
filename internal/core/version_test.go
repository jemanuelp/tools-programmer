package core

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	sha := strings.Repeat("a", 40)
	checkout := func(revision, modified string) *debug.BuildInfo {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: modified},
		}}
	}
	for _, tt := range []struct {
		name, tag, archiveRevision, archiveState string
		info                                     *debug.BuildInfo
		commit, state, build                     string
	}{
		{name: "default unknown", commit: "unknown", state: "unknown", build: "development"},
		{name: "plain clean build", info: checkout(sha, "false"), commit: sha, state: "clean", build: "development"},
		{name: "release", tag: "v" + Version, info: checkout(sha, "false"), commit: sha, state: "clean", build: "release"},
		{name: "dirty release refused", tag: "v" + Version, info: checkout(sha, "true"), commit: sha, state: "dirty", build: "development"},
		{name: "wrong tag", tag: "v9.0.0", info: checkout(sha, "false"), commit: sha, state: "clean", build: "development"},
		{name: "malformed revision", tag: "v" + Version, info: checkout("short", "false"), commit: "unknown", state: "clean", build: "development"},
		{name: "unknown state", tag: "v" + Version, info: checkout(sha, "invalid"), commit: sha, state: "unknown", build: "development"},
		{name: "archive release", tag: "v" + Version, archiveRevision: sha, archiveState: "clean", commit: sha, state: "clean", build: "release"},
		{name: "archive incomplete", tag: "v" + Version, archiveRevision: sha, commit: sha, state: "unknown", build: "development"},
		{name: "invalid archive", tag: "v" + Version, archiveRevision: "bad+metadata", archiveState: "invalid", commit: "unknown", state: "unknown", build: "development"},
		{name: "archive cannot hide dirty", tag: "v" + Version, archiveRevision: strings.Repeat("b", 40), archiveState: "clean", info: checkout(sha, "true"), commit: sha, state: "dirty", build: "development"},
		{name: "partial VCS blocks archive", tag: "v" + Version, archiveRevision: sha, archiveState: "clean", info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}}}, commit: "unknown", state: "unknown", build: "development"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVersion(tt.info, tt.tag, tt.archiveRevision, tt.archiveState)
			version := Version
			if tt.build == "development" {
				version += "-dev+" + tt.commit + "." + tt.state
			}
			want := VersionInfo{Version: version, Commit: tt.commit, State: tt.state, Build: tt.build}
			if got != want {
				t.Fatalf("got %+v; want %+v", got, want)
			}
		})
	}
}
