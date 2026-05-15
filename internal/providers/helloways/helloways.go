package helloways

import (
	"context"
	"encoding/json"
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

const maxPageBytes = 8 << 20

var trackIDPattern = regexp.MustCompile(`/tracks/([0-9a-f]{12,})/`)

type Provider struct {
	httpClient *http.Client
}

func New(httpClient *http.Client) *Provider {
	return &Provider{httpClient: providerkit.HTTPClient(httpClient)}
}

func (p *Provider) Name() string {
	return "helloways"
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      "helloways",
		Name:    "Helloways",
		Engine:  "jsonpoints",
		Domains: []string{"helloways.com"},
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), []string{"helloways.com"}) {
		return importer.Match{}
	}
	if strings.Contains(parsed.Path, "/hike/") || strings.Contains(parsed.Path, "/randonnees/") {
		return importer.Match{OK: true, Score: 90, Reason: "public Helloways track JSON"}
	}
	return importer.Match{}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	base, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}

	page, err := p.fetch(ctx, source, maxPageBytes)
	if err != nil {
		return nil, err
	}
	metadata := providerkit.ExtractHTMLMetadata(base, page)
	id, ok := extractTrackID(page)
	if !ok {
		return nil, fmt.Errorf("%s found no public track ID in %s", p.Name(), source)
	}

	apiURL := base.ResolveReference(&url.URL{Path: "/api/tracks/" + id}).String()
	data, err := p.fetch(ctx, apiURL, 0)
	if err != nil {
		return nil, err
	}
	points, err := parseTrackPoints(data)
	if err != nil {
		return nil, err
	}
	metadata = wanderer.MergeTrailUpdates(providerkit.MetadataFromPoints(points), metadata)
	metadata = wanderer.MergeTrailUpdates(metadata, parseTrackMetadata(data))

	name := p.Name()
	effectiveMetadata := wanderer.MergeTrailUpdates(metadata, spec.Update)
	if effectiveMetadata.Name != nil && strings.TrimSpace(*effectiveMetadata.Name) != "" {
		name = strings.TrimSpace(*effectiveMetadata.Name)
	}
	body, err := providerkit.GPXReadCloser(name, points)
	if err != nil {
		return nil, err
	}
	return &importer.ResolvedTrail{
		Source:   apiURL,
		Filename: providerkit.SlugFilename("helloways-"+id, ".gpx"),
		Body:     body,
		Metadata: metadata,
	}, nil
}

func (p *Provider) fetch(ctx context.Context, source string, limit int64) ([]byte, error) {
	res, err := providerkit.GET(ctx, p.httpClient, source)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	reader := io.Reader(res.Body)
	if limit > 0 {
		reader = io.LimitReader(res.Body, limit)
	}
	return io.ReadAll(reader)
}

func extractTrackID(data []byte) (string, bool) {
	match := trackIDPattern.FindSubmatch(data)
	if len(match) != 2 {
		return "", false
	}
	return string(match[1]), true
}

func parseTrackPoints(data []byte) ([]providerkit.Point, error) {
	var payload struct {
		Path json.RawMessage `json:"path"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if len(payload.Path) == 0 {
		return nil, fmt.Errorf("Helloways response had no path")
	}
	return jsonpoints.ParseGeoJSONLineString("lonlat")(payload.Path)
}

func parseTrackMetadata(data []byte) wanderer.TrailUpdate {
	var payload struct {
		Name       string  `json:"name"`
		RouteDist  float64 `json:"routeDist"`
		RoutePosHD float64 `json:"routePosHD"`
		RouteNegHD float64 `json:"routeNegHD"`
		Duration   struct {
			Walk float64 `json:"walk"`
		} `json:"duration"`
		Address struct {
			City    string `json:"city"`
			Region  string `json:"region"`
			Country string `json:"country"`
		} `json:"address"`
		Pictures []struct {
			URL string `json:"url"`
		} `json:"pictures"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return wanderer.TrailUpdate{}
	}
	update := wanderer.TrailUpdate{}
	if strings.TrimSpace(payload.Name) != "" {
		update.Name = stringPtr(payload.Name)
	}
	if payload.RouteDist > 0 {
		distance := payload.RouteDist * 1000
		update.Distance = &distance
	}
	if payload.RoutePosHD > 0 {
		update.ElevationGain = &payload.RoutePosHD
	}
	if payload.RouteNegHD > 0 {
		update.ElevationLoss = &payload.RouteNegHD
	}
	if payload.Duration.Walk > 0 {
		duration := payload.Duration.Walk * 3600
		update.Duration = &duration
	}
	locationParts := nonEmpty(payload.Address.City, payload.Address.Region, payload.Address.Country)
	if len(locationParts) > 0 {
		location := strings.Join(locationParts, ", ")
		update.Location = &location
	}
	for _, picture := range payload.Pictures {
		if strings.TrimSpace(picture.URL) != "" {
			update.PhotoURLs = append(update.PhotoURLs, picture.URL)
		}
	}
	return update
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	return &value
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
