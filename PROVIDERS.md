# Providers

This document tracks the provider audit for the ChatGPT shared conversation
containing trails around Montpellier:

`https://chatgpt.com/share/6a05ddc6-73c0-8387-9b84-0d26ad98cc6e`

It answers three questions for each source:

- Does the site expose GPX, KML, GeoJSON, coordinates, or another structured
  route representation?
- Should `wanderer-import` implement an importer for it?
- What is the current implementation status?

## Status Key

- `Implemented`: available in the CLI now.
- `Planned`: should be implemented as a normal provider.
- `Candidate`: useful, but needs more investigation before committing to a
  stable implementation.
- `Deferred`: route data exists or may exist, but access is auth-gated,
  anti-bot-sensitive, or too brittle for the first versions.
- `Out of scope`: not a route provider for this project, or no usable structured
  route data was found in the sampled URLs.

## Implementation Summary

The provider registry and shared engine architecture are implemented. The CLI
now registers multiple built-in provider plugins backed by shared engines.

Current implementation groups:

1. `direct`: local files and direct trail-file URLs.
2. `gpx-link-scraper`: generic provider for pages with direct `.gpx`, `.kml`,
   `.kmz`, or `.geojson` links.
3. `geotrek-gard`: Geotrek-powered route portal for `rando.gard.fr`.
4. Tourism wrappers using the `gpxlinks` engine: Herault Tourisme, Montpellier
   Tourisme, Tourisme Gard, Tourisme Aveyron, Cevennes Tourisme, Grand Pic
   Saint-Loup, Lodevois Larzac, and Visorando.
5. Dedicated export/API providers: `cirkwi`, `visugpx`, `komoot`, `sitytrail`,
   `decathlon-outdoor`, `bergfex`, `helloways`, `altituderando`, and
   `trails-viewer` public points API decoding, plus `alltrails` saved v3 JSON.
6. Source-session support is shared across providers: `--cookies` loads
   Netscape-format browser cookies, `--cookies-from-browser` reads local browser
   stores through Kooky, `--impersonate` adds Chrome/Firefox/Safari-like
   request headers, and `--user-agent`/`--referer` provide manual overrides.
7. Optional browser fetch support is shared through `--browser-fetch`; providers
   still try normal HTTP first and can fall back to Playwright for protected
   same-origin API calls.
8. Later/deferred providers: `randogps`.

Implemented providers extract as much Wanderer-compatible metadata as the
source exposes: name, description, location, difficulty, start coordinates,
distance, elevation gain/loss, duration, source photo URLs, and tags for export
sidecars. Imported metadata is normalized to Wanderer API values where needed,
including `easy`/`moderate`/`difficult` difficulty constants. Source photos are
uploaded after the trail is created when the source exposes usable image URLs.
Image discovery includes structured metadata, JSON-LD, page galleries,
lazy-loaded images, `srcset`, and embedded image URLs, with filtering for
obvious UI assets.

## Provider Table

