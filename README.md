# claude2stream

Real-time web viewer for Claude Code conversations. Streams conversation data from `~/.claude` as durable streams and provides a web UI for browsing sessions.

## Quick Start

```bash
# Build and run
task run

# Or just build
task build
./claude2stream
```

Open http://localhost:8214/ui/ to view conversations.

## Usage

```
claude2stream [flags]

Flags:
  -addr string   listen address (default ":8214")
  -dir string    claude directory (default "~/.claude")
  -dev           enable CORS for development
```

## Development

```bash
# Terminal 1: Run Go backend with CORS
go run . -dev

# Terminal 2: Run Vite dev server
task dev
```

The Vite dev server runs on port 3000 and proxies API requests to the Go backend on port 8214.

## Building

Requires Go 1.21+ and pnpm.

```bash
task build          # Build frontend + binary
task clean          # Remove build artifacts
task release:snapshot  # Test goreleaser locally
```

## Architecture

- **Go backend**: Watches `~/.claude` for JSONL conversation files, exposes them as [durable streams](https://github.com/ahimsalabs/durable-streams-go)
- **SolidJS frontend**: Real-time conversation viewer with TanStack Router, served from `/ui/`
