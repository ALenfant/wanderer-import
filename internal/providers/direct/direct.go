package direct

import (
	"context"
	"net/http"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/directfile"
)

type Provider struct {
	engine *directfile.Engine
}

func New(httpClient *http.Client) *Provider {
	return &Provider{engine: directfile.New(httpClient)}
}

func (p *Provider) Name() string {
	return "direct"
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:     p.Name(),
		Name:   "Direct file or trail-file URL",
		Engine: "directfile",
		Status: "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	return p.engine.Match(source)
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	return p.engine.Resolve(ctx, spec)
}
