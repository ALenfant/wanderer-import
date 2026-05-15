package importer

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"wanderer-import/internal/wanderer"
)

type fakeProvider struct {
	name        string
	score       int
	ok          bool
	description string
}

func (p fakeProvider) Name() string {
	return p.name
}

func (p fakeProvider) Match(string) Match {
	return Match{OK: p.ok, Score: p.score}
}

func (p fakeProvider) Resolve(context.Context, Spec) (*ResolvedTrail, error) {
	var metadata wanderer.TrailUpdate
	if p.description != "" {
		metadata.Description = &p.description
	}
	return &ResolvedTrail{Filename: "trail.gpx", Body: io.NopCloser(strings.NewReader("<gpx />")), Metadata: metadata}, nil
}

func TestRegistrySelectChoosesHighestScoredMatch(t *testing.T) {
	registry := NewRegistry(
		fakeProvider{name: "generic", score: 50, ok: true},
		fakeProvider{name: "specific", score: 90, ok: true},
	)

	provider, err := registry.Select("auto", "https://example.com/route")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "specific" {
		t.Fatalf("provider = %q", provider.Name())
	}
}

func TestRegistrySelectHonorsExplicitProvider(t *testing.T) {
	registry := NewRegistry(
		fakeProvider{name: "generic", score: 50, ok: true},
		fakeProvider{name: "specific", score: 90, ok: true},
	)

	provider, err := registry.Select("generic", "https://example.com/route")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "generic" {
		t.Fatalf("provider = %q", provider.Name())
	}
}

