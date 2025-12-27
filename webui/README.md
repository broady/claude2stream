# claude2stream Web UI

SolidJS frontend for viewing Claude Code conversations in real-time.

## Development

```bash
pnpm install
pnpm dev
```

Runs on http://localhost:3000/ui/. Proxies API requests to the Go backend at port 8214.

## Build

```bash
pnpm build
```

Output goes to `dist/`, which gets embedded into the Go binary.

## Stack

- SolidJS
- TanStack Router (basepath: `/ui`)
- Tailwind CSS v4
- Vite
