# Fresh-Machine Setup Prerequisites

This document describes the environment needed **before implementation begins**. It deliberately avoids hard-pinning patch versions so the setup does not become stale.

## Required host software

### Windows development environment

Recommended:

- Windows 11 or a current supported Windows release
- WSL2 with Ubuntu
- Docker Desktop using the WSL2 backend
- VS Code
- Git for Windows
- Go (current stable release)
- Node.js LTS
- GitHub CLI
- Bruno or Postman
- k6

### Coding assistants

- Claude Code, authenticated with the user's Claude plan/account
- OpenAI Codex, authenticated with the user's ChatGPT/Codex access

An OpenAI or Anthropic API key is **not required merely to use the corresponding coding assistant through a supported subscription login**. Only create API keys later if the project itself needs to call an API.

## Required host verification

PowerShell:

```powershell
git --version
node --version
npm --version
go version
docker --version
docker compose version
gh --version
```

WSL/Ubuntu:

```bash
uname -a
git --version
go version
docker --version
```

Docker smoke test:

```bash
docker run --rm hello-world
```

GitHub authentication:

```bash
gh auth status
```

Claude Code:

```bash
claude doctor
```

Codex:

```bash
codex
```

## Do not install these services natively

Run these with Docker Compose instead:

- PostgreSQL
- Redis
- Prometheus
- Grafana
- local target-service simulator

This keeps the development environment reproducible and easy for an AI coding agent to recreate.

## Accounts to prepare

Required:

- GitHub account
- Claude account with the intended Claude plan
- ChatGPT account with the intended Codex access

Optional, only when deployment starts:

- cloud provider account
- Docker image registry account
- domain/DNS account

Do not create or place cloud credentials in the repository before they are actually needed.

## Local ports

Reserve free local ports for:

- API server
- PostgreSQL
- Redis
- Prometheus
- Grafana
- target-service

Exact ports should be configurable through environment variables and documented in `.env.example` rather than hard-coded throughout the application.

## Environment variables

Never commit `.env`. Use `.env.example` as the template.

At minimum the eventual application configuration will need values for:

- PostgreSQL connection
- Redis connection
- HTTP listen address/port
- delivery timeouts
- retry policy
- circuit-breaker settings
- payload size limit
- API authentication settings for non-local environments

## Pre-handoff repository check

Before asking an AI coding assistant to implement:

```bash
git status
git branch --show-current
git log -1 --oneline
```

The repository should have a clean baseline commit containing the handoff documents.
