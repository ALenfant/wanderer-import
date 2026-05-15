package jsonpoints

import "testing"

func TestParseKomootItems(t *testing.T) {
	points, err := ParseKomootItems([]byte(`{"items":[{"lat":43.1,"lng":3.2,"alt":120.5}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Lat != 43.1 || points[0].Lon != 3.2 {
		t.Fatalf("points = %#v", points)
	}
	if points[0].Ele == nil || *points[0].Ele != 120.5 {
		t.Fatalf("elevation = %#v", points[0].Ele)
	}
}

func TestParseSityTrailWKT(t *testing.T) {
	points, err := ParseSityTrailWKT([]byte(`{"data":{"trail":{"trace_wkt":"LINESTRING (3.7 43.9 208, 3.8 44.0 209)"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].Lat != 43.9 || points[0].Lon != 3.7 {
		t.Fatalf("points = %#v", points)
	}
	if points[1].Ele == nil || *points[1].Ele != 209 {
		t.Fatalf("elevation = %#v", points[1].Ele)
	}
}

func TestParseAltitudeRandoLatLonGeoJSON(t *testing.T) {
	parse := ParseGeoJSONLineString("latlon")
	points, err := parse([]byte(`{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"LineString","coordinates":[[43.9,3.7,208],[44.0,3.8,209]]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].Lat != 43.9 || points[0].Lon != 3.7 {
		t.Fatalf("points = %#v", points)
	}
}
