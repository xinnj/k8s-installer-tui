package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

const defaultCreateConfigPath = "install.yaml"

type cliOptions struct {
	showVersion bool
	configPath  string
	dryRun      bool
	create      bool
}

// parseOptions parses the command line in isolation from the global flag set so
// tests can exercise it repeatedly.
func parseOptions(args []string) (cliOptions, error) {
	var opts cliOptions

	fs := flag.NewFlagSet("k8s-installer-tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // main owns all user-facing output

	fs.BoolVar(&opts.showVersion, "version", false, "print version information and exit")
	fs.BoolVar(&opts.showVersion, "v", false, "print version information and exit (shorthand)")
	fs.StringVar(&opts.configPath, "config", "", "load installation answers from this file and run without prompts")
	fs.StringVar(&opts.configPath, "c", "", "load installation answers from this file and run without prompts (shorthand)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "with --config, fill the wizard and stop before installing")
	fs.BoolVar(&opts.create, "create", false, "walk the wizard and write an installation config file")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err // includes flag.ErrHelp for -h/--help
	}
	if rest := fs.Args(); len(rest) > 0 {
		return cliOptions{}, fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
	}
	configSet := false
	fs.Visit(func(set *flag.Flag) {
		if set.Name == "config" || set.Name == "c" {
			configSet = true
		}
	})
	if configSet && opts.configPath == "" {
		return cliOptions{}, fmt.Errorf("--config requires a non-empty file")
	}
	if opts.create && opts.dryRun {
		return cliOptions{}, fmt.Errorf("--create cannot be combined with --dry-run")
	}
	if opts.dryRun && opts.configPath == "" {
		return cliOptions{}, fmt.Errorf("--dry-run requires --config")
	}
	if opts.create && opts.configPath == "" {
		opts.configPath = defaultCreateConfigPath
	}

	return opts, nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: k8s-installer-tui [options]

Options:
  -c, --config FILE  With --create, write the generated config to FILE
                     (default install.yaml). Otherwise, load installation
                     answers from FILE, fill the wizard, and run the
                     installation without prompts.
      --create       Walk the wizard step by step and write an install config
                     that --config can run. IP conflict pings, SSH key
                     generation/copy, and the installation are skipped.
      --dry-run      With --config, fill the wizard and stop on the Deploy page
                     without copying keys or running Ansible.
  -v, --version      Print version information and exit.
  -h, --help         Show this help message.
`)
}