func TestImportAppendsSourceToDescription(t *testing.T) {
	client := &captureClient{}
	registry := NewRegistry(fakeProvider{name: "specific", score: 90, ok: true, description: "Nice hike."})
	source := "https://example.test/trails/nice-hike"

	_, err := Import(context.Background(), client, registry, Spec{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if client.update.Description == nil {
		t.Fatal("description was not updated")
	}
	want := "Nice hike.\n\nImported by wanderer-import\nwanderer-import-source: " + source + "\nwanderer-import-provider: specific"
	if *client.update.Description != want {
		t.Fatalf("description = %q, want %q", *client.update.Description, want)
	}
}

func TestAppendImportMarkerToDescriptionDoesNotDuplicate(t *testing.T) {
	source := "https://example.test/trail"
	description := "Nice hike.\n\nImported by wanderer-import\nwanderer-import-source: " + source + "\nwanderer-import-provider: specific"
	update := wanderer.TrailUpdate{Description: &description}

	appendImportMarkerToDescription(&update, source, "specific")
	if update.Description == nil || *update.Description != description {
		t.Fatalf("description = %#v", update.Description)
	}
}

func TestImportUpdateExistingUsesSourceMatch(t *testing.T) {
	client := &captureClient{existing: &wanderer.Trail{ID: "existing123", Name: "Old trail"}}
	registry := NewRegistry(fakeProvider{name: "specific", score: 90, ok: true, description: "New metadata."})
	source := "https://example.test/trails/nice-hike"

	result, err := Import(context.Background(), client, registry, Spec{Source: source, UpdateExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	if client.uploaded {
		t.Fatal("uploaded a new trail")
	}
	if result.ID != "existing123" {
		t.Fatalf("id = %q, want existing123", result.ID)
	}
	if client.update.Description == nil || !strings.Contains(*client.update.Description, "wanderer-import-source: "+source) {
		t.Fatalf("description marker missing: %#v", client.update.Description)
	}
}

func TestImportUpdateExistingPreservesDescriptionForMarkerOnlyUpdate(t *testing.T) {
	client := &captureClient{existing: &wanderer.Trail{
		ID:          "existing123",
		Name:        "Old trail",
		Description: "User-edited description.",
	}}
	registry := NewRegistry(fakeProvider{name: "specific", score: 90, ok: true})
	source := "https://example.test/trails/nice-hike"

	_, err := Import(context.Background(), client, registry, Spec{Source: source, UpdateExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	if client.update.Description == nil {
		t.Fatal("description was not updated")
	}
	if !strings.Contains(*client.update.Description, "User-edited description.") {
		t.Fatalf("existing description was not preserved: %q", *client.update.Description)
	}
	if !strings.Contains(*client.update.Description, "wanderer-import-source: "+source) {
		t.Fatalf("description marker missing: %q", *client.update.Description)
	}
}

func TestImportDuplicateUploadUpdatesMatchedTrail(t *testing.T) {
	description := "New metadata."
	distance := 1000.0
	lat := 43.1
	lon := 3.1
	client := &captureClient{
		uploadErr: &wanderer.APIError{StatusCode: http.StatusBadRequest, Message: "Duplicate trail"},
		duplicate: &wanderer.Trail{
			ID:       "duplicate123",
			Name:     "Old duplicate",
			Distance: distance,
			Lat:      lat,
			Lon:      lon,
		},
	}
	registry := NewRegistry(fakeProvider{name: "specific", score: 90, ok: true, description: description})
	source := "https://example.test/trails/duplicate"

	result, err := Import(context.Background(), client, registry, Spec{
		Source: source,
		Update: wanderer.TrailUpdate{
			Distance: &distance,
			Lat:      &lat,
			Lon:      &lon,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "duplicate123" {
		t.Fatalf("id = %q, want duplicate123", result.ID)
	}
	if client.update.Description == nil || !strings.Contains(*client.update.Description, "wanderer-import-source: "+source) {
		t.Fatalf("description marker missing: %#v", client.update.Description)
	}
}

func TestImportDuplicateUploadWithoutMatchReturnsOriginalError(t *testing.T) {
	client := &captureClient{uploadErr: &wanderer.APIError{StatusCode: http.StatusBadRequest, Message: "Duplicate trail"}}
	registry := NewRegistry(fakeProvider{name: "specific", score: 90, ok: true})

	_, err := Import(context.Background(), client, registry, Spec{Source: "https://example.test/trails/duplicate"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Duplicate trail") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportHandlesPhotoUploadFailure(t *testing.T) {
	client := &captureClient{photoErr: http.ErrHandlerTimeout}
	registry := NewRegistry(fakeProvider{name: "specific", score: 90, ok: true})
	source := "https://example.test/trails/nice-hike"

	result, err := Import(context.Background(), client, registry, Spec{
		Source: source,
		Update: wanderer.TrailUpdate{PhotoURLs: []string{"http://broken.test/img.jpg"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "abc123" {
		t.Fatalf("id = %q, want abc123", result.ID)
	}
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "photo upload failed") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatal("expected photo upload failure warning")
	}
}

type captureClient struct {
	update    wanderer.TrailUpdate
	existing  *wanderer.Trail
	duplicate *wanderer.Trail
	uploaded  bool
	uploadErr error
	photoErr  error
}

func (c *captureClient) UploadTrail(context.Context, string, io.Reader, wanderer.UploadOptions) (*wanderer.Trail, error) {
	c.uploaded = true
	if c.uploadErr != nil {
		return nil, c.uploadErr
	}
	return &wanderer.Trail{ID: "abc123", Name: "Trail"}, nil
}

func (c *captureClient) UpdateTrail(_ context.Context, id string, update wanderer.TrailUpdate) (*wanderer.Trail, error) {
	c.update = update
	return &wanderer.Trail{ID: id, Name: "Trail", Description: deref(update.Description)}, nil
}

func (c *captureClient) UploadTrailPhotoURLs(_ context.Context, id string, _ []string) (*wanderer.Trail, error) {
	if c.photoErr != nil {
		return nil, c.photoErr
	}
	return &wanderer.Trail{ID: id, Name: "Trail"}, nil
}

func (c *captureClient) FindTrailBySource(context.Context, string) (*wanderer.Trail, bool, error) {
	return c.existing, c.existing != nil, nil
}

func (c *captureClient) FindDuplicateTrail(context.Context, wanderer.TrailUpdate) (*wanderer.Trail, bool, error) {
	if c.duplicate == nil {
		return nil, false, nil
	}
	return c.duplicate, true, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
