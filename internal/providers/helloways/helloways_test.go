package helloways

import "testing"

func TestExtractTrackID(t *testing.T) {
	data := []byte(`https://hlws.ams3.cdn.digitaloceanspaces.com/tracks/629130967f8d908247ddd39b/img-md/photo.jpg`)
	id, ok := extractTrackID(data)
	if !ok {
		t.Fatal("expected track ID")
	}
	if id != "629130967f8d908247ddd39b" {
		t.Fatalf("id = %q, want 629130967f8d908247ddd39b", id)
	}
}

func TestParseTrackPoints(t *testing.T) {
	data := []byte(`{"path":{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"LineString","coordinates":[[3.35,43.64,120],[3.36,43.65,130]]}}]}}`)
	points, err := parseTrackPoints(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("len(points) = %d, want 2", len(points))
	}
	if points[0].Lat != 43.64 || points[0].Lon != 3.35 || points[0].Ele == nil || *points[0].Ele != 120 {
		t.Fatalf("unexpected first point: %+v", points[0])
	}
}