| Provider ID | Domains | Structured data | Implement? | Status | Import approach | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| `direct` | local paths, direct `http(s)` file URLs | Yes | Yes | Implemented | Upload the supplied GPX/KML/KMZ/FIT/TCX/GeoJSON file directly. | Extracts metadata from supported route-file formats when possible. |
| `gpx-link-scraper` | Generic web pages | Sometimes | Yes | Implemented | Fetch page HTML and discover direct trail-file links using the `gpxlinks` engine. | Merges route-file metadata with HTML/JSON-LD page metadata, structured image URLs, and body-description fallbacks. |
| `herault-tourisme` | `herault-tourisme.com` | Yes | Yes | Implemented | Domain plugin backed by `gpxlinks`. | Extracts Tourinsoft activity labels and `tracegps.balises` steps from `DsioDetail.initialize`; Tourinsoft/GeoJSON fallback is still a future improvement. Evidence: `https://www.herault-tourisme.com/fr/fiche/itineraires-touristiques/boucle-cyclo-n11-le-lac-du-salagou-et-le-cirque-de-moureze-clermont-l-herault_TFOITILAR034V51PHO5/`, `https://www.herault-tourisme.com/fr/fiche/itineraires-touristiques/randonnee-de-l-oppidum-de-la-ramasse-clermont-l-herault_TFOITILAR034V52SFCT/`. |
| `montpellier-tourisme` | `montpellier-tourisme.fr` | Yes | Yes | Implemented | Domain plugin backed by `gpxlinks`. | Same shared logic as Herault Tourisme. |
| `tourismegard` | `tourismegard.com` | Yes | Yes | Implemented | Domain plugin backed by `gpxlinks`. | Some pages expose GPX links through Apidae/static resources. |
| `tourisme-aveyron` | `tourisme-aveyron.com` | Mixed, often yes | Yes | Implemented | Domain plugin backed by `gpxlinks`; unsupported pages return a clear error. | Some itinerary pages expose GPX, some only metadata. |
| `geotrek-gard` | `rando.gard.fr`, `rando-preprod.gard.fr` | Yes | Yes | Implemented | Geotrek engine fetches public API v2 detail metadata, tries route export URLs, then falls back to GPX link scraping. | Extracts ordered itinerary steps, photos, distance/elevation/duration, and difficulty when the page exposes an API base. Evidence: `https://geotrek-aggr.gard.fr/api/v2/trek/343049/?language=fr`. |
| `grandpicsaintloup-tourisme` | `grandpicsaintloup-tourisme.fr` | Yes, session-rendered GPX links | Yes | Implemented | Perform the site viewport/session redirect, then use `gpxlinks` on the expanded itinerary page. | Evidence: `https://www.grandpicsaintloup-tourisme.fr/en/_pages/itineraries/randonnee-du-ravin-des-arcs` redirects to an itinerary page exposing `/iti/gpx/itineraire-21.gpx`. |
| `tourisme-lodevois-larzac` | `tourisme-lodevois-larzac.fr` | Yes, JS-loaded | Yes | Implemented | Domain plugin backed by `gpxlinks`. | Backing API/rendered-data extraction is still a future improvement. |
| `cirkwi` | `cirkwi.com` | Yes | Yes | Implemented | Parse page-embedded `objetJSON.trace.altimetries` coordinates and metadata, then fall back to Cirkwi GPX export URL templates. | Current pages expose the full route and metadata in `objetJSON`, including photos, tags, duration, difficulty hints, and step/topo descriptions. Evidence: `https://www.cirkwi.com/fr/circuit/1070291-randonnee-le-ranc-de-banes`. |
| `visorando` | `visorando.com` | Yes | Yes | Implemented | Domain plugin backed by `gpxlinks`. | May need a dedicated endpoint provider if anti-bot behavior blocks page fetches. |
| `komoot` | `komoot.com` | Yes, coordinates | Yes | Implemented | Use public coordinate API and generate GPX with `gpxgo`. | GPX download may require login; coordinate API is enough to create a Wanderer import file; duration and point stats are extracted where exposed. |
| `decathlon-outdoor` | `decathlon-outdoor.com` | Yes, GeoJSON | Yes | Implemented | Use the public `/bff/route/{id}/geojson` endpoint, generate GPX, and merge page JSON-LD metadata. Inspiration pages resolve the first embedded `/explore/...-{id}` route link before using the same endpoint. | Representative endpoint: `https://www.decathlon-outdoor.com/bff/route/64ff2278d1188/geojson`; inspiration evidence: `https://www.decathlon-outdoor.com/fr-fr/inspire/france/randonnee-cascade-de-la-vis-gard`. |
| `visugpx` | `visugpx.com` | Yes | Yes | Implemented | Use `download.php?id=...` URL template. | Straightforward dedicated provider. |
| `sitytrail` | `sitytrail.com` | Yes, coordinates/WKT | Yes | Implemented | Use public API route data, parse WKT, and generate GPX with `gpxgo`. | Public API exposes trace geometry, while UI download may require login. |
| `altituderando` | `altituderando.com` | Yes, JSON trace | Yes | Implemented | Extract page-linked JSON trace and generate GPX with `gpxgo`. | GPX download path may be login-gated, but route coordinates are exposed. |
| `trails-viewer` | `fr-fr.trails-viewer.com` and other `trails-viewer.com` locales | Yes, encoded JS API | Yes | Implemented | Read the page `appTrail` ID/version/`appTime`, call `api.trails.getPoints` with the page-derived token, decode shifted base36 coordinates, and generate GPX. If normal HTTP returns a browser-session status, `--browser-fetch` can retry the points request inside a Playwright same-origin page context. | The response format, token derivation, and browser fallback trigger are covered by tests. Chrome and Firefox DevTools checks showed `api.trails.getPoints` returns `403` with omitted credentials but `200` inside a loaded page using same-origin credentials. Live CLI verification succeeded with `--browser-fetch chrome --browser-fetch-headful`; headless Chromium still returned `403`. Evidence URL: `https://fr-fr.trails-viewer.com/trail-fd5kl/Gourgas-Cirque-du-Bout-du-Monde/`. |
| `randogps` | `randogps.net` | Likely yes | Maybe | Candidate | Investigate old GPX download flow and page structure. | The site advertises free GPX but the flow looked brittle. |
| `bergfex` | `bergfex.fr` | Yes, GPX export | Yes | Implemented | Extract the numeric tour ID and download `/downloads/gps/?id={id}&fileType=gpx`. | Representative endpoint: `https://www.bergfex.fr/downloads/gps/?id=3878525&fileType=gpx`. |
| `alltrails` | `alltrails.com` | Yes, but page/API fetch is bot-blocked | Yes, manual JSON plus authenticated source sessions | Implemented | Import saved `/api/alltrails/v3/trails/...` JSON responses and convert encoded route polylines to GPX. Page/API URLs can use shared `--cookies`, `--cookies-from-browser`, `--user-agent`, `--referer`, and `--impersonate chrome` source-session options, but DataDome may still block non-browser TLS fingerprints. | Chrome DevTools verification on `https://www.alltrails.com/en-gb/trail/france/gard/sumene-ranc-de-banes-pont-des-chevres` returned a DataDome 403 before trail data loaded. Existing public tooling indicates the useful response is `https://www.alltrails.com/api/alltrails/v3/trails/{route_id}` with polyline data at `/trails/0/defaultMap/routes/0/lineSegments/0/polyline/pointsData`; the provider also supports `/maps/0/routes/0/lineSegments/0/polyline/pointsData`. Direct exported GPX already works through `direct`. |
| `helloways` | `helloways.com` | Yes, public JSON coordinates | Yes | Implemented | Extract the track ID from public page media URLs, read `/api/tracks/{id}`, convert the `path` GeoJSON to GPX, and extract metadata/photos from the track JSON. | Representative endpoint: `https://www.helloways.com/api/tracks/629130967f8d908247ddd39b`. GPX download remains login/credit-gated, but geometry is public. |
| `sentiers-en-france` | `sentiers-en-france.eu` | Mostly no | No | Out of scope | None. | Sample pages had empty GPX fields or non-downloadable "Trace GPS" UI. |
| `occitanie-rando` | `occitanie-rando.fr` | No | No | Out of scope | None. | Sample pages were static topo/map content without machine-readable track data. |
| `destination-salagou` | `destination-salagou.fr` | Mostly no | No | Out of scope | None. | Metadata/PDF pages; no GPX found in sampled pages. |
| `cevennes-tourisme` | `cevennes-tourisme.fr` | Yes | Yes | Implemented | Domain plugin backed by `gpxlinks`; pages expose Apidae GPX links and inline route geometry. | Evidence: `https://www.cevennes-tourisme.fr/offres/randonnee-autour-des-aigladines-mialet-fr-6207984/` exposes `https://static.apidae-tourisme.com/filestore/objets-touristiques/plans/107/73/37767531/autour-des-aigladines.gpx`. |
| `sudcevennes` | `sudcevennes.com` | Unknown/likely metadata | No for now | Out of scope | Generic GPX scraper can catch direct files if present. | No dedicated provider planned. |
| `outdooractive` | `outdooractive.com` | Not for sampled URLs | No | Out of scope | None. | Sample was a climbing spot, not a route. |
| `ignrando` | `ignrando.fr` | No/currently sunset | No | Out of scope | None. | Pages redirect into IGN/WeTrek transition information. |
| `hika` | `hika.app` | App/metadata only | No | Out of scope | None. | No public route geometry found in sampled page. |
| `thecrag` | `thecrag.com` | Climbing data | No | Out of scope | None. | Not a hiking trail provider for Wanderer import. |
| `guide-goyav` | `guide-goyav.com` | Article content | No | Out of scope | None. | Travel article, not structured route source. |
| `rando-grandemotte` | `rando-grandemotte.fr` | Article content | No | Out of scope | None. | No structured route data found. |
| `randoherault` | `randoherault.fr` | No | No | Out of scope | None. | Sample indicated the detailed route was unavailable. |
| `randonnees-herault` | `randonnees-herault.fr` | Unknown/site issue | No for now | Out of scope | None. | Host/SSL issues during audit. |
| `toporandosmontagne` | `toporandosmontagne.com` | No obvious data | No | Out of scope | None. | No structured route data found in sampled pages. |
| `theoutbound` | `theoutbound.com` | Point metadata only | No | Out of scope | None. | Sample was not a downloadable trail route. |
| `vagueintrepide` | `vagueintrepide.com` | Article content | No | Out of scope | None. | No provider planned. |
| `webzinevoyage` | `webzinevoyage.fr` | Article content | No | Out of scope | None. | Links out to stronger sources such as Herault Tourisme. |
| `ville-data` | `ville-data.com` | Directory metadata | No | Out of scope | None. | Not a route geometry provider. |
| `wanderlog` | `wanderlog.com` | Place/travel guide | No | Out of scope | None. | Not a trail-file source. |
| `petitfute` | `petitfute.com` | POI guide | No | Out of scope | None. | Not a trail-file source. |
| `scribd` | `fr.scribd.com` | Document host | No | Out of scope | None. | Not a reliable structured route source. |
| `accommodation-sites` | `gitedegroupe.fr`, `grandsgites.com`, `gitedanjeau.fr`, `campinglacdusalagou.fr`, `campingdestempliers-caylar-larzac.fr`, `campingborepo.fr` | No | No | Out of scope | None. | Accommodation/camping pages, not route providers. |
| `blogs-and-clubs` | `france3-regions.blog.franceinfo.fr`, `bougetafrance.fr`, `pierreeteau.fr`, `cafmontpellier.ffcam.fr`, `narbonne-randonnee-montagne.clubeo.com`, `lamarcheasuivre.fr`, `guide-goyav.com` | Usually no | No | Out of scope | None. | Article or club pages; generic GPX link extraction can catch direct files if present. |
| `canyoning-and-activity-sites` | `moniteurs-herault.fr`, `languedoc-canyoning.fr`, `ludo-sport-aventure.com`, `natureo-sport-aventure.com`, `impulsion-voyages.fr`, `fr.milesrepublic.com` | No for hiking route import | No | Out of scope | None. | Activity pages, not trail route sources. |

