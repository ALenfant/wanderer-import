# Agent Instructions

This repository is a Go CLI for importing trail routes into Wanderer. Keep
changes small, provider-focused, and aligned with the existing `cmd/` and
`internal/` layout.

## Documentation Requirements

Update documentation as part of the same change whenever behavior changes.

- Update `README.md` when CLI flags, environment variables, manifest schema,
  build/test commands, authentication behavior, or high-level project status
  changes.
- Update `PROVIDERS.md` when provider support changes, a provider is added or
  removed, a provider status changes, or new evidence changes whether a site
  exposes GPX/structured route data.
- Update `ARCHITECTURE.md` when provider interfaces, registry behavior, shared
  engines, or dependency strategy changes.
- Keep provider status words consistent with `PROVIDERS.md`: `Implemented`,
  `Planned`, `Candidate`, `Deferred`, and `Out of scope`.
- If a provider implementation depends on a discovered endpoint, include a
  representative evidence URL in `PROVIDERS.md`.
- Do not leave provider code and provider documentation out of sync.

## Development Workflow

- Prefer `rg`/`rg --files` for searching.
- Use `gofmt` on touched Go files.
- Run `go test ./...` after Go code changes.
- Prefer existing libraries for established formats and protocols before writing
  custom parsers.
- For docs-only changes, read the changed docs back before finishing.
- Do not commit unless the user explicitly asks.
- Do not revert unrelated user changes.

## Provider Checklist

When adding a provider:

- Add provider code under `internal/providers/<provider-id>/` or follow the
  closest existing provider pattern.
- Reuse an engine under `internal/providers/engines/` whenever the provider
  shares behavior with existing sources.
- Match only the intended domains and URL shapes.
- Prefer official exports or direct GPX/KML/GeoJSON links over reconstructing
  route data.
- If only coordinates are available, convert them to a standards-compliant GPX
  file before upload.
- Avoid auth-gated scraping and anti-bot workarounds unless the user explicitly
  approves that scope.
- Add focused tests for URL matching, metadata extraction, and error handling.
- Update `README.md` and `PROVIDERS.md` in the same change.

## Wanderer API Notes

Keep API behavior conservative. The CLI currently uploads a trail file first and
then applies metadata updates when needed. If Wanderer API endpoints or payloads
change, update the API notes in `README.md` and any affected tests.

Batch imports continue after per-source failures by default and emit warnings.
Preserve that behavior unless a change explicitly targets `--fail-fast`.
