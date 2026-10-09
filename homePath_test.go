package main

// Tests for resolving the home directory of the user who invoked the app.

import (
	"os"
	"os/user"
	"testing"
)

func TestResolveHomePathUsesSudoUserHome(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("can't look up the current user: %v", err)
	}
	t.Setenv("SUDO_USER", me.Username)

	if got := resolveHomePath(); got != me.HomeDir {
		t.Errorf("resolveHomePath() = %q, want SUDO_USER's home %q", got, me.HomeDir)
	}
}

func TestResolveHomePathFallsBackWithoutSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	want, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory in this environment: %v", err)
	}

	if got := resolveHomePath(); got != want {
		t.Errorf("resolveHomePath() = %q, want %q", got, want)
	}
}

func TestResolveHomePathFallsBackOnUnknownSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "no-such-user-for-home-path-test")
	want, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory in this environment: %v", err)
	}

	if got := resolveHomePath(); got != want {
		t.Errorf("resolveHomePath() = %q, want %q", got, want)
	}
}
