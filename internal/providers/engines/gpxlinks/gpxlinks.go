package gpxlinks

import (
	"context"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"

	nethtml "golang.org/x/net/html"
)

const maxPageBytes = 8 << 20

var (
	urlPattern = regexp.MustCompile(`(?is)(https?:\\?/\\?/[^"'\s<>]+|/[a-z0-9_./%?&=;:+#,-]+|\.\.?/[a-z0-9_./%?&=;:+#,-]+)`)
)

type Config struct {
	ID                 string
	Name               string
	Domains            []string
	AllowAnyDomain     bool
	AllowExternalLinks bool
	Score              int
}

type Provider struct {
	cfg            Config
	httpClient     *http.Client
	BrowserFetcher browserfetch.Fetcher
}

func NewProvider(cfg Config, httpClient *http.Client) *Provider {
	return NewProviderWithOptions(cfg, Options{HTTPClient: httpClient})
}

type Options struct {
	HTTPClient     *http.Client
	BrowserFetcher browserfetch.Fetcher
}

func NewProviderWithOptions(cfg Config, opts Options) *Provider {
	if cfg.Score == 0 {
		if cfg.AllowAnyDomain {
			cfg.Score = 50
		} else {
			cfg.Score = 80
		}
	}
	return &Provider{
		cfg:            cfg,
		httpClient:     providerkit.HTTPClient(opts.HTTPClient),
		BrowserFetcher: opts.BrowserFetcher,
	}
}

func (p *Provider) Name() string {
	return p.cfg.ID
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      p.cfg.ID,
		Name:    p.cfg.Name,
		Engine:  "gpxlinks",
		Domains: append([]string(nil), p.cfg.Domains...),
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return importer.Match{}
	}
	if p.cfg.AllowAnyDomain || providerkit.HostMatches(parsed.Hostname(), p.cfg.Domains) {
		if providerkit.HasTrailFileExtension(source) {
			return importer.Match{}
		}
		return importer.Match{OK: true, Score: p.cfg.Score, Reason: "page may expose trail-file links"}
	}
	return importer.Match{}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	base, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}

	page, err := p.AnalyzePage(ctx, source)
	if err != nil {
		return nil, err
	}
	links := page.Links
	if len(links) == 0 {
		return nil, fmt.Errorf("%s found no GPX/KML/GeoJSON links in %s", p.Name(), source)
	}

	sortTrailLinks(links)
	var lastErr error
	for _, link := range links {
		if !p.cfg.AllowExternalLinks && !sameHostOrSubdomain(base, link) {
			continue
		}
		download, err := providerkit.DownloadVerifiedTrail(ctx, p.httpClient, link)
		if err == nil {
			return &importer.ResolvedTrail{
				Source:   download.Source,
				Filename: download.Filename,
				Body:     download.Body,
				Metadata: wanderer.MergeTrailUpdates(download.Metadata, page.Metadata),
			}, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%s found no same-host trail-file links in %s", p.Name(), source)
}

func (p *Provider) FindTrailLinks(ctx context.Context, source string) ([]string, error) {
	page, err := p.AnalyzePage(ctx, source)
	if err != nil {
		return nil, err
	}
	return page.Links, nil
}

type PageAnalysis struct {
	Links    []string
	Metadata wanderer.TrailUpdate
}

func (p *Provider) AnalyzePage(ctx context.Context, source string) (*PageAnalysis, error) {
	base, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("invalid HTTP URL %q", source)
	}
	var data []byte
	res, err := providerkit.GET(ctx, p.httpClient, source)
	if err == nil {
		defer res.Body.Close()
		data, err = io.ReadAll(io.LimitReader(res.Body, maxPageBytes))
	}
	if err != nil || len(data) == 0 {
		if p.BrowserFetcher != nil {
			data, err = p.BrowserFetcher.Fetch(ctx, source, source, browserfetch.RequestOptions{})
		}
		if err != nil {
			return nil, err
		}
	}
	return &PageAnalysis{
		Links:    ExtractTrailLinks(base, data),
		Metadata: providerkit.ExtractHTMLMetadata(base, data),
	}, nil
}

func ExtractTrailLinks(base *url.URL, data []byte) []string {
	text := stdhtml.UnescapeString(string(data))
	text = strings.ReplaceAll(text, `\/`, `/`)
	text = strings.ReplaceAll(text, `\u002F`, `/`)
	text = strings.ReplaceAll(text, `\u0026`, `&`)

	seen := map[string]struct{}{}
	add := func(candidate string) {
		resolved, ok := providerkit.ResolveReference(base, candidate)
		if !ok || !providerkit.HasTrailFileExtension(resolved) {
			return
		}
		seen[resolved] = struct{}{}
	}

	for _, value := range htmlAttributeValues(text) {
		add(value)
	}
	for _, match := range urlPattern.FindAllStringSubmatch(text, -1) {
		add(match[1])
	}

	links := make([]string, 0, len(seen))
	for link := range seen {
		links = append(links, link)
	}
	sort.Strings(links)
	return links
}

func htmlAttributeValues(text string) []string {
	tokenizer := nethtml.NewTokenizer(strings.NewReader(text))
	var values []string
	for {
		switch tokenizer.Next() {
		case nethtml.ErrorToken:
			return values
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			token := tokenizer.Token()
			for _, attr := range token.Attr {
				key := strings.ToLower(attr.Key)
				if key == "href" || key == "src" || key == "content" || strings.HasPrefix(key, "data-") {
					values = append(values, attr.Val)
				}
			}
		}
	}
}

func sortTrailLinks(links []string) {
	rank := func(link string) int {
		switch providerkit.TrailFileExtension(link) {
		case ".gpx":
			return 0
		case ".kml":
			return 1
		case ".kmz":
			return 2
		case ".geojson":
			return 3
		default:
			return 9
		}
	}
	sort.SliceStable(links, func(i, j int) bool {
		ri, rj := rank(links[i]), rank(links[j])
		if ri != rj {
			return ri < rj
		}
		return links[i] < links[j]
	})
}

func sameHostOrSubdomain(base *url.URL, link string) bool {
	parsed, ok := providerkit.ParseHTTPURL(link)
	if !ok {
		return false
	}
	baseHost := base.Hostname()
	linkHost := parsed.Hostname()
	return baseHost == linkHost ||
		strings.HasSuffix(linkHost, "."+baseHost) ||
		strings.HasSuffix(baseHost, "."+linkHost)
}
