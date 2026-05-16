package visorando

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/gpxlinks"
	"wanderer-import/internal/providers/providerkit"
)

const (
	googlebotUserAgent = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
)

type Provider struct {
	*gpxlinks.Provider
	httpClient *http.Client
}

func New(httpClient *http.Client) *Provider {
	return NewWithOptions(gpxlinks.Options{HTTPClient: httpClient})
}

func NewWithOptions(opts gpxlinks.Options) *Provider {
	return &Provider{
		Provider: gpxlinks.NewProviderWithOptions(gpxlinks.Config{
			ID:                 "visorando",
			Name:               "Visorando",
			Domains:            []string{"visorando.com"},
			AllowExternalLinks: true,
			Score:              85,
		}, opts),
		httpClient: providerkit.HTTPClient(opts.HTTPClient),
	}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	if _, ok := providerkit.ParseHTTPURL(source); !ok {
		return nil, fmt.Errorf("visorando requires an HTTP URL")
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	req.Header.Set("User-Agent", googlebotUserAgent)
	res, err := p.httpClient.Do(req)
	if err == nil {
		defer res.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		
		name := extractName(data)
		
		if id := extractHikeID(data); id != "" {
			apiURL := fmt.Sprintf("https://www.visorando.com/index.php?component=exportData&task=getRandoGeoJson&wholePointsData=1&idRandonnee=%s", id)
			reqAPI, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
			reqAPI.Header.Set("User-Agent", googlebotUserAgent)
			resAPI, err := p.httpClient.Do(reqAPI)
			if err == nil {
				defer resAPI.Body.Close()
				var payload struct {
					GeoJSON json.RawMessage `json:"geojson"`
				}
				if err := json.NewDecoder(resAPI.Body).Decode(&payload); err == nil {
					points, _, err := providerkit.ParseGeoJSON(payload.GeoJSON)
					if err == nil && len(points) > 0 {
						if name == "" {
							name = "visorando-" + id
						}
						body, err := providerkit.GPXReadCloser(name, points)
						if err == nil {
							metadata := providerkit.MetadataFromPoints(points)
							metadata.Name = &name
							return &importer.ResolvedTrail{
								Source:   source,
								Filename: providerkit.SlugFilename(name, ".gpx"),
								Body:     body,
								Metadata: metadata,
							}, nil
						}
					}
				}
			}
		}
	}

	return p.Provider.Resolve(ctx, spec)
}

func extractName(data []byte) string {
	s := string(data)
	reTitle := regexp.MustCompile(`(?i)<title>(.*?)</title>`)
	if match := reTitle.FindStringSubmatch(s); len(match) > 1 {
		title := strings.TrimSpace(match[1])
		title = html.UnescapeString(title)
		title = strings.Split(title, " - ")[0]
		title = strings.Split(title, " | ")[0]
		title = strings.TrimPrefix(title, "A faire : ")
		return title
	}
	return ""
}

func extractHikeID(data []byte) string {
	s := string(data)
	re := regexp.MustCompile(`idRando\s*=\s*(\d+)`)
	if match := re.FindStringSubmatch(s); len(match) > 1 {
		return match[1]
	}
	return ""
}
