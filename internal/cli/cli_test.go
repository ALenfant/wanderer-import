package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/wanderer"
)

func TestImportSourcesDryRun(t *testing.T) {
	dir := t.TempDir()
	sourcesPath := filepath.Join(dir, "sources.txt")
	content := strings.Join([]string{
		"# comment",
		"https://www.komoot.com/tour/38639433",
		"",
		"https://www.decathlon-outdoor.com/fr-fr/explore/france/montee-jusqu-a-la-grotte-d-anjeau-64ff2278d1188",
	}, "\n")
	if err := os.WriteFile(sourcesPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr)
	err := app.Run(context.Background(), []string{"import", "--dry-run", "--json", "--sources", sourcesPath})
	if err != nil {
		t.Fatalf("Run: %v\nstderr:\n%s", err, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		`"source": "https://www.komoot.com/tour/38639433"`,
		`"provider": "komoot"`,
		`"source": "https://www.decathlon-outdoor.com/fr-fr/explore/france/montee-jusqu-a-la-grotte-d-anjeau-64ff2278d1188"`,
		`"provider": "decathlon-outdoor"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "comment") {
		t.Fatalf("comment line was imported:\n%s", output)
	}
}

func TestRunImportSpecsContinuesByDefault(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr)
	client := fakeImportClient{}
	registry := importer.NewRegistry(
		fakeImportProvider{name: "ok", ok: true},
		fakeImportProvider{name: "bad", ok: true, err: errors.New("no route data")},
	)

	results, err := app.runImportSpecs(context.Background(), client, registry, []importer.Spec{
		{Source: "ok", Provider: "ok"},
		{Source: "bad", Provider: "bad"},
	}, importRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Error != "" || results[1].Error != "no route data" {
		t.Fatalf("results = %#v", results)
	}
	if !strings.Contains(stderr.String(), "[2/2] warning: failed to import bad: no route data") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), ansiYellow+"[2/2] warning: failed to import bad: no route data\n"+ansiReset) {
		t.Fatalf("stderr is not yellow: %q", stderr.String())
	}
}

func TestRunImportSpecsJSONDoesNotColorWarnings(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr)
	client := fakeImportClient{}
	registry := importer.NewRegistry(fakeImportProvider{name: "bad", ok: true, err: errors.New("no route data")})

	_, err := app.runImportSpecs(context.Background(), client, registry, []importer.Spec{
		{Source: "bad", Provider: "bad"},
	}, importRunOptions{json: true})
	if err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExportErrorLogIsRed(t *testing.T) {
	var stderr bytes.Buffer
	writeError(&stderr, "failed source via provider: no route data\n")
	if got, want := stderr.String(), ansiRed+"failed source via provider: no route data\n"+ansiReset; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRunImportSpecsFailFast(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr)
	client := fakeImportClient{}
	registry := importer.NewRegistry(fakeImportProvider{name: "bad", ok: true, err: errors.New("no route data")})

	_, err := app.runImportSpecs(context.Background(), client, registry, []importer.Spec{
		{Source: "bad", Provider: "bad"},
	}, importRunOptions{failFast: true})
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeImportProvider struct {
	name string
	ok   bool
	err  error
}

func (p fakeImportProvider) Name() string {
	return p.name
}

func (p fakeImportProvider) Match(string) importer.Match {
	return importer.Match{OK: p.ok, Score: 100}
}

func (p fakeImportProvider) Resolve(context.Context, importer.Spec) (*importer.ResolvedTrail, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &importer.ResolvedTrail{
		Filename: "trail.gpx",
		Body:     io.NopCloser(strings.NewReader("<gpx />")),
	}, nil
}

type fakeImportClient struct{}

func (fakeImportClient) UploadTrail(context.Context, string, io.Reader, wanderer.UploadOptions) (*wanderer.Trail, error) {
	return &wanderer.Trail{ID: "abc123", Name: "Imported"}, nil
}

func (fakeImportClient) UpdateTrail(context.Context, string, wanderer.TrailUpdate) (*wanderer.Trail, error) {
	return &wanderer.Trail{ID: "abc123", Name: "Imported"}, nil
}

func (fakeImportClient) UploadTrailPhotoURLs(context.Context, string, []string) (*wanderer.Trail, error) {
	return &wanderer.Trail{ID: "abc123", Name: "Imported"}, nil
}
