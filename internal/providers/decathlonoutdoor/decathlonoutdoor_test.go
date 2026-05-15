package decathlonoutdoor

import (
	"net/url"
	"testing"
)

func TestExtractID(t *testing.T) {
	parsed, err := url.Parse("https://www.decathlon-outdoor.com/fr-fr/explore/france/boucle-forestiere-autour-de-la-cascade-de-la-vis-642fb3af36a98")
	if err != nil {
		t.Fatal(err)
	}
	id, ok := extractID(parsed)
	if !ok {
		t.Fatal("expected route ID")
	}
	if id != "642fb3af36a98" {
		t.Fatalf("id = %q, want 642fb3af36a98", id)
	}
}

func TestFirstRouteURL(t *testing.T) {
	base, err := url.Parse("https://www.decathlon-outdoor.com/fr-fr/inspire/france/randonnee-cascade-de-la-vis-gard")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`"\u002Ffr-fr\u002Fexplore\u002Ffrance\u002Fboucle-forestiere-autour-de-la-cascade-de-la-vis-642fb3af36a98"`)
	routeURL, ok := firstRouteURL(base, data)
	if !ok {
		t.Fatal("expected embedded route URL")
	}
	want := "https://www.decathlon-outdoor.com/fr-fr/explore/france/boucle-forestiere-autour-de-la-cascade-de-la-vis-642fb3af36a98"
	if routeURL != want {
		t.Fatalf("routeURL = %q, want %q", routeURL, want)
	}
}
