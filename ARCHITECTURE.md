# Architecture

`wanderer-import` uses one provider identity per source, backed by shared engines
for the behavior that many sources have in common.

The goal is to keep provider selection explicit and understandable while
avoiding duplicated scraping, download, coordinate parsing, and GPX generation
logic.

## Layers

```text
cmd/wanderer-import
  -> internal/cli
     -> internal/session
     -> internal/importer
        -> internal/providers
           -> provider plugins
           -> shared engines
           -> providerkit helpers
     -> internal/wanderer
```

## Import Flow

1. The CLI turns flags or manifest entries into `importer.Spec`.
2. The CLI builds one optional source-site HTTP client from session flags and
   passes it to the built-in provider registry.
3. The importer asks the provider registry to select a provider.
4. The registry uses explicit `--provider` when supplied, otherwise it chooses
   the highest-scoring provider match.
5. The selected provider resolves the source into an `importer.ResolvedTrail`.
6. When `UpdateExisting` is enabled and the Wanderer client supports source
   lookup, the importer searches existing trails for the source marker.
7. If a matching trail exists, the importer updates that trail's metadata and
   photos.
8. Otherwise, the importer uploads the resolved file to Wanderer.
9. If Wanderer rejects the upload as a duplicate, the importer asks the client
   to perform a conservative duplicate lookup using route-derived metadata. A
   matched duplicate is updated and marked with the current source URL.
10. The importer applies API-sendable metadata updates when needed.
11. The importer downloads and uploads source photos when photo URLs are
   available.

Metadata is merged in layers: route-file-derived metadata first, provider/API
or source-page metadata second, and explicit CLI or manifest values last.
HTML metadata extraction also recognizes Herault/Tourinsoft-style
`DsioDetail.initialize(...)` payloads, including activity labels and
`tracegps.balises` step-by-step route descriptions. Activity labels are stored
as category names in provider metadata; the Wanderer client resolves those names
to `/category` record IDs before sending an update.
After metadata merge, the importer appends a stable description marker because
the documented Wanderer update API has no dedicated source URL field:

```text
Imported by wanderer-import
wanderer-import-source: <source>
wanderer-import-provider: <provider-id>
```

The marker is also the deduplication key for update mode. The source lookup
scans the Wanderer trail list and matches either `wanderer-import-source:
<source>` or the older `Source: <source>` line used by earlier versions.
When the Wanderer upload endpoint returns a duplicate error before a source
marker exists, the client scans existing trails and only accepts a duplicate
candidate when route-derived metadata aligns conservatively: distance and start
coordinate are primary signals, with name, location, elevation, and duration as
supporting signals. The fallback updates metadata/photos but does not replace
the existing trail file.
Extracted tags are kept in exported JSON sidecars, but are not sent as Wanderer
tag updates because the Wanderer API expects tag relation ids rather than
free-form label text. For the same reason, the tool import marker is stored in
the description rather than as a native Wanderer tag.
Extracted photo URLs are also kept as metadata; on import the Wanderer client
uploads them through `/trail/{id}/file` after the trail has been created. Photo
upload failures are warnings, not fatal import errors.
Photo extraction intentionally combines structured metadata with gallery-style
HTML discovery, but filters obvious UI assets before upload.

## Source HTTP Sessions

Source-site authentication is handled below the provider layer by
`internal/session`. The CLI constructs a single optional `*http.Client` from
session flags and gives it to every built-in provider:

- `--cookies` loads Netscape `cookies.txt` files into a standard Go cookie jar.
- `--cookies-from-browser BROWSER[:PROFILE]` uses
  `github.com/browserutils/kooky` to read valid cookies from a local browser
  store and copy them into the same jar.
- `--user-agent` and `--referer` override request headers for source websites.
- `--impersonate chrome`, `--impersonate firefox`, and `--impersonate safari`
  apply browser-like User-Agent and request headers. If impersonation is not set
  explicitly, the session layer selects a matching profile for browser cookies
  loaded from Chrome-like browsers, Firefox, or Safari.

This design is intentionally provider-agnostic. AllTrails, Trails Viewer,
generic GPX scraping, and tourism wrappers can use the same logged-in/session
client without duplicating cookie parsing. Header impersonation is not a full
browser transport: it does not execute JavaScript and does not change the Go
TLS fingerprint.

## Browser Fetch Fallback

Some providers expose route geometry only to a real same-origin browser page.
The optional `--browser-fetch` path is handled by `internal/browserfetch` and is
passed into built-in providers through `providers.BuiltinsWithOptions`.

The contract is deliberately narrow:

```go
type Fetcher interface {
    Fetch(ctx context.Context, pageURL, requestURL string, opts RequestOptions) ([]byte, error)
}

type RequestOptions struct {
    Headers map[string]string
    Cookies map[string]string
}
```

Providers still perform their normal HTTP fetch first. They should call the
browser fetcher only when the normal path fails with a status that indicates a
browser/session requirement, for example `403` from a protected same-origin API.
The browser fetcher loads `pageURL`, then evaluates `fetch(requestURL, {
credentials: "same-origin" })` inside that page context and returns the response
body to the provider's existing parser. Provider-specific headers and
first-party cookies can be supplied through `RequestOptions`.

The current implementation uses
`github.com/playwright-community/playwright-go`. The fetcher is shared for a CLI
run and lazily launches the selected browser (`chromium`, `chrome`, `firefox`,
or `webkit`) on the first fallback request. It runs headless by default, with
`--browser-fetch-headful` available when a provider rejects headless browser
automation. Browser binaries are an explicit user setup step via the Playwright
installer; normal imports do not start a browser.

