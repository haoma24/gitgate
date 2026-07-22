# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

GitGate is a Go CLI that inserts a **local quality gate between `git push` and the real remote**. Instead of pushing to `origin`, the user pushes to a `gitgate` remote (a bare repo on disk). A `post-receive` hook notifies a background daemon, which runs a pipeline (rebase → review → test → lint) in an isolated worktree and only forwards the push to the real `origin` — then opens a PR — if every step passes.

## Commands

The `Makefile` targets assume a POSIX shell (`mkdir -p`, `command -v`). On this Windows dev box, run `make` under Git Bash, or call `go` directly:

```sh
make build            # or: go build -o bin/gitgate.exe ./cmd/gitgate
make test             # go test -v -race -timeout 120s ./...
make test-short       # skips integration tests (go test -short)
make lint             # golangci-lint if present, else falls back to go vet
make fmt              # gofmt -s + goimports
make build-all        # cross-compile linux/darwin/windows

# Run a single test
go test ./internal/pipeline/ -run TestExecute
go test ./internal/daemon/  -run TestCreateRun -v
```

There is no golangci-lint config in the repo, so `make lint` degrades to `go vet` unless the tool is installed globally.

## Architecture

The flow spans three cooperating processes — the `git push` client, the CLI invoked by the git hook, and the long-running daemon — communicating through the filesystem and a local socket.

```
git push gitgate <branch>
  → ~/.gitgate/repos/<id>.git  (bare repo, added as the 'gitgate' remote by `gitgate init`)
  → post-receive hook runs `gitgate daemon notify-push` (internal, hidden command)
  → daemon.NotifyPush sends a PushEvent JSON over the IPC socket
  → daemon enqueues a Run (SQLite), a worker creates a detached worktree, runs the pipeline
  → on success: `git push origin` + `gh pr create` (or `glab mr create`)
```

Package map (`internal/`):

- **`cli/`** — Cobra commands wired in `root.go`. `init.go` does the one-time setup (bare repo, hook, `gitgate` remote, sample config). `daemon.go` manages the daemon and hosts the hidden `notify-push` command.
- **`daemon/`** — the core. `daemon.go` is the worker-pool orchestrator; `db.go` is the SQLite state store (`runs`, `findings`, `logs`); `service.go` installs/controls the daemon as an OS service. IPC and process control are split by build tag (`*_unix.go` / `*_windows.go`).
- **`pipeline/`** — `pipeline.go` runs the 7 ordered steps in `AllSteps` (`intent, rebase, review, test, lint, push, pr`). Steps run sequentially; a `fail` or `waiting` status stops the pipeline. Test/lint commands come from `.gitgate.yml` if set, else are **auto-detected** from marker files (`go.mod`, `package.json`, etc.) in `detectTestCommand`/`detectLintCommand`.
- **`review/`** — the AI review backend. `NewProvider` selects an implementation from `review.Options` (`anthropic`, `openai`, `ollama` over HTTP, or `cli` shelling out to a local agent). All use stdlib `net/http` — **do not add provider SDKs** (keeps deps minimal and CGO-free). Providers return `[]review.Finding` parsed from the model's JSON output by `parseFindings`; `MeetsThreshold` applies the `fail_on` gate. A missing API key returns `ErrNoAPIKey`, which the pipeline treats as *skip*, not *fail*.
- **`gitutil/`** — all git shell-outs plus path helpers. `GitGateHome()` = `~/.gitgate` and defines the on-disk layout (`repos/`, `worktrees/`, `data/gitgate.db`, `daemon.log`).
- **`config/`** — `.gitgate.yml` schema, defaults, sample-file writer, and `LoadOrDefault` (used on the daemon hot path).

### On-disk state (`~/.gitgate/`)

`repos/<id>.git/` (bare intermediaries; `<id>` = sha256 of the origin URL), `worktrees/<run-id>/` (ephemeral, removed after each run and pruned on daemon restart), `data/gitgate.db` (SQLite, WAL mode), `daemon.sock`, `daemon.pid`, `daemon.log`.

### Cross-platform structure

Anything OS-specific is separated by Go build tags, not runtime branches: IPC is a Unix domain socket (`ipc_unix.go`) vs. a named pipe (`ipc_windows.go`); the daemon is supervised by launchd (macOS), systemd user service (Linux), or Task Scheduler / detached process (Windows) — see the `ServiceManager` implementations in `service.go`. When touching daemon lifecycle, IPC, or process signalling, change **all** platform variants together.

### SQLite driver

Uses `modernc.org/sqlite` — a **pure-Go, CGO-free** driver. Do not introduce `mattn/go-sqlite3` or anything requiring CGO; the cross-compilation in `build-all` depends on staying CGO-free.

## How config flows into a run (the important wiring)

`.gitgate.yml` **is** consumed at runtime, but note *where*: `daemon.processRun` loads it from the **worktree** (the pushed code) via `config.LoadOrDefault`, then maps it into `pipeline.Config` (skip list, target branch, test/lint command + timeout, and `review.Options`). So the config that governs a push is the committed `.gitgate.yml` at that SHA — not whatever is in the user's working tree.

`gitgate run --intent/--skip` can't travel through the git push protocol, so they are stashed as one-shot `gitgate.pending-intent` / `gitgate.pending-skip` keys in the **bare repo's** git config and consumed by the daemon (`EnqueuePush` / `processRun`) on the next push, then cleared.

`gitgate init` adds the real remote (e.g. `origin`) *inside the bare repo* via `gitutil.EnsureRemote`, because pipeline worktrees share the bare repo's config and the rebase/push steps run `git fetch`/`git push <origin>` there. Without it those steps have nowhere to go.

## Known limitations / not yet built

- **Review is synchronous.** `fail_on` decides pass/fail; there is no *pause-and-resume* on `ask-user` findings. The `waiting` run status and the pipeline's `waiting` branch exist but nothing currently produces them, so a resumable pipeline is still a future addition.
- **`gitgate respond`** therefore manages finding bookkeeping (mark a finding `applied`/`skipped`, or `abort` a run) — it does **not** resume a paused pipeline.
- **`max_parallel_runs`** in config is not yet enforced (the daemon uses a fixed 2-worker pool in `daemon.New`).
- **`gitgate update`** replaces the binary from GitHub Releases; on Windows the old binary is renamed aside and may linger until reboot.

## Conventions

- `go.mod` declares `go 1.26.4`. Dependencies are currently all marked `// indirect`; run `go mod tidy` after adding a direct import so the graph stays accurate.
- Pipeline steps return a `*StepResult` with a string `Status` (`"pass"`, `"fail"`, `"skip"`, `"waiting"`) — not an error-only convention. Follow that shape when adding steps, and register new steps in both the `steps` map (`New`) and the ordered `AllSteps` slice.
- The post-receive hook always `exit 0` and calls `notify-push` in the background, so a broken daemon never blocks the user's push. Keep that non-blocking guarantee if you edit `buildPostReceiveHook`.