## Domain Inventory From The Conversation

The shared conversation included these route-adjacent domains after removing
OpenAI/ChatGPT/static/internal links.

| Domain | URL count | Provider decision |
| --- | ---: | --- |
| `herault-tourisme.com` | 253 | Implemented through `herault-tourisme` / `gpx-link-scraper`. |
| `alltrails.com` | 38 | Implemented for saved v3 JSON responses and shared source-session options, including direct browser cookie loading; automated public page/API fetch may still be blocked by DataDome/TLS fingerprinting. |
| `montpellier-tourisme.fr` | 27 | Implemented through `montpellier-tourisme` / `gpx-link-scraper`. |
| `tourisme-aveyron.com` | 20 | Implemented where GPX links are present. |
| `cevennes-tourisme.fr` | 14 | Implemented through `cevennes-tourisme` / `gpx-link-scraper`. |
| `randogps.net` | 11 | Candidate. |
| `decathlon-outdoor.com` | 11 | Implemented through public GeoJSON API, page metadata, and first-route resolution for inspiration pages. |
| `visorando.com` | 10 | Implemented as GPX-link wrapper. |
| `komoot.com` | 10 | Implemented through coordinates API. |
| `sentiers-en-france.eu` | 8 | Out of scope. |
| `occitanie-rando.fr` | 8 | Out of scope. |
| `tourismegard.com` | 6 | Implemented through `tourismegard` / `gpx-link-scraper`. |
| `gitedegroupe.fr` | 5 | Out of scope. |
| `destination-salagou.fr` | 5 | Out of scope. |
| `thecrag.com` | 4 | Out of scope. |
| `sitytrail.com` | 4 | Implemented through public WKT API. |
| `dav-berlin.de` | 4 | Out of scope. |
| `toporandosmontagne.com` | 3 | Out of scope. |
| `rando.gard.fr` | 3 | Implemented through `geotrek-gard`. |
| `grandpicsaintloup-tourisme.fr` | 3 | Implemented with session redirect plus GPX-link extraction. |
| `fr-fr.trails-viewer.com` | 3 | Implemented for encoded points API responses, with optional `--browser-fetch` Playwright fallback for same-origin points API requests that reject plain HTTP clients. |
| `cirkwi.com` | 3 | Implemented through page-embedded route JSON, with export URL templates as fallback. |
| `visugpx.com` | 2 | Implemented through `download.php` URL template. |
| `ville-data.com` | 2 | Out of scope. |
| `tourisme-lodevois-larzac.fr` | 2 | Implemented as GPX-link wrapper; rendered/API extraction remains pending. |
| `helloways.com` | 2 | Implemented through public track JSON. |
| `bergfex.fr` | 2 | Implemented through GPX export endpoint. |
| Other singletons | 1 each | Mixed; see provider-specific rows, otherwise out of scope unless generic GPX extraction finds a direct file. |