## Provider Plugins

Provider plugins are thin packages under `internal/providers/<provider>/`.

Each plugin owns:

- provider ID
- domain matching
- URL/ID extraction quirks
- engine configuration

Plugins should not duplicate common parsing or download behavior. They should
configure an engine whenever a reusable engine fits.

Example:

```go
func New(httpClient *http.Client) importer.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "herault-tourisme",
		Name:               "Herault Tourisme",
		Domains:            []string{"herault-tourisme.com"},
		AllowExternalLinks: true,
		Score:              85,
	}, httpClient)
}
```

## Provider Interface

Providers implement:

```go
type Provider interface {
	Name() string
	Match(source string) Match
	Resolve(ctx context.Context, spec Spec) (*ResolvedTrail, error)
}
```

`Match` is scored so specific providers beat generic providers:

```go
type Match struct {
	OK     bool
	Score  int
	Reason string
}
```

Typical score bands:

- `90`: specific provider with stable export/API logic
- `85`: specific provider using generic page extraction
- `50`: generic GPX/KML/GeoJSON link scraper
- `20`: direct local file or direct trail-file URL

Explicit provider selection bypasses scoring:

```bash
wanderer-import import --provider komoot https://www.komoot.com/tour/38639433
```

## Shared Engines

Shared engines live under `internal/providers/engines/`.

Current engines:

- `directfile`: local files and direct trail-file URLs, with metadata extracted
  from GPX, KML/KMZ, or GeoJSON when possible.
- `gpxlinks`: HTML page fetch plus direct GPX/KML/KMZ/GeoJSON link discovery,
  merging page metadata, structured image URLs, body-description fallbacks, and
  route-file metadata.
- `geotrek`: Geotrek route ID extraction, public API v2 detail metadata
  extraction, GPX candidate URLs, and GPX-link fallback. When a page exposes an
  API base, the engine fetches route descriptions, ordered itinerary steps,
  photos, distance/elevation/duration, and difficulty from the detail endpoint.
- `urltemplate`: providers where a route ID maps to one or more export URL
  templates.
- `jsonpoints`: JSON APIs that expose route coordinates, converted to GPX, with
  point-derived distance/elevation statistics and optional provider metadata
  parsers.
- `pagejson`: pages that link to a JSON trace file, converted to GPX.

When adding a provider, first check whether one of these engines can express the
provider with configuration only. Add a new engine only when at least two
providers plausibly share that behavior or when an implementation is large
enough to deserve isolation.

Some providers can compose an engine instead of returning it directly. For
example, Decathlon Outdoor direct route pages use the `jsonpoints` engine, while
Decathlon inspiration pages first resolve the first embedded `/explore/...`
route link and then delegate to the same route provider. Grand Pic Saint-Loup
performs the site's session redirect handshake before delegating to `gpxlinks`.
Trails Viewer is source-specific because its public point API uses shifted
base36 encoded coordinates rather than a standard JSON geometry format.

## Existing Libraries

Prefer maintained libraries for established formats:

- `golang.org/x/net/html` for HTML tokenization.
- `github.com/tkrajina/gpxgo/gpx` for GPX generation.
- `github.com/paulmach/orb/geojson` for standard GeoJSON parsing.
- `github.com/paulmach/orb/encoding/wkt` for WKT validation/parsing when the
  source format is compatible.
- `github.com/browserutils/kooky` for direct browser cookie-store discovery and
  decryption where the operating system permits it.
- `github.com/playwright-community/playwright-go` for opt-in browser-backed
  same-origin fetches when provider APIs reject plain HTTP clients.

Small source-specific cleanup is still acceptable around those libraries. For
example, SityTrail exposes 3D WKT without the `Z` marker, so the parser normalizes
the line to 2D WKT for library validation while preserving elevation values.

Wanderer difficulty values are defined centrally in `internal/wanderer` as
`easy`, `moderate`, and `difficult`. Providers should normalize source labels
to those constants before setting metadata.

## Current Built-In Providers

Built-ins are registered in `internal/providers/builtins.go`.

Implemented provider IDs:

- `direct`
- `gpx-link-scraper`
- `geotrek-gard`
- `cirkwi`
- `visugpx`
- `komoot`
- `sitytrail`
- `altituderando`
- `bergfex`
- `trails-viewer`
- `herault-tourisme`
- `montpellier-tourisme`
- `tourismegard`
- `tourisme-aveyron`
- `cevennes-tourisme`
- `grandpicsaintloup-tourisme`
- `tourisme-lodevois-larzac`
- `visorando`
- `decathlon-outdoor`
- `helloways`

Use the CLI to inspect the active registry:

```bash
wanderer-import providers
```

## Adding A Provider

1. Add a plugin package under `internal/providers/<provider>/`.
2. Pick an existing engine or create a shared engine if needed.
3. Add the provider to `internal/providers/builtins.go`.
4. Add tests for ID extraction, matching, parsing, or link discovery.
5. Update `PROVIDERS.md` with status and implementation notes.
6. Update `README.md` if CLI behavior or user-facing support changed.

## External Plugins

The current architecture is for built-in Go providers. If third-party plugins
are needed later, prefer a subprocess JSON protocol over Go's runtime `plugin`
package. Runtime plugins are fragile across operating systems, Go versions, and
compiler settings; a subprocess protocol keeps provider plugins language-neutral
and easier to distribute.
