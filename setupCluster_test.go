package main

import (
	"strings"
	"testing"
)

func TestBecomeExportLineHandsThePasswordToAnsible(t *testing.T) {
	if line := becomeExportLine(false); line != "" {
		t.Errorf("becomeExportLine(false) = %q, want no line when there is no password", line)
	}

	line := becomeExportLine(true)
	// Ansible 2.17 reads ANSIBLE_BECOME_PASSWORD_FILE (the default of
	// --become-password-file). ANSIBLE_BECOME_PASSWORD is not an Ansible
	// variable, and exporting it leaves the playbook with no password.
	want := "export ANSIBLE_BECOME_PASSWORD_FILE=/data/idocluster/ansible-become-password"
	if !strings.Contains(line, want) {
		t.Errorf("becomeExportLine(true) = %q, want it to contain %q", line, want)
	}
}
