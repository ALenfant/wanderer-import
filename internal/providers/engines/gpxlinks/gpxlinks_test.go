package gpxlinks

import (
	"net/url"
	"testing"
)

func TestExtractTrailLinksUsesHTMLAttributesAndJSONEscapedURLs(t *testing.T) {
	base, err := url.Parse("https://example.com/trails/page")
	if err != nil {
		t.Fatal(err)
	}

	links := ExtractTrailLinks(base, []byte(`
		<a href="/downloads/route.gpx">GPX</a>
		<script>{"kml":"https:\/\/cdn.example.com\/route.kml"}</script>
	`))

	want := map[string]bool{
		"https://example.com/downloads/route.gpx": true,
		"https://cdn.example.com/route.kml":       true,
	}
	if len(links) != len(want) {
		t.Fatalf("links = %#v", links)
	}
	for _, link := range links {
		if !want[link] {
			t.Fatalf("unexpected link %q in %#v", link, links)
		}
	}
}
