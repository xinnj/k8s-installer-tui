# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development

```bash
# Build
go build -o k8s-installer-tui .

# The app must run as root/sudo on a Linux host (or VM) with:
# - Python 3 available
# - Podman (or the bundled podman-launcher)
# - A kubespray-*.tar.gz archive alongside the binary
# - The kubespray-runtime container image (pulled automatically or loaded from offline path)
```

Run the Go tests with `go test ./...` — unit tests on the pure helpers (vars, HA mode, SSH diagnosis parsing, form items) that need no root, container or nodes. The Python inventory builder (`inventory_builder/`) uses tox: `tox -e py3`.

## Architecture

This is a **single-binary TUI wizard** (Go + [tview](https://github.com/rivo/tview)) that drives [Kubespray](https://github.com/kubernetes-sigs/kubespray) to provision Kubernetes clusters. All Go code lives in one `main` package split across files by UI page.

### Page flow (wizard steps)

```
Setup Mode ──→ Project (New/Load) ──→ Edit Hosts ──→ Features ──→ HA Mode
                                                                       │
                                                                       v
Setup Cluster ←── Deploy Cluster ←── Mirror ←────────────────── Network
```

- **New cluster path**: Steps through all pages sequentially.
- **Add nodes path**: Setup Mode → Project (Load) → Edit Hosts → Deploy Cluster → Setup Cluster.
- **Create-config path (`--create`)**: Setup Mode (Add Nodes disabled) → Project → Edit Hosts → Features → HA Mode → Network → Mirror → Save Config. Deploy and Setup are not reached.

Each page is a `tview.Flex` registered in `main.go:pages.AddPage()`. Navigation uses `pages.SwitchToPage()`. Every page follows the same pattern: a content area on top, a `formDown` button bar (Back/Next/Quit) on bottom.

### Key files and their roles

| File | Responsibility |
|---|---|
| `main.go` | Entry point, config loading (`config.yaml`), dependency install (podman + kubespray-runtime image), page registration, Ctrl+C handler, `--config`/`--create` dispatch |
| `installConfig.go` | `--config` YAML schema, both `hosts` shapes, defaults, validation, `MarshalYAML` for generated files |
| `installPlan.go` | Pure planners: detailed-host inventory, extra-vars precedence, mirror rendering, sampled CIDR IPs |
| `configRun.go` | Headless setup for a config run: project preparation, conflict check, SSH staging, page prefill, auto-run exit/cleanup |
| `createConfig.go` | Pure `--create` exporter: inventory→detailed hosts, features/extra-vars classification, atomic 0600 YAML writer |
| `saveConfig.go` | `--create` final page: output path, SSH password or existing key, build/validate/write, overwrite confirmation |
| `setupMode.go` | Initial screen: "Create / Update Cluster" vs "Add Nodes To Cluster" |
| `project.go` | `Inventory` struct, project path management, load/save/populate inventory YAML, invokes `inventory_builder/inventory.py` to assign roles |
| `hosts.go` | Host list with add/remove, role auto-assignment for new hosts |
| `hostDetails.go` | Per-host editor: IP, access IP, groups (etcd/control-plane/node/calico_rr), node labels, node taints |
| `features.go` | Configurable feature toggles driven by `config.yaml` → `configurable_vars` (NTP, firewall rules, containerd registries, CSI snapshot controller, etc.) |
| `haMode.go` | HA mode: localhost load balancing vs kube-vip (with VIP input) |
| `network.go` | Service/Pod CIDR config, CIDR overlap check, sampled IP conflict detection via parallel ping |
| `mirror.go` | Public download mirror toggle (DaoCloud mirrors for China), modifies `group_vars/all/mirror.yml` |
| `deployCluster.go` | Final review: editable inventory + extra-vars YAML, SSH key creation/copy, key-login probe with sshd diagnosis before starting |
| `setupCluster.go` | Executes Ansible playbooks in the container, streams stdout to the TUI log view, shows elapsed timer. Success → green background, failure → red |
| `utils.go` | `execCommand`/`execCommandAndCheck` (runs commands in Podman container or on host), `checkPrivilege`, modal helpers, CIDR IP enumeration, parallel ping |

### Config-driven run (`--config`)

`k8s-installer-tui --config cluster.yaml` loads an installation file, fills the
same globals the pages read, and auto-runs the create-cluster pipeline. It is not
a second execution path: `populateInventory` (bare hosts), the SSH functions,
`execCmd`, and the log view are reused. `--config ... --dry-run` stops on the
Deploy page with Start disabled; it skips `initLog`, SSH, and Ansible.

`hosts` has two shapes. Bare entries (`ip`, `start-end`, `ip,access_ip`) go
through `inventory.py`, so roles are assigned exactly as the TUI does. Detailed
host objects carry explicit `roles`, which are used as written — a control-plane
host is not implicitly a `node`.

`runConfigDriven` (`configRun.go`) sets `autoRun`, prepares the project and
files, and queues `initFlexSetupCluster(true)` after `app.Run()` starts; the log
view's `app.Draw()` blocks until the event loop exists. In auto-run, `execCmd`
records the exit code, removes the container/password files, and calls
`finishAutoRun`, which stops the event loop; the process exits 0/1 in
`runConfigDriven` only after `Run` returns. Stopping first is what lets
`app.Stop` run tcell's `Fini` — exiting from the playbook goroutine left the
terminal in raw mode with mouse reporting on. Because `Fini` also clears the
screen, `runConfigDriven` prints `autoRunSummary` afterward: a result block with
the outcome, exit code, duration, project and log path, then — for a failure —
the hosts the last PLAY RECAP marks unreachable/failed, the last `fatal:` /
`ERROR!` line with its `msg` value, and the last 20 lines of the log. The Deploy
page reads
`prefillSSHPassword`/`prefillSudoPassword` and the `dryRun` global
(`deployCluster.go`). Config errors exit 2 before the wizard opens.

### Create mode (`--create`)

`k8s-installer-tui --create [-c out.yaml]` walks the wizard and writes an
install config for `--config`. In this mode `-c/--config` is the *output* path
(default `install.yaml`); it is dispatched before the `--config` branch in
`main.go`, so the file is never loaded. `createMode` is set before `initPages`
because Setup Mode disables its Add Nodes button. The preflight still runs:
creating a project runs `inventory.py` in the container, and loading copies the
sample inventory and reads the existing files.

The flow ends at Save Config instead of Deploy. `mirrorNextPage()` chooses the
destination; `networkConflictCheckEnabled()` keeps the Network checkbox's value
for the file but skips the sampled pings. No SSH function, key copy, or
playbook is called.

`buildInstallConfig` (`createConfig.go`) turns the live globals into the same
schema `--config` reads, then runs `applyDefaults` + `validate` before writing.
Hosts become detailed objects sorted by name with roles from group membership.
`hostname_prefix` is recovered from the host names (`onpremisis-` for
`onpremisis-1..3`) instead of the wizard's `node` default; names that share no
prefix keep the operator's value. A trailing hyphen is valid there because the
prefix is followed by a number (validation checks `prefix + "1"`).
Features are every key in `configurableVarTypes`; `extra_vars` keeps the
remainder after removing feature, network, HA (`applyHaMode`), mirror, and
unchanged `Default_vars` keys — `extra_vars` is merged last, so a leaked key
would override its structured section. The writer uses a temp file in the
destination directory, mode 0600, and an atomic rename. `MarshalYAML` on
`installHosts` emits either the bare or detailed shape, never the struct's
`auto`/`detailed` fields.

### Container execution model

Commands run inside a Podman container (`kubespray-runtime`) that mounts three directories:

- `appPath` → `/data/k8s-installer-tui` (binary, kubespray, ansible roles/playbooks)
- `projectPath` → `/data/idocluster` (inventory YAML, SSH keys, logs)
- `offlinePath` → `/data/k8s-installer-offline` (offline materials cache)

The `inContainer` constant in `main.go` controls this. Container mode is the only supported path.

One container carries every in-container command of a session. `ensureContainer()` (`utils.go`) makes it on demand — the first in-container command, since `projectPath` is only chosen on the Project page and is one of the mounts — and reuses it while it is running and was made with the same mounts; a stopped container, or one made for another project or image, is removed and replaced. What the mounts and image were is written to the container as the label `io.idocluster.spec`, and that label is what a later command compares, rather than parsing `podman inspect`'s binds. No command uses `--replace`, which deletes a container of the same name — one shared name with `--replace` is what let one SSH check destroy the container another check was working in. `removeLeftoverContainers()` sweeps what a crashed session left behind at startup, including an Ansible run nobody is watching.

Commands are built as argument vectors (`containerRunArgv`, `containerExecArgv`) and run with `podman exec`, not as shell strings, so no shell on the host parses them: a command may contain `$`, a backtick or a quote and still arrive as it was written. The SSH password goes through a file (`ansible-password`, `writeSshPassword`) because a password in an argument is visible in `ps`. The playbook run (`setupCluster.go`) uses the same container; the Abort button removes it, since killing the client only ends the view of a run that lives in the container.

A failed in-container command is reported by the line ssh wrote (`errorText`/`meaningfulLine` in `deployCluster.go`), never by the container runtime's own teardown error and never as a bare exit status; the command's whole output goes to the run log, and `recordCheck` logs every node's SSH check whether it passed or failed. A key-login probe only counts as a pass when the node reported its identity (`probeFailure`), so a node that cannot take the key cannot be waved through to the playbook. A failure that is the runtime's own (`isContainerReport`) is named as such — "The check did not run: the container it needed was gone." — instead of being blamed on the node or its password.

The SSH stages do not depend on an exit status to carry the answer: `runCheckStage` (deployCluster.go) makes each stage write its output into the project directory (`check-<node>-<stage>.out`, mounted, so it survives a container that goes away mid-command) and reads the answer from there, `runUntilAnswered` looks a second time when a stage produced no answer, and `inContainerExec` runs a command again against a fresh container when the runtime says the container was gone. The probe pins `LC_ALL=C` before `id`, because a Kylin node translates that output (`用户id=0(root)`) and the marker it is read for would be gone.

### Inventory data flow

1. User provides IPs → `inventory_builder/inventory.py` generates `hosts.yaml` with role assignments
2. Go `Inventory` struct (in `project.go`) mirrors the Kubespray inventory YAML schema
3. `extraVars` (`map[string]any`) holds Ansible extra vars, persisted as `extra-vars.yaml`
4. Both files are saved to `projectPath` (default: `~/idocluster/`)
5. Before execution, `ansible-roles/` and `ansible-playbooks/` are copied into the kubespray directory

### config.yaml

Drives the UI dynamically:
- `predefined_node_labels` / `predefined_node_taints` — dropdown options in the labels/taints editor
- `configurable_vars` — auto-generates form fields (checkbox, input, textarea) based on the value type
- `default_vars` — always-merged Ansible variables
- `default_mirrors` — registry mirror mappings
