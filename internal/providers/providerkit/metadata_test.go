package providerkit

import (
	"net/url"
	"strings"
	"testing"

	"wanderer-import/internal/wanderer"
)

func TestExtractHTMLMetadataFromJSONLD(t *testing.T) {
	base, err := url.Parse("https://www.decathlon-outdoor.com/fr-fr/explore/france/route-64ff2278d1188")
	if err != nil {
		t.Fatal(err)
	}

	update := ExtractHTMLMetadata(base, []byte(`<!doctype html>
<html>
<head>
<script type="application/ld+json">{
  "@context":"https://schema.org",
  "@type":"Product",
  "name":"Montée jusqu'à la grotte d'Anjeau",
  "description":"Belle boucle.",
  "image":{"url":"/images/trail-photo.webp"},
  "category":"Hiking route",
  "additionalProperty":[
    {"@type":"PropertyValue","name":"Durée est.","value":"PT4H45M"},
    {"@type":"PropertyValue","name":"Distance","value":"9,8 km"},
    {"@type":"PropertyValue","name":"Dénivelé positif","value":"683 m"},
    {"@type":"PropertyValue","name":"Difficulté","value":"Difficile"}
  ]
}</script>
<script type="application/ld+json">{
  "@context":"https://schema.org",
  "@type":"Place",
  "address":{"@type":"PostalAddress","addressLocality":"Saint-Laurent-le-Minier","addressRegion":"Gard","addressCountry":"FR"},
  "geo":{"@type":"GeoCoordinates","longitude":3.657675,"latitude":43.928766}
}</script>
<meta property="og:image" content="https://cdn.example.test/routes/photo.jpg">
</head>
<body></body>
</html>`))

	if update.Name == nil || *update.Name != "Montée jusqu'à la grotte d'Anjeau" {
		t.Fatalf("name = %#v", update.Name)
	}
	if update.Location == nil || *update.Location != "Saint-Laurent-le-Minier, Gard, FR" {
		t.Fatalf("location = %#v", update.Location)
	}
	if update.Difficulty == nil || *update.Difficulty != wanderer.DifficultyDifficult {
		t.Fatalf("difficulty = %#v", update.Difficulty)
	}
	if update.Category == nil || *update.Category != "Hiking" {
		t.Fatalf("category = %#v", update.Category)
	}
	assertFloat(t, "distance", update.Distance, 9800)
	assertFloat(t, "duration", update.Duration, 17100)
	assertFloat(t, "elevation gain", update.ElevationGain, 683)
	assertFloat(t, "lat", update.Lat, 43.928766)
	assertFloat(t, "lon", update.Lon, 3.657675)
	if len(update.PhotoURLs) != 2 {
		t.Fatalf("photo urls = %#v", update.PhotoURLs)
	}
}

func TestExtractHTMLMetadataFromDsioDetailPayload(t *testing.T) {
	base, err := url.Parse("https://www.herault-tourisme.com/fr/fiche/itineraires-touristiques/randonnee_TFOITILAR034/")
	if err != nil {
		t.Fatal(err)
	}

	update := ExtractHTMLMetadata(base, []byte(`<!doctype html>
<html>
<head><title>RANDONNÉE DE L’OPPIDUM</title></head>
<body>
  <p class="lgrid-column-center-middle pr-40 pl-40"><i class="lae-icon-hiking-light"></i><span> Pédestre</span></p>
  <script>DsioDetail.initialize({
    "title":"RANDONNÉE DE L’OPPIDUM",
    "description":"Le sentier traverse un oppidum.",
    "tracegps":{"balises":[
      {"numero_etape":1,"titre":"Départ","descriptif":"Suivre les allées puis traverser le boulevard."},
      {"numero_etape":2,"titre":"","descriptif":"Monter l'ancien chemin dit de Toulouse."}
    ]}
  });</script>
</body>
</html>`))

	if update.Category == nil || *update.Category != "Hiking" {
		t.Fatalf("category = %#v", update.Category)
	}
	if update.Description == nil {
		t.Fatal("description is nil")
	}
	for _, want := range []string{
		"Le sentier traverse un oppidum.",
		"Itinerary steps:",
		"1. Départ: Suivre les allées puis traverser le boulevard.",
		"2. Monter l'ancien chemin dit de Toulouse.",
	} {
		if !strings.Contains(*update.Description, want) {
			t.Fatalf("description missing %q:\n%s", want, *update.Description)
		}
	}
}