Other singleton domains observed:

`webzinevoyage.fr`, `wanderlog.com`, `vagueintrepide.com`,
`theoutbound.com`, `sudcevennes.com`, `randonnees-herault.fr`,
`randoherault.fr`, `rando-preprod.gard.fr`, `rando-grandemotte.fr`,
`quefaireautour.fr`, `pierreeteau.fr`, `petitfute.com`, `outdooractive.com`,
`natureo-sport-aventure.com`, `narbonne-randonnee-montagne.clubeo.com`,
`moniteurs-herault.fr`, `martinpierre.fr`, `luxfugae.fr`,
`ludo-sport-aventure.com`, `languedoc-canyoning.fr`, `lamarcheasuivre.fr`,
`impulsion-voyages.fr`, `ignrando.fr`, `hika.app`, `herault.fr`,
`guide-goyav.com`, `grandsgites.com`, `gitedanjeau.fr`,
`france3-regions.blog.franceinfo.fr`, `fr.scribd.com`,
`fr.milesrepublic.com`, `fr-academic.com`, `commune-mairie.fr`,
`campinglacdusalagou.fr`, `campingdestempliers-caylar-larzac.fr`,
`campingborepo.fr`, `cafmontpellier.ffcam.fr`, `bougetafrance.fr`,
`altituderando.com`.

