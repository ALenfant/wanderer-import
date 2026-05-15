package geotrek

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/wanderer"
)

func TestResolveFetchesAPIDetailStepsAndPhotos(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/trek/343049-Randonnee-Test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
			<title>Fallback title</title>
			<script id="__NEXT_DATA__" type="application/json">{"props":{"apiUrl":"` + server.URL + `/api/v2"}}</script>
		`))
	})
	mux.HandleFunc("/api/v2/trek/343049/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("language"); got != "fr" {
			t.Fatalf("language = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "Randonnée Test",
			"description_teaser": "<p>Short teaser.</p>",
			"ambiance": "<p>Forest and ridge.</p>",
			"description": "<p><strong>D</strong> - Start at the station.</p><ol><li>Turn left at the bridge.</li><li>Climb to the viewpoint.</li></ol>",
			"advice": "<p>Avoid storms.</p>",
			"departure": "Old station",
			"arrival": "Old station",
			"advised_parking": "Station parking",
			"ascent": 123,
			"descent": -122,
			"duration": 2.5,
			"length_2d": 7400,
			"difficulty": 5,
			"attachments": [
				{"type": "image", "url": "https://example.com/photo.jpg", "thumbnail": "https://example.com/photo-thumb.jpg"}
			]
		}`))
	})
	mux.HandleFunc("/api/treks/343049.gpx", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gpx+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><gpx version="1.1" creator="test"><trk><name>Fallback GPX</name><trkseg><trkpt lat="43.1" lon="3.1"></trkpt><trkpt lat="43.2" lon="3.2"></trkpt></trkseg></trk></gpx>`))
	})

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	provider := NewProvider(Config{
		ID:      "test-geotrek",
		Name:    "Test Geotrek",
		Domains: []string{parsed.Hostname()},
	}, server.Client())

	resolved, err := provider.Resolve(context.Background(), importer.Spec{
		Source: server.URL + "/trek/343049-Randonnee-Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()

	metadata := resolved.Metadata
	if metadata.Name == nil || *metadata.Name != "Randonnée Test" {
		t.Fatalf("name = %#v", metadata.Name)
	}
	if metadata.Description == nil {
		t.Fatal("description is nil")
	}
	description := *metadata.Description
	for _, want := range []string{
		"Short teaser.",
		"Route: D - Start at the station.",
		"Itinerary steps:",
		"1. Turn left at the bridge.",
		"2. Climb to the viewpoint.",
		"Advice: Avoid storms.",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q:\n%s", want, description)
		}
	}
	if metadata.Distance == nil || *metadata.Distance != 7400 {
		t.Fatalf("distance = %#v", metadata.Distance)
	}
	if metadata.Duration == nil || *metadata.Duration != 9000 {
		t.Fatalf("duration = %#v", metadata.Duration)
	}
	if metadata.Difficulty == nil || *metadata.Difficulty != wanderer.DifficultyDifficult {
		t.Fatalf("difficulty = %#v", metadata.Difficulty)
	}
	if len(metadata.PhotoURLs) != 1 || metadata.PhotoURLs[0] != "https://example.com/photo.jpg" {
		t.Fatalf("photo urls = %#v", metadata.PhotoURLs)
	}
}
