package wikiloc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/gpxlinks"
	"wanderer-import/internal/providers/providerkit"
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
			ID:                 "wikiloc",
			Name:               "Wikiloc",
			Domains:            []string{"wikiloc.com"},
			AllowExternalLinks: true,
			Score:              90,
		}, opts),
		httpClient: providerkit.HTTPClient(opts.HTTPClient),
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

	pathSegments := strings.Split(strings.TrimRight(sourceURL.Path, "/"), "/")
	lastSegment := pathSegments[len(pathSegments)-1]

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

	if p.Provider.BrowserFetcher != nil {
		resolved, err := p.resolveViaBrowser(ctx, source)
		if err == nil && resolved != nil {
			return resolved, nil
		}
	}

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

	var response map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	geom, _ := response["geom"].(string)
	if geom == "" {
		return nil, fmt.Errorf("wikiloc API returned empty geom for trail %s", idStr)
	}

	data, err := base64.StdEncoding.DecodeString(geom)
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
	
	if name, ok := response["name"].(string); ok && name != "" {
		metadata.Name = &name
	} else if name, ok := response["title"].(string); ok && name != "" {
		metadata.Name = &name
	} else {
		// Fallback: fetch page for name
		if name := p.fetchNameFromPage(ctx, source); name != "" {
			metadata.Name = &name
		}
	}

	name := "wikiloc-" + idStr
	if metadata.Name != nil {
		name = *metadata.Name
	}
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

const (
	googlebotUserAgent = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
)

func (p *Provider) fetchNameFromPage(ctx context.Context, source string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	req.Header.Set("User-Agent", googlebotUserAgent)
	res, err := p.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	s := string(data)
	re := regexp.MustCompile(`(?i)<title>(.*?)</title>`)
	if match := re.FindStringSubmatch(s); len(match) > 1 {
		title := strings.TrimSpace(match[1])
		title = strings.Split(title, " | ")[0]
		title = strings.Split(title, " - ")[0]
		return title
	}
	return ""
}

func (p *Provider) resolveViaBrowser(ctx context.Context, source string) (*importer.ResolvedTrail, error) {
	data, err := p.Provider.BrowserFetcher.Fetch(ctx, source, source, browserfetch.RequestOptions{
		Script: `() => {
			const result = {
				name: document.title.split("|")[0].trim().split(" - ")[0].trim(),
				points: []
			};
			if (window.mapData && window.mapData.length > 0) {
				result.points = window.mapData;
				return JSON.stringify(result);
			}
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
			if (coords) {
				result.points = coords;
				return JSON.stringify(result);
			}
			return document.documentElement.outerHTML;
		}`,
	})
	if err != nil || len(data) == 0 || data[0] != '{' {
		return nil, fmt.Errorf("browser fetch did not return coordinate JSON")
	}

	var browserPayload struct {
		Name   string `json:"name"`
		Points []struct {
			Lat float64 `json:"lat"`
			Lng float64 `json:"lng"`
			Lon float64 `json:"lon"`
			Ele float64 `json:"ele"`
			Alt float64 `json:"alt"`
		} `json:"points"`
	}
	if err := json.Unmarshal(data, &browserPayload); err != nil || len(browserPayload.Points) == 0 {
		return nil, fmt.Errorf("failed to parse browser coordinate data")
	}

	points := make([]providerkit.Point, 0, len(browserPayload.Points))
	for _, rp := range browserPayload.Points {
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
	if browserPayload.Name != "" {
		metadata.Name = &browserPayload.Name
	}

	name := "wikiloc-" + source
	if metadata.Name != nil {
		name = *metadata.Name
	}
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
