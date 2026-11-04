package main

import "testing"

func TestGetHostsListHandlesNamesWithoutTrailingDigits(t *testing.T) {
	old := inventory
	t.Cleanup(func() { inventory = old })

	inventory = Inventory{}
	inventory.All.Hosts = map[string]Host{
		"cp-a":  {Ansible_host: "10.0.0.11"},
		"cp-b":  {Ansible_host: "10.0.0.12"},
		"node1": {Ansible_host: "10.0.0.13"},
	}

	got := getHostsList()
	if len(got) != 3 {
		t.Fatalf("getHostsList() returned %d entries, want 3: %v", len(got), got)
	}
}
