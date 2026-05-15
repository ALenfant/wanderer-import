package alltrails

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"wanderer-import/internal/importer"
)

func TestParseV3TrailOfflineFormat(t *testing.T) {
	data := []byte(`{
	  "trails": [{
	    "name": "Ranc de Banes",
	    "description": "Loop above Sumene.",
	    "activityTypeName": "Hiking",
	    "defaultMap": {
	      "routes": [{
	        "lineSegments": [{
	          "polyline": {"pointsData": "_p~iF~ps|U_ulLnnqC_mqNvxq\u0060@"}
	        }]
	      }]
	    }
	  }]
	}`)

	points, metadata, err := ParseV3Trail(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 {
		t.Fatalf("points = %#v", points)
	}
	if points[0].Lat != 38.5 || points[0].Lon != -120.2 {
		t.Fatalf("first point = %#v", points[0])
	}
	if metadata.Name == nil || *metadata.Name != "Ranc de Banes" {
		t.Fatalf("name = %#v", metadata.Name)
	}
	if metadata.Description == nil || *metadata.Description != "Loop above Sumene." {
		t.Fatalf("description = %#v", metadata.Description)
	}
	if metadata.Category == nil || *metadata.Category != "Hiking" {
		t.Fatalf("category = %#v", metadata.Category)
	}
}

func TestParseV3TrailDeepFormat(t *testing.T) {
	data := []byte(`{
	  "maps": [{
	    "name": "Custom map",
	    "routes": [{
	      "lineSegments": [{
	        "polyline": {"pointsData": "_p~iF~ps|U"}
	      }]
	    }]
	  }]
	}`)

	points, metadata, err := ParseV3Trail(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %#v", points)
	}
	if metadata.Name == nil || *metadata.Name != "Custom map" {
		t.Fatalf("name = %#v", metadata.Name)
	}
}

func TestResolveSavedJSONBuildsGPX(t *testing.T) {
	file := t.TempDir() + "/trail.json"
	data := map[string]any{
		"trails": []any{
			map[string]any{
				"name": "Saved trail",
				"defaultMap": map[string]any{
					"routes": []any{
						map[string]any{
							"lineSegments": []any{
								map[string]any{
									"polyline": map[string]any{"pointsData": "_p~iF~ps|U"},
								},
							},
						},
					},
				},
			},
		},
	}
	body, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := New(nil).Resolve(context.Background(), importer.Spec{Source: file})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	gpx, err := io.ReadAll(resolved.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gpx), "<gpx") || !strings.Contains(string(gpx), "Saved trail") {
		t.Fatalf("unexpected gpx:\n%s", gpx)
	}
}