func TestExtractHTMLMetadataMapsCyclingCategory(t *testing.T) {
	base, err := url.Parse("https://www.herault-tourisme.com/fr/fiche/itineraires-touristiques/boucle-cyclo_TFOITILAR034/")
	if err != nil {
		t.Fatal(err)
	}

	update := ExtractHTMLMetadata(base, []byte(`<!doctype html>
<html>
<head><title>BOUCLE CYCLO N°11</title></head>
<body>
  <p class="lgrid-column-center-middle pr-40 pl-40"><i class="lae-icon-velo"></i><span> Cyclotouriste</span></p>
</body>
</html>`))

	if update.Category == nil || *update.Category != "Biking" {
		t.Fatalf("category = %#v", update.Category)
	}
}

func TestExtractHTMLMetadataFallsBackToBodyDescription(t *testing.T) {
	base, err := url.Parse("https://example.test/route")
	if err != nil {
		t.Fatal(err)
	}

	update := ExtractHTMLMetadata(base, []byte(`<!doctype html>
<html>
<head><title>Route</title></head>
<body>
  <main>
    <p>Cette randonnée grimpe progressivement dans une belle forêt avant de rejoindre un balcon rocheux avec une vue dégagée sur la vallée.</p>
  </main>
</body>
</html>`))

	if update.Description == nil || *update.Description == "" {
		t.Fatalf("description = %#v", update.Description)
	}
}

func TestExtractHTMLMetadataFindsGalleryImages(t *testing.T) {
	base, err := url.Parse("https://example.test/routes/walk")
	if err != nil {
		t.Fatal(err)
	}

	update := ExtractHTMLMetadata(base, []byte(`<!doctype html>
<html>
<body>
  <img src="/assets/logo.svg">
  <img data-src="/uploads/routes/walk-1.jpg">
  <picture>
    <source srcset="/media/walk-small.webp 640w, /media/walk-large.webp 1200w">
  </picture>
  <div style="background-image: url('/photos/walk-hero.png')"></div>
  <script>window.route = {"image": "https:\/\/cdn.example.test\/gallery\/walk-2.jpg"};</script>
</body>
</html>`))

	want := []string{
		"https://example.test/uploads/routes/walk-1.jpg",
		"https://example.test/media/walk-small.webp",
		"https://example.test/media/walk-large.webp",
		"https://example.test/photos/walk-hero.png",
		"https://cdn.example.test/gallery/walk-2.jpg",
	}
	for _, url := range want {
		if !containsString(update.PhotoURLs, url) {
			t.Fatalf("photo urls missing %q: %#v", url, update.PhotoURLs)
		}
	}
	if containsString(update.PhotoURLs, "https://example.test/assets/logo.svg") {
		t.Fatalf("logo should be filtered: %#v", update.PhotoURLs)
	}
}

func TestMetadataFromGPXTrailData(t *testing.T) {
	update := MetadataFromTrailData("route.gpx", nil, []byte(`<?xml version="1.0"?>
<gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1">
  <metadata>
    <name>Ridge loop</name>
    <desc>Morning route</desc>
    <time>2026-05-14T06:30:00Z</time>
    <keywords>hike,ridge</keywords>
  </metadata>
  <trk>
    <name>Track name</name>
    <type>hiking</type>
    <trkseg>
      <trkpt lat="43.0" lon="3.0"><ele>100</ele></trkpt>
      <trkpt lat="43.001" lon="3.001"><ele>110</ele></trkpt>
      <trkpt lat="43.002" lon="3.002"><ele>105</ele></trkpt>
    </trkseg>
  </trk>
</gpx>`))

	if update.Name == nil || *update.Name != "Ridge loop" {
		t.Fatalf("name = %#v", update.Name)
	}
	if update.Description == nil || *update.Description != "Morning route" {
		t.Fatalf("description = %#v", update.Description)
	}
	if update.Date == nil || *update.Date != "2026-05-14T06:30:00Z" {
		t.Fatalf("date = %#v", update.Date)
	}
	assertFloat(t, "lat", update.Lat, 43.0)
	assertFloat(t, "lon", update.Lon, 3.0)
	if update.Distance == nil || *update.Distance <= 0 {
		t.Fatalf("distance = %#v", update.Distance)
	}
	if len(update.Tags) != 3 {
		t.Fatalf("tags = %#v", update.Tags)
	}
}

func TestPointsFromGeoJSONPreservesElevation(t *testing.T) {
	points, err := PointsFromGeoJSON([]byte(`{
  "type":"Feature",
  "properties":{"name":"Geo route"},
  "geometry":{"type":"LineString","coordinates":[[3.0,43.0,100],[3.001,43.001,125]]}
}`), "lonlat")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("points = %#v", points)
	}
	if points[0].Lat != 43.0 || points[0].Lon != 3.0 {
		t.Fatalf("first point = %#v", points[0])
	}
	if points[1].Ele == nil || *points[1].Ele != 125 {
		t.Fatalf("elevation = %#v", points[1].Ele)
	}
}

func assertFloat(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s is nil, want %v", name, want)
	}
	diff := *got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.000001 {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
