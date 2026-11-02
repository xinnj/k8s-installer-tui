package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDownloadPatchCopiesTheCacheThroughBecome pins the transfer of a cached
// file to the node to a module that runs through Ansible's become. The role's
// own synchronize runs its remote rsync outside become: through sudo, where a
// sudo that needs a password cannot be answered; without it, as the SSH user,
// into a directory the role created for root. Either way the copy fails for a
// non-root SSH user, and making that directory writable by the user would let
// the user replace binaries that root later installs.
func TestDownloadPatchCopiesTheCacheThroughBecome(t *testing.T) {
	task := findPatchTask(t, filepath.Join("patches", "roles", "download", "tasks", "download_file.yml"),
		"Download_file | Copy file from cache to nodes, if it is available")

	if _, ok := task["ansible.builtin.copy"]; !ok {
		t.Errorf("the cache copy uses %v, want ansible.builtin.copy so it runs through become", mapKeys(task))
	}
	if _, ok := task["ansible.posix.synchronize"]; ok {
		t.Error("the cache copy still uses ansible.posix.synchronize, whose remote rsync runs outside become")
	}
}

// TestContainerPatchCopiesTheCachedImageThroughBecome does the same for the
// container images, which are pushed into the same release directory.
func TestContainerPatchCopiesTheCachedImageThroughBecome(t *testing.T) {
	task := findPatchTask(t, filepath.Join("patches", "roles", "download", "tasks", "download_container.yml"),
		"Download_container | Upload image to node if it is cached")

	if _, ok := task["ansible.builtin.copy"]; !ok {
		t.Errorf("the image upload uses %v, want ansible.builtin.copy so it runs through become", mapKeys(task))
	}
	if _, ok := task["ansible.posix.synchronize"]; ok {
		t.Error("the image upload still uses ansible.posix.synchronize, whose remote rsync runs outside become")
	}
}

// findPatchTask returns the task with the given name from a kubespray task
// file. The download role puts its steps in a block, so both the top-level
// entries and the block's steps are searched.
func findPatchTask(t *testing.T, path, name string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var items []map[string]any
	if err := yaml.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}

	for _, item := range items {
		if taskName(item) == name {
			return item
		}
		block, _ := item["block"].([]any)
		for _, entry := range block {
			task, _ := entry.(map[string]any)
			if taskName(task) == name {
				return task
			}
		}
	}

	t.Fatalf("%s has no task named %q", path, name)

	return nil
}

func taskName(task map[string]any) string {
	name, _ := task["name"].(string)
	return name
}

func mapKeys(task map[string]any) []string {
	keys := make([]string, 0, len(task))
	for key := range task {
		keys = append(keys, key)
	}
	return keys
}
