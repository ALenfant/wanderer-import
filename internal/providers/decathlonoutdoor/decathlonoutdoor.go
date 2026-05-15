package decathlonoutdoor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/jsonpoints"
	"wanderer-import/internal/providers/providerkit"
)

const maxPageBytes = 8 << 20

var (
	referencePattern = regexp.MustCompile(`(?i)([0-9a-f]{12,})$`)
	routeLinkPattern = regexp.MustCompile(`(?i)\\?/fr-fr\\?/explore\\?/[^"'<> ]+?-([0-9a-f]{12,})`)
)

type Provider struct {
	routeProvider *jsonpoints.Provider
	httpClient    *http.Client
}

func New(httpClient *http.Client) *Provider {
	return &Provider{
		routeProvider: jsonpoints.NewProvider(jsonpoints.Config{
			ID:      "decathlon-outdoor",
			Name:    "Decathlon Outdoor",
			Domains: []string{"decathlon-outdoor.com"},
			Score:   90,
			Templates: []string{
				"https://www.decathlon-outdoor.com/bff/route/{id}/geojson",
			},
			ExtractID: extractID,
			Parse:     jsonpoints.ParseGeoJSONLineString("lonlat"),
		}, httpClient),
		httpClient: providerkit.HTTPClient(httpClient),
	}
}

func (p *Provider) Name() string {
	return "decathlon-outdoor"
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      "decathlon-outdoor",
		Name:    "Decathlon Outdoor",
		Engine:  "jsonpoints",
		Domains: []string{"decathlon-outdoor.com"},
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	if match := p.routeProvider.Match(source); match.OK {
		return match
	}
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), []string{"decathlon-outdoor.com"}) {
		return importer.Match{}
	}
	if strings.Contains(parsed.Path, "/inspire/") {
		return importer.Match{OK: true, Score: 88, Reason: "Decathlon inspiration page with embedded route links"}
	}
	return importer.Match{}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	if match := p.routeProvider.Match(spec.Source); match.OK {
		return p.routeProvider.Resolve(ctx, spec)
	}

	source := strings.TrimSpace(spec.Source)
	base, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}
	res, err := providerkit.GET(ctx, p.httpClient, source)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxPageBytes))
	if err != nil {
		return nil, err
	}
	routeURL, ok := firstRouteURL(base, data)
	if !ok {
		return nil, fmt.Errorf("%s found no embedded route link in %s", p.Name(), source)
	}
	return p.routeProvider.Resolve(ctx, importer.Spec{
		Source:           routeURL,
		IgnoreDuplicates: spec.IgnoreDuplicates,
		Update:           spec.Update,
	})
}

func extractID(parsed *url.URL) (string, bool) {
	if !strings.Contains(parsed.Path, "/explore/") && !strings.Contains(parsed.Path, "/solo/") {
		return "", false
	}
	segment := strings.TrimSuffix(path.Base(parsed.Path), "/")
	match := referencePattern.FindStringSubmatch(segment)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func firstRouteURL(base *url.URL, data []byte) (string, bool) {
	text := strings.ReplaceAll(string(data), `\u002F`, `/`)
	text = strings.ReplaceAll(text, `\/`, `/`)
	match := routeLinkPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", false
	}
	ref := match[0]
	resolved, ok := providerkit.ResolveReference(base, ref)
	return resolved, ok
}
