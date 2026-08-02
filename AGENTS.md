# Repository Guidelines

## Project Structure & Module Organization

- `cmd/tg-search/` holds the main Go entrypoint.
- `internal/` contains backend packages such as `api`, `repository`, `telegram`, `search`, `task`, `config`, and `web`.
- `web/src/` contains the Vue 3 frontend, with `views/`, `components/`, `stores/`, `api/`, `router/`, and `utils/`.
- `web/src/test/setup.ts` configures frontend tests; `*.test.ts` files live beside the code they test.
- `docs/` contains API, deployment, architecture, product, and planning references.
- `scripts/` contains operational and packaging helpers.
- `docker/`, `Dockerfile`, and `compose.yaml` support containerized runs.
- `internal/web/dist/` is generated embedded frontend output; avoid direct edits.

## Build, Test, and Development Commands

- `GOCACHE=/tmp/go-build-cache go test ./...` runs all Go tests.
- `go build -o /tmp/tg-search ./cmd/tg-search` builds the backend binary.
- `npm install --prefix web` installs frontend dependencies.
- `npm run web:dev` starts the Vite admin shell at `http://127.0.0.1:5173`.
- `npm run web:test` runs frontend Vitest tests.
- `npm run web:typecheck` runs Vue/TypeScript type checks.
- `npm run web:build` builds the frontend bundle.
- `docker compose up -d` starts the containerized service using local `./data`.

## Coding Style & Naming Conventions

Use `gofmt` for Go files and keep packages aligned with existing `internal/` domain boundaries. Go package names are lowercase; tests use `*_test.go`.

Frontend code uses Vue SFCs, TypeScript, Pinia, Vue Router, Naive UI, and UnoCSS. Keep component filenames in PascalCase, for example `ResourceTable.vue`; stores and utilities use lower camel case, for example `resources.ts`. Prefer the `@/` alias for frontend imports.

## Testing Guidelines

Backend tests use Go's standard `testing` package. Place tests beside implementation files. Frontend tests use Vitest with `jsdom` and Vue Test Utils; colocate `*.test.ts` files with the related component, store, view, or utility.

Run Go and frontend checks before merging changes that touch both stacks:

```bash
GOCACHE=/tmp/go-build-cache go test ./...
npm run web:typecheck
npm run web:test
```

## Commit & Pull Request Guidelines

Recent history uses short conventional prefixes such as `feat:` and `fix:`; keep subjects imperative and scoped, for example `feat: add signed telegram media proxy`.

Pull requests should describe the change, list commands run, and link related issues or docs. Include screenshots for UI changes and note config, migration, Docker, or API contract impact.

## Security & Configuration Tips

Do not commit local runtime data from `data/`, Telegram credentials, API keys, session files, logs, backups, or generated secrets. Use local `config.yaml` for development and `/data/tg-search/config.yaml` in production.

## Release Policy Exception
1. Release/version publishing must not create a new worktree.
2. Releases should be performed directly from main or a dedicated release branch.
3. Release commits must be made without using task worktrees.
4. If a release branch is used, it should be lightweight and short-lived, and not tied to a worktree lifecycle.
5. After release, no additional cleanup of worktrees is required for the release process itself.
