package direct

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"wanderer-import/internal/importer"
)

func TestResolveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trail.gpx")
	if err := os.WriteFile(path, []byte("<gpx />"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := New(nil).Resolve(context.Background(), importer.Spec{Source: path})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()

	if resolved.Filename != "trail.gpx" {
		t.Fatalf("filename = %q", resolved.Filename)
	}
	content, err := io.ReadAll(resolved.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "<gpx />" {
		t.Fatalf("content = %q", content)
	}
}

func TestResolveURLUsesContentDispositionFilename(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="route.fit"`)
		_, _ = w.Write([]byte("fit-data"))
	}))
	defer server.Close()

	resolved, err := New(server.Client()).Resolve(context.Background(), importer.Spec{Source: server.URL + "/download"})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()

	if resolved.Filename != "route.fit" {
		t.Fatalf("filename = %q", resolved.Filename)
	}
}

func TestMatchOnlyAcceptsLocalPathsAndDirectTrailFileURLs(t *testing.T) {
	provider := New(nil)

	if match := provider.Match("/tmp/trail.gpx"); !match.OK {
		t.Fatalf("local file should match")
	}
	if match := provider.Match("https://example.com/trail.gpx"); !match.OK {
		t.Fatalf("direct trail URL should match")
	}
	if match := provider.Match("https://example.com/trail-page"); match.OK {
		t.Fatalf("HTML page should not match direct provider")
	}
}
