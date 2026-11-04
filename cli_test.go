package main

// Tests for command-line option parsing. They run without root, podman or a
// terminal.

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    cliOptions
		wantErr bool
	}{
		{name: "no args", args: nil, want: cliOptions{}},
		{name: "long version", args: []string{"--version"}, want: cliOptions{showVersion: true}},
		{name: "short version", args: []string{"-v"}, want: cliOptions{showVersion: true}},
		{name: "short config", args: []string{"-c", "cluster.yaml"}, want: cliOptions{configPath: "cluster.yaml"}},
		{name: "long config", args: []string{"--config", "cluster.yaml"}, want: cliOptions{configPath: "cluster.yaml"}},
		{name: "config equals", args: []string{"--config=cluster.yaml"}, want: cliOptions{configPath: "cluster.yaml"}},
		{name: "dry run", args: []string{"-c", "cluster.yaml", "--dry-run"}, want: cliOptions{configPath: "cluster.yaml", dryRun: true}},
		{name: "dry run without config", args: []string{"--dry-run"}, wantErr: true},
		{name: "config without value", args: []string{"--config"}, wantErr: true},
		{name: "empty config", args: []string{"--config="}, wantErr: true},
		{name: "empty short config", args: []string{"-c", ""}, wantErr: true},
		{name: "create default output", args: []string{"--create"}, want: cliOptions{create: true, configPath: "install.yaml"}},
		{name: "create short output", args: []string{"--create", "-c", "out.yaml"}, want: cliOptions{create: true, configPath: "out.yaml"}},
		{name: "create long output", args: []string{"--create", "--config", "out.yaml"}, want: cliOptions{create: true, configPath: "out.yaml"}},
		{name: "create equals output", args: []string{"--create", "--config=out.yaml"}, want: cliOptions{create: true, configPath: "out.yaml"}},
		{name: "create empty output", args: []string{"--create", "--config="}, wantErr: true},
		{name: "create dry run", args: []string{"--create", "--dry-run"}, wantErr: true},
		{name: "create dry run with output", args: []string{"--create", "-c", "out.yaml", "--dry-run"}, wantErr: true},
		{name: "unknown flag", args: []string{"--nope"}, wantErr: true},
		{name: "positional argument", args: []string{"extra"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOptions(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseOptions(%v) = nil error, want error", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOptions(%v) error: %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("parseOptions(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseOptionsCreateRejectsDryRun(t *testing.T) {
	for _, args := range [][]string{
		{"--create", "--dry-run"},
		{"--create", "-c", "out.yaml", "--dry-run"},
	} {
		_, err := parseOptions(args)
		if err == nil {
			t.Fatalf("parseOptions(%v) accepted --dry-run with --create", args)
		}
		if !strings.Contains(err.Error(), "--create") {
			t.Fatalf("parseOptions(%v) error = %q, want it to mention --create", args, err)
		}
	}
}

func TestParseOptionsHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			_, err := parseOptions([]string{arg})
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("parseOptions(%q) error = %v, want flag.ErrHelp", arg, err)
			}
		})
	}
}
