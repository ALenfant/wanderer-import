package trailsviewer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
)

func TestParsePointsDecodesShiftedCoordinates(t *testing.T) {
	points, err := ParsePoints([]byte(`[
		{"latitude":"9s0357","longitude":"jd5yc","elevation":"hrmf"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 {
		t.Fatalf("point count = %d", len(points))
	}
	assertClose(t, "lat", points[0].Lat, 43.77778)
	assertClose(t, "lon", points[0].Lon, 3.28954)
	if points[0].Ele == nil {
		t.Fatal("elevation is nil")
	}
	assertClose(t, "ele", *points[0].Ele, 303.1)
}

func TestResolveFetchesPageAndPointsAPI(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/trail-lx94s/Les-corniches-du-Lauroux/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
			<title>Les corniches du Lauroux</title>
			<meta name="description" content="Distance : 13,3 Km • Dénivelé positif : 678 m">
			<script>VERSION="202604212208";appTime="pbys3qw";appTrail={"id":"lx94s","title":"Les corniches du Lauroux"}</script>
		`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("_path"); got != "api.trails.getPoints" {
			t.Fatalf("_path = %q", got)
		}
		if got := r.URL.Query().Get("trail"); got != "lx94s" {
			t.Fatalf("trail = %q", got)
		}
		if got, want := r.Header.Get("Token"), encodeToken(1778848410); got != want {
			t.Fatalf("token = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(`[
			{"latitude":"9s0357","longitude":"jd5yc","elevation":"hrmf"},
			{"latitude":"9s0357","longitude":"jd5yc","elevation":"hrmf"}
		]`))
	})

	provider := New(server.Client())
	source := server.URL + "/trail-lx94s/Les-corniches-du-Lauroux/"
	resolved, err := provider.Resolve(context.Background(), importer.Spec{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	if !strings.Contains(resolved.Source, "_path=api.trails.getPoints") {
		t.Fatalf("source = %q", resolved.Source)
	}
	if resolved.Metadata.Name == nil || !strings.Contains(*resolved.Metadata.Name, "Les corniches") {
		t.Fatalf("name = %#v", resolved.Metadata.Name)
	}
}

func TestResolveFallsBackToBrowserFetcherOnForbiddenPointsAPI(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/trail-lx94s/Les-corniches-du-Lauroux/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
			<title>Les corniches du Lauroux</title>
			<script>VERSION="202604212208";appTime="pbys3qw";appTrail={"id":"lx94s","title":"Les corniches du Lauroux"}</script>
		`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	fetcher := &fakeBrowserFetcher{body: []byte(`[
		{"latitude":"9s0357","longitude":"jd5yc","elevation":"hrmf"},
		{"latitude":"9s0357","longitude":"jd5yc","elevation":"hrmf"}
	]`)}
	provider := NewWithOptions(Options{HTTPClient: server.Client(), BrowserFetcher: fetcher})
	source := server.URL + "/trail-lx94s/Les-corniches-du-Lauroux/"
	resolved, err := provider.Resolve(context.Background(), importer.Spec{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	if fetcher.pageURL != source {
		t.Fatalf("browser pageURL = %q, want %q", fetcher.pageURL, source)
	}
	if !strings.Contains(fetcher.requestURL, "_path=api.trails.getPoints") {
		t.Fatalf("browser requestURL = %q", fetcher.requestURL)
	}
	if got, want := fetcher.opts.Headers["Token"], encodeToken(1778848410); got != want {
		t.Fatalf("browser token = %q, want %q", got, want)
	}
	if fetcher.opts.Cookies["consents"] == "" || fetcher.opts.Cookies["FCCDCF"] == "" {
		t.Fatalf("browser cookies = %#v", fetcher.opts.Cookies)
	}
}

func TestPageAppTimeDecodesTokenSeed(t *testing.T) {
	got, ok := pageAppTime([]byte(`appTime="pbys3qw"`))
	if !ok {
		t.Fatal("appTime not found")
	}
	if got != 1778848410 {
		t.Fatalf("appTime = %d", got)
	}
}

type fakeBrowserFetcher struct {
	pageURL    string
	requestURL string
	opts       browserfetch.RequestOptions
	body       []byte
	err        error
}

func (f *fakeBrowserFetcher) Fetch(_ context.Context, pageURL, requestURL string, opts browserfetch.RequestOptions) ([]byte, error) {
	f.pageURL = pageURL
	f.requestURL = requestURL
	f.opts = opts
	if f.err != nil {
		return nil, f.err
	}
	if f.body == nil {
		return nil, fmt.Errorf("missing fake browser body")
	}
	return f.body, nil
}

func assertClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	delta := got - want
	if delta < 0 {
		delta = -delta
	}
	if delta > 0.00001 {
		t.Fatalf("%s = %f, want %f", name, got, want)
	}
}
