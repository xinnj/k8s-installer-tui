package main

import "runtime/debug"

const appName = "K8s Installer TUI"

// version is overridden at release time via:
//
//	go build -ldflags "-X main.version=v2.4.0"
//
// It must be a var, not a const, for the linker to set it.
var version = "dev"

func versionString() string {
	revision := ""
	if version == "dev" {
		revision = vcsRevision()
	}
	return appName + " " + versionLabel(version, revision)
}

// versionLabel is pure so it can be tested without depending on the test
// binary's embedded VCS metadata.
func versionLabel(v, revision string) string {
	if v == "" {
		v = "dev"
	}
	if v != "dev" || revision == "" {
		return v
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	return v + " (" + revision + ")"
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}
