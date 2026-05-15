package urltemplate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

type IDExtractor func(*url.URL) (string, bool)

type Config struct {
	ID        string
	Name      string
	Domains   []string
	Score     int
	Templates []string
	ExtractID IDExtractor
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
		Engine:  "urltemplate",
		Domains: append([]string(nil), p.cfg.Domains...),
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), p.cfg.Domains) {
		return importer.Match{}
	}
	if p.cfg.ExtractID != nil {
		if _, ok := p.cfg.ExtractID(parsed); !ok {
			return importer.Match{}
		}
	}
	return importer.Match{OK: true, Score: p.cfg.Score, Reason: "provider-specific download URL template"}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	parsed, ok := providerkit.ParseHTTPURL(strings.TrimSpace(spec.Source))
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}

	id := ""
	if p.cfg.ExtractID != nil {
		var ok bool
		id, ok = p.cfg.ExtractID(parsed)
		if !ok {
			return nil, fmt.Errorf("%s could not extract an ID from %s", p.Name(), spec.Source)
		}
	}
	metadata := p.fetchSourceMetadata(ctx, parsed.String())

	var lastErr error
	for _, template := range p.cfg.Templates {
		candidate := strings.ReplaceAll(template, "{id}", url.PathEscape(id))
		download, err := providerkit.DownloadVerifiedTrail(ctx, p.httpClient, candidate)
		if err == nil {
			return &importer.ResolvedTrail{
				Source:   download.Source,
				Filename: download.Filename,
				Body:     download.Body,
				Metadata: wanderer.MergeTrailUpdates(download.Metadata, metadata),
			}, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%s has no download templates configured", p.Name())
}

func (p *Provider) fetchSourceMetadata(ctx context.Context, source string) wanderer.TrailUpdate {
	res, err := providerkit.GET(ctx, p.httpClient, source)
	if err != nil {
		return wanderer.TrailUpdate{}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return wanderer.TrailUpdate{}
	}
	base, _ := providerkit.ParseHTTPURL(source)
	return providerkit.ExtractHTMLMetadata(base, data)
}
