package main

// Tests for the Network page's create-mode conflict-check bypass.

import "testing"

func TestNetworkConflictCheckEnabled(t *testing.T) {
	oldCheck, oldCreate := checkConflicts, createMode
	t.Cleanup(func() {
		checkConflicts, createMode = oldCheck, oldCreate
	})

	tests := []struct {
		name     string
		check    bool
		create   bool
		expected bool
	}{
		{name: "normal with checking", check: true, create: false, expected: true},
		{name: "normal without checking", check: false, create: false, expected: false},
		{name: "create mode with the box checked", check: true, create: true, expected: false},
		{name: "create mode with the box cleared", check: false, create: true, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkConflicts, createMode = tt.check, tt.create
			if got := networkConflictCheckEnabled(); got != tt.expected {
				t.Fatalf("enabled = %v, want %v", got, tt.expected)
			}
		})
	}
}
