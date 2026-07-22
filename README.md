# GitGate — Local Quality Gate for Git

> **Stop pushing broken code.** GitGate intercepts your `git push`, runs a configurable pipeline (rebase, AI review, tests, lint), and only forwards to the real remote when everything passes — then automatically opens a Pull Request.

[![Release](https://img.shields.io/github/v/release/jvrsantacruz/gitgate)](https://github.com/jvrsantacruz/gitgate/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8.svg)](https://go.dev)

---

## Why GitGate?

Most CI pipelines run _after_ you push. You wait for them, you get the failure notification, you fix it, you push again. **GitGate runs the pipeline _before_ your push reaches the remote** — locally, in isolation, with AI-assisted review — so your remote only ever receives code that has already passed.

```
Before GitGate:            With GitGate:
  git push origin main       git push gitgate main
       ↓                           ↓
  Remote (CI fails)      Local gate (rebase → AI review → test → lint)
       ↓                           ↓
  Fix & push again         Real remote (always green)
                                   ↓
                             PR auto-created
```

---

## Features

- 🔀 **Auto-rebase** onto the target branch before push
- 🤖 **AI code review** (Anthropic Claude, OpenAI, or local CLI agent)
- 🧪 **Auto-detect and run** tests (`go test`, `npm test`, `pytest`, ...)
- 🔍 **Auto-detect and run** linters (`golangci-lint`, `eslint`, `ruff`, ...)
- 🚀 **Safe push** — refuses to overwrite remote commits
- 📋 **Auto PR creation** via `gh` (GitHub) or `glab` (GitLab)
- 🖥️ **TUI** for interactive review of AI findings
- 🤖 **JSON output mode** for agent/automation workflows
- 💾 **SQLite-backed** state with crash recovery
- 🌐 **Cross-platform**: Linux, macOS, Windows

---

## Installation

### Quick install (Linux/macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/jvrsantacruz/gitgate/main/scripts/install.sh | sh
```

### From source

```sh
git clone https://github.com/jvrsantacruz/gitgate.git
cd gitgate
make install
```

### Verify

```sh
gitgate doctor
```

---

## Quick Start

```sh
# 1. Go to your existing Git repo (must have an 'origin' remote)
cd my-project

# 2. Initialize GitGate
gitgate init

# 3. Work normally, then push through the gate instead of directly to origin
git push gitgate main
```

That's it. GitGate will:
1. Rebase your branch onto `origin/main`
2. Run AI review on the diff
3. Run your tests
4. Run your linter
5. Push to `origin/main` if everything passes
6. Create or update a Pull Request

**To bypass the gate** (direct push without quality check):
```sh
git push origin main
```

---

## Configuration

GitGate reads `.gitgate.yml` from your repository root (created automatically by `gitgate init`):

```yaml
version: 1

pipeline:
  # Steps to skip entirely
  skip: []                  # e.g. [lint, review]
  max_parallel_runs: 2
  target_branch: main

review:
  provider: anthropic       # anthropic | openai | ollama | cli
  model: claude-opus-4-5
  fail_on: high             # critical | high | medium | low

test:
  # command: go test ./...  # auto-detected if not set
  timeout_secs: 300

lint:
  # command: golangci-lint run  # auto-detected if not set
  timeout_secs: 60

pr:
  draft: false
  labels: []
```

### Environment Variables

| Variable | Description |
|---|---|
| `ANTHROPIC_API_KEY` | API key for Anthropic Claude review |
| `OPENAI_API_KEY` | API key for OpenAI review |
| `GITGATE_INSTALL_DIR` | Override install directory |
| `GITGATE_CONFIG` | Override config file path |

---

## CLI Reference

```
gitgate init              # Set up GitGate for the current repo
gitgate doctor            # Check dependencies and config
gitgate status            # Show recent pipeline runs
gitgate daemon start      # Start background daemon
gitgate daemon stop       # Stop background daemon
gitgate daemon status     # Check daemon status
gitgate daemon logs       # View recent logs

# Automation/agent mode (JSON output)
gitgate run --intent "fix: resolve race condition" --json
gitgate respond --id <run-id> --action fix --json
```

---

## Architecture

```
git push gitgate <branch>
         │
         ▼
  ~/.gitgate/repos/<id>.git    (bare repo, your 'gitgate' remote)
         │
         ▼ post-receive hook
  gitgate daemon notify-push
         │
         ▼
  Daemon (goroutine pool, SQLite state)
         │
         ▼ git worktree add
  ~/.gitgate/worktrees/<run-id>/
         │
         ▼ Pipeline
  [intent] → [rebase] → [review] → [test] → [lint] → [push] → [pr]
         │
         ▼ (all pass)
  git push origin <branch>
  gh pr create --fill
```

### Directory Layout

```
~/.gitgate/
├── bin/                  # gitgate binary
├── repos/                # bare repo intermediaries
│   └── <id>.git/
│       ├── hooks/
│       │   └── post-receive
│       └── config
├── worktrees/            # ephemeral, cleaned after each run
│   └── <run-id>/
├── data/
│   └── gitgate.db        # SQLite state (runs, findings, logs)
└── daemon.log
```

---

## Development

```sh
# Clone
git clone https://github.com/jvrsantacruz/gitgate.git
cd gitgate

# Build
make build

# Run tests
make test

# Lint
make lint

# Format
make fmt

# Build all platforms
make build-all
```

### Project Structure

```
gitgate/
├── cmd/
│   └── gitgate/          # CLI entry point
├── internal/
│   ├── cli/              # Cobra commands
│   ├── config/           # Config loading & validation
│   ├── daemon/           # Daemon, SQLite, IPC, service management
│   ├── gitutil/          # Git operations
│   ├── pipeline/         # Pipeline engine (7 steps)
│   └── tui/              # Bubbletea TUI
├── scripts/
│   └── install.sh        # Installer
├── .github/
│   └── workflows/
│       └── release.yml   # Multi-platform CI/CD
├── .gitgate.yml          # Sample config
├── Makefile
└── README.md
```

---

## Roadmap

- [x] AI review integration (Anthropic, OpenAI, Ollama, local CLI agent)
- [x] `gitgate run` — manually trigger the pipeline with JSON output
- [x] `gitgate respond` — resolve findings (fix / skip / abort)
- [x] `gitgate update` — self-update from GitHub Releases
- [ ] Pause-and-resume pipeline for `ask-user` findings
- [ ] Enforce `max_parallel_runs` from config
- [ ] Full Bubbletea TUI with real-time step progress
- [ ] Auto-fix findings with AI assistance
- [ ] Support for monorepos (path-based routing)
- [ ] Slack/webhook notifications on pipeline result

---

## Contributing

Contributions are welcome! Please open an issue first to discuss what you'd like to change.

1. Fork the repository
2. Create your feature branch: `git checkout -b feat/amazing-feature`
3. Run tests: `make test`
4. Submit a Pull Request

---

## License

MIT — see [LICENSE](LICENSE).
