package core

import "runtime/debug"

// Version is the next semantic release version, independent of the database schema.
const Version = "0.2.0"

// ReleaseTag is stamped only when building a verified release tag. ArchiveRevision
// and ArchiveState are optional provenance assertions for builds without VCS data.
// Linker stamps are builder assertions, not proof of tag publication.
var ReleaseTag, ArchiveRevision, ArchiveState string

type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	State   string `json:"state"`
	Build   string `json:"build"`
}

// BuildVersion reads embedded metadata; it never runs Git or accesses local state.
func BuildVersion() VersionInfo {
	info, _ := debug.ReadBuildInfo()
	return resolveVersion(info, ReleaseTag, ArchiveRevision, ArchiveState)
}

func resolveVersion(info *debug.BuildInfo, tag, revision, state string) VersionInfo {
	vcs := false
	if info != nil {
		for _, setting := range info.Settings {
			if setting.Key == "vcs" || setting.Key == "vcs.revision" || setting.Key == "vcs.modified" {
				vcs = true
			}
		}
		if vcs {
			// Never let archive assertions override embedded checkout evidence.
			revision, state = "", ""
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					revision = setting.Value
				case "vcs.modified":
					if setting.Value == "true" {
						state = "dirty"
					} else if setting.Value == "false" {
						state = "clean"
					}
				}
			}
		}
	}
	if !ValidSHA(revision) {
		revision = "unknown"
	}
	if state != "clean" && state != "dirty" {
		state = "unknown"
	}
	result := VersionInfo{Commit: revision, State: state, Build: "development"}
	if tag == "v"+Version && revision != "unknown" && state == "clean" {
		result.Version, result.Build = Version, "release"
		return result
	}
	result.Version = Version + "-dev+" + revision + "." + state
	return result
}