## Representative Audit URLs

These URLs were used as representative checks while classifying providers:

- Herault Tourisme:
  `https://www.herault-tourisme.com/fr/fiche/itineraires-touristiques/randonnee-les-balcons-de-soumont-soumont_TFOITILAR034V50GD7G/`
- Montpellier Tourisme:
  `https://www.montpellier-tourisme.fr/decouvrir/au-coeur-de-la-nature/se-balader-randonner/tous-les-itineraires-de-randonnee-a-pied-a-velo-a-moto/randonnee-du-chateau-de-restinclieres-prades-le-lez-fr-4115343/`
- Rando Gard:
  `https://rando.gard.fr/trek/343049-Randonnee-Le-Ranc-de-Banes`
- Visorando:
  `https://www.visorando.com/randonnee-le-roc-blanc/`
- Komoot coordinates API:
  `https://api.komoot.de/v007/smart_tours/38639433/coordinates`
- Decathlon Outdoor:
  `https://www.decathlon-outdoor.com/fr-fr/explore/france/montee-jusqu-a-la-grotte-d-anjeau-64ff2278d1188`
- Decathlon Outdoor inspiration page:
  `https://www.decathlon-outdoor.com/fr-fr/inspire/france/randonnee-cascade-de-la-vis-gard`
- Bergfex:
  `https://www.bergfex.fr/sommer/okzitanien/touren/wanderung/3878525%2Ccol-de-lane--roc-blanc--montagne-de-la-seranne/`
- Helloways:
  `https://www.helloways.com/hike/lac-du-salagou-liausson`
- Cirkwi:
  `https://www.cirkwi.com/fr/circuit/1070291-randonnee-le-ranc-de-Banes`
- VisuGPX:
  `https://www.visugpx.com/GRXijN33Vd`
- SityTrail API:
  `https://capi.geolives.com/qq0zvz2zws4bq2lad0pu/sitytour/trails/106716`
- AltitudeRando:
  `https://www.altituderando.com/Tour-du-pic-et-de-la-grotte-d-Anjeau`

## Maintenance Rules

Update this file whenever:

- A provider is implemented, renamed, deferred, or removed.
- A site changes its route export/API behavior.
- A new domain appears in the source conversation or future import corpus.
- A provider moves from `Candidate` or `Deferred` into `Planned` or
  `Implemented`.
- Evidence URLs change or become unavailable.

When updating a row, keep the status, implementation decision, and import
approach aligned with the actual code.
