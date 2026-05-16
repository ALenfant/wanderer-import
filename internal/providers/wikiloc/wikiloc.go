package wikiloc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/gpxlinks"
	"wanderer-import/internal/providers/providerkit"
)

type Provider struct {
	*gpxlinks.Provider
}

func New(httpClient *http.Client) *Provider {
	return NewWithOptions(gpxlinks.Options{HTTPClient: httpClient})
}

func NewWithOptions(opts gpxlinks.Options) *Provider {
	return &Provider{
		Provider: gpxlinks.NewProviderWithOptions(gpxlinks.Config{
			ID:                 "wikiloc",
			Name:               "Wikiloc",
			Domains:            []string{"wikiloc.com"},
			AllowExternalLinks: true,
			Score:              90,
		}, opts),
	}
}

func (p *Provider) Match(source string) importer.Match {
	if parsed, ok := providerkit.ParseHTTPURL(source); ok {
		if providerkit.HostMatches(parsed.Hostname(), []string{"wikiloc.com"}) {
			return importer.Match{OK: true, Score: 90, Reason: "Wikiloc trail page"}
		}
	}
	return importer.Match{}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	sourceURL, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("wikiloc requires an HTTP URL")
	}

	// Extract trail ID from the URL. Wikiloc URLs come in two forms:
	// /hiking-trails/151551929           → last segment is the ID
	// /itineraires-randonnee/name-name-60586940 → ID is after the last dash
	pathSegments := strings.Split(strings.TrimRight(sourceURL.Path, "/"), "/")
	lastSegment := pathSegments[len(pathSegments)-1]

	// The ID is either the whole last segment (if numeric) or after the last dash.
	idStr := lastSegment
	if dashParts := strings.Split(lastSegment, "-"); len(dashParts) > 1 {
		idStr = dashParts[len(dashParts)-1]
	}

	if idStr != "" {
		resolved, err := p.resolveViaAPI(ctx, source, idStr)
		if err == nil && resolved != nil {
			return resolved, nil
		}
	}

	// Fall back to browser fetcher if available.
	if p.Provider.BrowserFetcher != nil {
		resolved, err := p.resolveViaBrowser(ctx, source)
		if err == nil && resolved != nil {
			return resolved, nil
		}
	}

	// Last resort: gpxlinks engine.
	return p.Provider.Resolve(ctx, spec)
}

func (p *Provider) resolveViaAPI(ctx context.Context, source, idStr string) (*importer.ResolvedTrail, error) {
	signedClient := &http.Client{
		Transport: &ApiSigningTransport{
			Transport: http.DefaultTransport,
			Token:     "",
		},
	}

	apiURL := "https://www.wikiloc.com/wikiloc/api2/trail/" + idStr + "/preview/open"
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := signedClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wikiloc API returned %d for trail %s", resp.StatusCode, idStr)
	}

	var response struct {
		Geom string `json:"geom"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}
	if response.Geom == "" {
		return nil, fmt.Errorf("wikiloc API returned empty geom for trail %s", idStr)
	}

	data, err := base64.StdEncoding.DecodeString(response.Geom)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	points, err := ParseTWKBLineString(data)
	if err != nil {
		return nil, fmt.Errorf("TWKB parse failed: %w", err)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("TWKB returned zero points for trail %s", idStr)
	}

	metadata := providerkit.MetadataFromPoints(points)
	name := "wikiloc-" + idStr
	body, err := providerkit.GPXReadCloser(name, points)
	if err != nil {
		return nil, err
	}

	return &importer.ResolvedTrail{
		Source:   source,
		Filename: providerkit.SlugFilename(name, ".gpx"),
		Body:     body,
		Metadata: metadata,
	}, nil
}

func (p *Provider) resolveViaBrowser(ctx context.Context, source string) (*importer.ResolvedTrail, error) {
	data, err := p.Provider.BrowserFetcher.Fetch(ctx, source, source, browserfetch.RequestOptions{
		Script: `() => {
			if (window.mapData && window.mapData.length > 0) return JSON.stringify(window.mapData);
			const findCoords = (obj) => {
				if (!obj || typeof obj !== "object") return null;
				if (Array.isArray(obj) && obj.length > 10 && obj[0].lat && (obj[0].lng || obj[0].lon)) return obj;
				for (const key in obj) {
					if (key === "parent") continue;
					try { const res = findCoords(obj[key]); if (res) return res; } catch(e) {}
				}
				return null;
			};
			const coords = findCoords(window);
			if (coords) return JSON.stringify(coords);
			return document.documentElement.outerHTML;
		}`,
	})
	if err != nil || len(data) == 0 || data[0] != '[' {
		return nil, fmt.Errorf("browser fetch did not return coordinate JSON")
	}

	var rawPoints []struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
		Lon float64 `json:"lon"`
		Ele float64 `json:"ele"`
		Alt float64 `json:"alt"`
	}
	if err := json.Unmarshal(data, &rawPoints); err != nil || len(rawPoints) == 0 {
		return nil, fmt.Errorf("failed to parse browser coordinate data")
	}

	points := make([]providerkit.Point, 0, len(rawPoints))
	for _, rp := range rawPoints {
		pt := providerkit.Point{Lat: rp.Lat, Lon: rp.Lng}
		if pt.Lon == 0 {
			pt.Lon = rp.Lon
		}
		ele := rp.Ele
		if ele == 0 {
			ele = rp.Alt
		}
		if ele != 0 {
			pt.Ele = &ele
		}
		points = append(points, pt)
	}

	metadata := providerkit.MetadataFromPoints(points)
	name := "wikiloc-" + source
	body, err := providerkit.GPXReadCloser(name, points)
	if err != nil {
		return nil, err
	}

	return &importer.ResolvedTrail{
		Source:   source,
		Filename: providerkit.SlugFilename(name, ".gpx"),
		Body:     body,
		Metadata: metadata,
	}, nil
}
