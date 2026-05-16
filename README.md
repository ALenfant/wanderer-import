# wanderer-import

`wanderer-import` is a Go CLI for importing trail routes from local files,
direct trail-file URLs, and supported outdoor websites into
[Wanderer](https://github.com/open-wanderer/wanderer/) through the
[Wanderer API](https://wanderer.to/develop/api/).

The project is in early MVP state. The current implementation can upload local
or direct remote trail files, scrape pages with direct trail-file links, and use
several provider-specific APIs/export routes. Website-specific importers are
tracked in [PROVIDERS.md](PROVIDERS.md); the provider architecture is documented
in [ARCHITECTURE.md](ARCHITECTURE.md).

## Current Status

- Implemented: plugin-style provider registry with scored auto-detection.
- Implemented: `direct`, `gpx-link-scraper`, Geotrek, Cirkwi, VisuGPX, Komoot,
  SityTrail, AltitudeRando, Bergfex, Helloways, Decathlon Outdoor, AllTrails
  saved v3 JSON, Trails Viewer API decoding, and several tourism-site wrappers.
- Implemented: manifest imports from JSON, dry-run mode, JSON output,
  metadata extraction, remote photo upload, source URL handling, public/private
  flag, duplicate handling, source-based update mode, and trail metadata
  overrides.
- Implemented: Wanderer authentication using API token, PocketBase auth cookie,
  or username/password login.
- Implemented: source-site session options using Netscape `cookies.txt` files,
  direct browser cookie-store extraction, User-Agent/Referer overrides, and a
  Chrome-like header profile for providers that need a logged-in or more
  browser-like request context.
- Implemented: optional Playwright browser fallback for providers that need a
  real same-origin browser request after the normal HTTP fetch fails.
- Still planned/deferred: more robust JavaScript payload extraction and
  candidate providers listed in [PROVIDERS.md](PROVIDERS.md).

## Build

```bash
go test ./...
go build -o bin/wanderer-import ./cmd/wanderer-import
```

Run without building:

```bash
go run ./cmd/wanderer-import --help
```

## Configuration

The CLI needs the Wanderer base URL plus one authentication method.

```bash
export WANDERER_URL="https://wanderer.example.com"
export WANDERER_API_TOKEN="..."
```

Supported authentication options:

- `--api-token` or `WANDERER_API_TOKEN`
- `--pb-auth-cookie` or `WANDERER_PB_AUTH`
- `--username` and `--password`

Use `--wanderer-url` or `WANDERER_URL` to point at the Wanderer instance. If no
base URL is provided, the CLI defaults to `http://localhost:3000`.

## Source Website Sessions

Wanderer authentication is separate from source-website authentication. For
providers such as AllTrails or other sites that need a browser session, the CLI
can read cookies directly from a local browser store:

```bash
wanderer-import export \
  --cookies-from-browser chrome \
  --impersonate chrome \
  https://www.alltrails.com/en-gb/trail/france/gard/sumene-ranc-de-banes-pont-des-chevres
```

You can also export cookies from your browser in Netscape `cookies.txt` format
and pass them explicitly:

```bash
wanderer-import export \
  --cookies cookies.txt \
  --impersonate chrome \
  https://www.alltrails.com/en-gb/trail/france/gard/sumene-ranc-de-banes-pont-des-chevres
```

Session-related flags are available on both `import` and `export`:

- `--cookies PATH` or `WANDERER_IMPORT_COOKIES`: load Netscape-format source
  website cookies.
- `--cookies-from-browser BROWSER[:PROFILE]` or
  `WANDERER_IMPORT_COOKIES_FROM_BROWSER`: load source website cookies from a
  local browser store. Supported browser names are `all`, `brave`, `chrome`,
  `chromium`, `edge`, `firefox`, `opera`, and `safari`.
- `--user-agent VALUE` or `WANDERER_IMPORT_USER_AGENT`: override the
  User-Agent sent to source websites.
- `--referer URL` or `WANDERER_IMPORT_REFERER`: override the Referer sent to
  source websites.

### Third-Party Credentials

Some providers use hardcoded API keys or secrets to access their respective
platforms. You can override these via environment variables (recommended for
local development, often managed via `direnv` and a `.env` file):

1. **Install direnv**: `brew install direnv` (or your platform's equivalent).
2. **Setup .env**: Copy the template with `cp .env.example .env` and fill in your keys.
3. **Allow direnv**: Run `direnv allow` in the project root.

Available variables:
- `ALLTRAILS_API_KEY`: API key for AllTrails v3 API.
- `WIKILOC_API_KEY`: API key for Wikiloc mobile API.
- `WIKILOC_SECRET_KEY`: Signing key for Wikiloc mobile API requests.
- `SITYTRAIL_TOKEN`: Access token for SityTrail API.

If these are missing, the CLI will log a warning and attempt to use alternative
methods that do not require secrets (such as browser-based extraction or HTML
parsing) if available.
- `--impersonate chrome`, `--impersonate firefox`, or `--impersonate safari`:
  add browser-like User-Agent and request headers. When omitted, the CLI picks a
  matching header profile for `--cookies-from-browser chrome`, `firefox`, or
  `safari`. This is header impersonation only; it does not change TLS/browser
  fingerprinting.

`--cookies-from-browser` uses
[`github.com/browserutils/kooky`](https://github.com/browserutils/kooky) to read
browser stores. If the browser has the cookie database locked or the operating
system denies keychain access, close the browser or fall back to an exported
`cookies.txt` file.

For providers that require JavaScript-created same-origin browser state, enable
the Playwright fallback:

```bash
wanderer-import export \
  --browser-fetch chrome \
  --browser-fetch-headful \
  https://fr-fr.trails-viewer.com/trail-fd5kl/Gourgas-Cirque-du-Bout-du-Monde/
```

`--browser-fetch BROWSER` or `WANDERER_IMPORT_BROWSER_FETCH` is available on
both `import` and `export`. Supported values are `chromium`, `chrome`,
`firefox`, and `webkit`. The provider still tries the normal Go HTTP path first;
Playwright is only used as a fallback for providers that opt into it, currently
Trails Viewer when its points API returns a browser-session status such as
`403`. Add `--browser-fetch-headful` when a site rejects headless Playwright;
for example, Trails Viewer accepted a visible Chrome fallback in testing while
headless Chromium still returned `403`.

Install Playwright's driver and browser binaries before using this option:

```bash
go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install
```

## Import a Local File

```bash
wanderer-import import \
  --wanderer-url "$WANDERER_URL" \
  --api-token "$WANDERER_API_TOKEN" \
  --name "Morning loop" \
  --difficulty moderate \
  ./morning-loop.gpx
```

## Import a Direct URL

```bash
wanderer-import import \
  --wanderer-url "$WANDERER_URL" \
  --api-token "$WANDERER_API_TOKEN" \
  --name "Downloaded trail" \
  https://example.com/trails/download.gpx
```

The `direct` provider currently accepts local files or URLs ending in supported
trail-file extensions such as `.gpx`, `.kml`, `.kmz`, `.fit`, `.tcx`, or `.geojson`.

## List Providers

```bash
wanderer-import providers
```

Provider IDs can be passed with `--provider`. Use `auto` to let the registry
choose the highest-scoring match.

## Dry Run

Use dry-run mode to verify provider selection and metadata without uploading to
Wanderer.

```bash
wanderer-import import --dry-run --json --name "Morning loop" ./morning-loop.gpx
```

## Manifest Imports

For batches, pass a JSON manifest with `--manifest`.

```bash
wanderer-import import --manifest imports.json
```

Accepted shapes:

```json
{
  "imports": [
    {
      "source": "./morning-loop.gpx",
      "name": "Morning loop",
      "description": "Short loop before work",
      "difficulty": "moderate",
      "public": false,
      "ignoreDuplicates": true,
      "updateExisting": true,
      "distance": 8200,
      "elevation_gain": 140,
      "elevation_loss": 140,
      "duration": 5400,
      "photo_urls": ["https://example.com/trails/morning-loop.jpg"]
    }
  ]
}
```

The manifest can also be a plain array of import objects or a single import
object. Supported difficulty values are `easy`, `moderate`, and `difficult`.
Command-line metadata is used as a default and can be overridden per manifest
entry.

## Batch Import From A URL File

For a plain text file with one URL or path per line, use `--sources`. Blank
lines and lines beginning with `#` are ignored.

```bash
wanderer-import import \
  --sources hike_urls.txt \
  --wanderer-url "$WANDERER_URL" \
  --api-token "$WANDERER_API_TOKEN" \
  --ignore-duplicates
```

You can combine `--sources` with positional sources and with `--manifest`; all
entries are imported in one run. Import continues by default when an individual
source fails and prints a warning such as `warning: failed to import ...`. Use
`--fail-fast` to restore stop-on-first-error behavior.

## Deduplicate Or Update Existing Imports

Every import appends a stable marker block to the trail description:

```text
Imported by wanderer-import
wanderer-import-source: https://example.com/source-page
wanderer-import-provider: provider-id
```

Use `--update-existing` to scan existing Wanderer trails for that source marker
before uploading. When a match is found, the CLI updates metadata and uploads
new photos on the existing trail instead of creating another trail.
If the provider has no description to apply, the existing description is
preserved and only the import marker is added when missing.

```bash
wanderer-import import \
  --sources hike_urls.txt \
  --wanderer-url "$WANDERER_URL" \
  --api-token "$WANDERER_API_TOKEN" \
  --update-existing
```

The update mode also recognizes the older `Source: ...` description line used
by previous versions.

If Wanderer rejects an upload with `Duplicate trail`, the importer now performs
a conservative metadata lookup even without `--update-existing`. It scans
existing trails for matching route-derived values such as distance and start
coordinate, using name, location, elevation, and duration as supporting signals.
When a match is found, the importer updates that existing trail and adds the
source marker so future runs can match it directly. It does not replace the GPX
file of an existing trail; when the route geometry itself changes, create a new
import or delete the old trail in Wanderer first.

## Provider Model

Providers resolve an input source into a trail file plus metadata. The resolved
trail is then uploaded to Wanderer through the API. File and provider metadata
can include name, description, location, date, difficulty, category,
public/private state, start coordinates, distance, elevation gain/loss,
duration, thumbnail index, source photo URLs, route step descriptions, and
tags. Geotrek-backed providers read public API detail endpoints when available,
so ordered itinerary steps and official attachment photos are preserved in the
metadata instead of relying only on page summaries. Extracted activity labels
such as `Hiking` and `Biking` are resolved to
Wanderer category IDs through `/category` before metadata updates are sent.
Free-form extracted tags are preserved in JSON/export output, but are not sent
to Wanderer as tag updates because the API expects tag relation ids.
Wanderer does not currently expose a dedicated source URL field in the update
API, so imports append the `wanderer-import-source: ...` marker to the trail
description. This marker is also the deduplication key used by
`--update-existing`. The CLI does not currently create a native Wanderer tag for
tool imports because the documented update API expects tag relation ids rather
than free-form tag names.
Photo URL extraction reads structured image metadata, JSON-LD, page galleries,
lazy-loaded image attributes, `srcset`, and embedded image URLs while filtering
obvious UI assets such as logos, icons, labels, and menu graphics.

Provider code is split between thin website plugins and shared engines:

1. Keep one provider identity per website or source.
2. Reuse shared engines for common behavior such as GPX link extraction,
   Geotrek downloads, URL-template exports, JSON coordinate APIs, and GPX
   generation.
3. Prefer existing Go libraries for HTML parsing, GPX generation, GeoJSON, and
   WKT parsing.
4. Prefer user-supplied browser cookies over embedding provider login flows.
   Use `--browser-fetch` only for protected same-origin API calls where a normal
   HTTP request has already failed.

See [PROVIDERS.md](PROVIDERS.md) for the provider audit from the ChatGPT shared
conversation and the current implementation plan. See
[ARCHITECTURE.md](ARCHITECTURE.md) for the plugin/engine layout.

## AllTrails

AllTrails public pages currently return DataDome bot protection to automated
CLI and Chrome DevTools fetches before route data loads. The `alltrails`
provider therefore supports the stable data artifact instead: a saved
`/api/alltrails/v3/trails/...` JSON response copied from a browser session where
you have legitimate access.

```bash
wanderer-import import \
  --provider alltrails \
  --wanderer-url "$WANDERER_URL" \
  --api-token "$WANDERER_API_TOKEN" \
  ./alltrails-trail.json
```

The provider reads the encoded route polyline from the v3 JSON response,
generates GPX, and preserves available name, description, category, distance,
and start coordinates.

You can also try source-session flags with AllTrails page or API URLs:

```bash
wanderer-import export \
  --provider alltrails \
  --cookies-from-browser firefox \
  --impersonate firefox \
  "https://www.alltrails.com/api/alltrails/v3/trails/..."
```

This can supply your logged-in cookies, but it is still a normal Go HTTP client,
not a real browser. DataDome or similar systems may continue to block it.

## Trails Viewer Browser Fallback

Trails Viewer pages expose metadata in HTML, but some route point API calls
return `403` unless made from inside a loaded browser page. The provider first
tries its normal HTTP request with the page-derived token. If that fails with a
browser-session status and `--browser-fetch` is enabled, it opens the source
page with Playwright and runs the points API `fetch` from that same-origin page
context.

```bash
wanderer-import export \
  --provider trails-viewer \
  --browser-fetch chrome \
  --browser-fetch-headful \
  https://fr-fr.trails-viewer.com/trail-fd5kl/Gourgas-Cirque-du-Bout-du-Monde/
```

## Wanderer API Notes

The current implementation targets the documented Wanderer API shape around:

- `POST /api/v1/auth/login`
- `PUT /api/v1/trail/upload`
- `GET /api/v1/category`
- `GET /api/v1/trail`
- `POST /api/v1/trail/form/{id}`
- `POST /api/v1/trail/{id}`
- `POST /api/v1/trail/{id}/file`

Trail upload is multipart-based. Metadata updates are sent after upload when
needed. The client tries the form endpoint first and falls back to the JSON
endpoint when the form endpoint is unavailable or returns a compatibility error.
Remote photos discovered from source metadata or supplied with `--photo-url` /
`photo_urls` are downloaded and uploaded through the trail file endpoint after
the trail exists. Photo upload failures are reported as warnings so a usable
trail import is not discarded because a source image is unavailable.
With `--update-existing`, the client lists trails and looks for the
`wanderer-import-source` marker in descriptions before upload.

## Development

```bash
go test ./...
gofmt -w ./cmd ./internal
```

When adding or changing providers, update [README.md](README.md),
[PROVIDERS.md](PROVIDERS.md), and [ARCHITECTURE.md](ARCHITECTURE.md) when the
provider interface or engine layout changes. Repo-local agent guidance is in
[AGENTS.md](AGENTS.md).
