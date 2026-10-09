package main

// Tests for handing files created by the app back to the user who invoked it
// with sudo.

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func ownerOf(t *testing.T, path string) (uint32, uint32) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("can't read the raw ownership of a file on this platform")
	}
	return st.Uid, st.Gid
}

func testTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "roles", "etcd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roles", "etcd", "main.yml"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("main.yml", filepath.Join(dir, "roles", "etcd", "link.yml")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Not started with sudo: nothing is chowned.
func TestChownToRunnerWithoutSudoIDsDoesNothing(t *testing.T) {
	t.Setenv("SUDO_UID", "")
	t.Setenv("SUDO_GID", "")

	dir := testTree(t)
	uid, gid := ownerOf(t, dir)

	if err := chownToRunner(dir); err != nil {
		t.Errorf("chownToRunner() = %v, want nil", err)
	}
	if gotUid, gotGid := ownerOf(t, dir); gotUid != uid || gotGid != gid {
		t.Errorf("ownership changed to %d:%d, want %d:%d", gotUid, gotGid, uid, gid)
	}
}

// SUDO_UID/SUDO_GID that are not plain numbers are refused rather than passed
// on, since the app runs as root.
func TestChownToRunnerIgnoresMalformedSudoIDs(t *testing.T) {
	t.Setenv("SUDO_UID", "0; touch "+filepath.Join(t.TempDir(), "pwned"))
	t.Setenv("SUDO_GID", "$(id -u)")

	dir := testTree(t)
	uid, gid := ownerOf(t, dir)

	if err := chownToRunner(dir); err != nil {
		t.Errorf("chownToRunner() = %v, want nil", err)
	}
	if gotUid, gotGid := ownerOf(t, dir); gotUid != uid || gotGid != gid {
		t.Errorf("ownership changed to %d:%d, want %d:%d", gotUid, gotGid, uid, gid)
	}
}

// Every file of the tree, at any depth, is given to the sudo invoker. A group
// the test user belongs to but differs from the current one is used so that the
// change is observable without root.
func TestChownToRunnerGivesTreeToSudoUser(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, ownership changes are not observable")
	}
	uid, gid := ownerOf(t, t.TempDir())
	groups, err := os.Getgroups()
	if err != nil {
		t.Skipf("can't list groups: %v", err)
	}
	newGid := -1
	for _, g := range groups {
		if uint32(g) != gid {
			newGid = g
			break
		}
	}
	if newGid < 0 {
		t.Skip("the test user has only one group")
	}

	dir := testTree(t)
	t.Setenv("SUDO_UID", strconv.Itoa(int(uid)))
	t.Setenv("SUDO_GID", strconv.Itoa(newGid))

	if err := chownToRunner(dir); err != nil {
		t.Fatalf("chownToRunner() = %v, want nil", err)
	}
	for _, path := range []string{
		dir,
		filepath.Join(dir, "roles"),
		filepath.Join(dir, "roles", "etcd"),
		filepath.Join(dir, "roles", "etcd", "main.yml"),
		filepath.Join(dir, "roles", "etcd", "link.yml"),
	} {
		gotUid, gotGid := ownerOf(t, path)
		if gotUid != uid || gotGid != uint32(newGid) {
			t.Errorf("%s is owned by %d:%d, want %d:%d", path, gotUid, gotGid, uid, newGid)
		}
	}
}
