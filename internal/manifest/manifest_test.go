package manifest

import (
	"strings"
	"testing"
)

func TestLoadObjectManifest(t *testing.T) {
	specs, err := Load(strings.NewReader(`{
		"imports": [
			{
				"source": "https://example.test/trail.gpx",
				"provider": "direct",
				"ignoreDuplicates": true,
				"updateExisting": true,
				"name": "Ridge walk",
				"public": false,
				"tags": ["hike", "ridge", ""]
			}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d", len(specs))
	}
	spec := specs[0]
	if spec.Source != "https://example.test/trail.gpx" {
		t.Fatalf("source = %q", spec.Source)
	}
	if !spec.IgnoreDuplicates {
		t.Fatal("ignoreDuplicates was not loaded")
	}
	if !spec.UpdateExisting {
		t.Fatal("updateExisting was not loaded")
	}
	if spec.Update.Name == nil || *spec.Update.Name != "Ridge walk" {
		t.Fatalf("name = %#v", spec.Update.Name)
	}
	if spec.Update.Public == nil || *spec.Update.Public {
		t.Fatalf("public = %#v", spec.Update.Public)
	}
	if len(spec.Update.Tags) != 2 {
		t.Fatalf("tags = %#v", spec.Update.Tags)
	}
}

func TestLoadSingleEntryManifest(t *testing.T) {
	specs, err := Load(strings.NewReader(`{"source":"walk.gpx","name":"Walk"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d", len(specs))
	}
	if specs[0].Source != "walk.gpx" {
		t.Fatalf("source = %q", specs[0].Source)
	}
}
