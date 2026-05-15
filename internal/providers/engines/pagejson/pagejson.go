package pagejson

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/jsonpoints"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

type Config struct {
	ID         string
	Name       string
	Domains    []string
	Score      int
	URLPattern *regexp.Regexp
	Parse      jsonpoints.Parser
}

type Provider struct {
	cfg        Config
	httpClient *http.Client
}

func NewProvider(cfg Config, httpClient *http.Client) *Provider {
	if cfg.Score == 0 {
		cfg.Score = 90
	}
	return &Provider{cfg: cfg, httpClient: providerkit.HTTPClient(httpClient)}
}

func (p *Provider) Name() string {
	return p.cfg.ID
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      p.cfg.ID,
		Name:    p.cfg.Name,
		Engine:  "pagejson",
		Domains: append([]string(nil), p.cfg.Domains...),
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), p.cfg.Domains) {
		return importer.Match{}
	}
	return importer.Match{OK: true, Score: p.cfg.Score, Reason: "page-linked JSON coordinates"}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	base, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}
	if p.cfg.URLPattern == nil || p.cfg.Parse == nil {
		return nil, fmt.Errorf("%s is missing page JSON configuration", p.Name())
	}

	jsonURL, sourceMetadata, err := p.findJSONURL(ctx, base, source)
	if err != nil {
		return nil, err
	}
	res, err := providerkit.GET(ctx, p.httpClient, jsonURL)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	points, err := p.cfg.Parse(data)
	if err != nil {
		return nil, err
	}
	metadata := wanderer.MergeTrailUpdates(providerkit.MetadataFromPoints(points), sourceMetadata)

	name := p.cfg.ID
	effectiveMetadata := wanderer.MergeTrailUpdates(metadata, spec.Update)
	if effectiveMetadata.Name != nil && strings.TrimSpace(*effectiveMetadata.Name) != "" {
		name = strings.TrimSpace(*effectiveMetadata.Name)
	}
	body, err := providerkit.GPXReadCloser(name, points)
	if err != nil {
		return nil, err
	}
	return &importer.ResolvedTrail{
		Source:   jsonURL,
		Filename: providerkit.SlugFilename(p.cfg.ID, ".gpx"),
		Body:     body,
		Metadata: metadata,
	}, nil
}

func (p *Provider) findJSONURL(ctx context.Context, base *url.URL, source string) (string, wanderer.TrailUpdate, error) {
	res, err := providerkit.GET(ctx, p.httpClient, source)
	if err != nil {
		return "", wanderer.TrailUpdate{}, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", wanderer.TrailUpdate{}, err
	}
	metadata := providerkit.ExtractHTMLMetadata(base, data)
	text := strings.ReplaceAll(string(data), `\/`, `/`)
	text = strings.ReplaceAll(text, `\u002F`, `/`)
	match := p.cfg.URLPattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return "", metadata, fmt.Errorf("%s found no JSON route URL in %s", p.Name(), source)
	}
	resolved, ok := providerkit.ResolveReference(base, match[1])
	if !ok {
		return "", metadata, fmt.Errorf("%s found invalid JSON route URL %q", p.Name(), match[1])
	}
	return resolved, metadata, nil
}
