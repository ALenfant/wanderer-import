package alltrails

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

type Provider struct {
	httpClient *http.Client
}

func New(httpClient *http.Client) *Provider {
	return &Provider{httpClient: providerkit.HTTPClient(httpClient)}
}

func (p *Provider) Name() string {
	return "alltrails"
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      "alltrails",
		Name:    "AllTrails",
		Engine:  "alltrails-v3-json",
		Domains: []string{"alltrails.com"},
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	if parsed, ok := providerkit.ParseHTTPURL(source); ok {
		if providerkit.HostMatches(parsed.Hostname(), []string{"alltrails.com"}) {
			return importer.Match{OK: true, Score: 90, Reason: "AllTrails trail page or v3 trail JSON"}
		}
		return importer.Match{}
	}
	if strings.EqualFold(filepath.Ext(source), ".json") {
		return importer.Match{OK: true, Score: 5, Reason: "possible saved AllTrails v3 trail JSON"}
	}
	return importer.Match{}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	data, err := p.readSource(ctx, source)
	if err != nil {
		if parsed, ok := providerkit.ParseHTTPURL(source); ok && providerkit.HostMatches(parsed.Hostname(), []string{"alltrails.com"}) {
			return nil, fmt.Errorf("alltrails page/API fetch failed; AllTrails commonly blocks automated requests, so provide a saved /api/alltrails/v3/trails response JSON instead: %w", err)
		}
		return nil, err
	}
	points, metadata, err := ParseV3Trail(data)
	if err != nil {
		if parsed, ok := providerkit.ParseHTTPURL(source); ok && providerkit.HostMatches(parsed.Hostname(), []string{"alltrails.com"}) {
			return nil, fmt.Errorf("alltrails requires a saved /api/alltrails/v3/trails response JSON or a direct API response URL; public page fetch did not expose route JSON: %w", err)
		}
		return nil, err
	}

	name := "alltrails"
	if metadata.Name != nil && strings.TrimSpace(*metadata.Name) != "" {
		name = strings.TrimSpace(*metadata.Name)
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

func (p *Provider) readSource(ctx context.Context, source string) ([]byte, error) {
	if parsed, ok := providerkit.ParseHTTPURL(source); ok {
		res, err := providerkit.GET(ctx, p.httpClient, parsed.String())
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		return io.ReadAll(io.LimitReader(res.Body, 16<<20))
	}
	return os.ReadFile(source)
}

func ParseV3Trail(data []byte) ([]providerkit.Point, wanderer.TrailUpdate, error) {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, wanderer.TrailUpdate{}, err
	}
	polyline, ok := stringAt(payload,
		"trails", "0", "defaultMap", "routes", "0", "lineSegments", "0", "polyline", "pointsData",
	)
	if !ok {
		polyline, ok = stringAt(payload,
			"maps", "0", "routes", "0", "lineSegments", "0", "polyline", "pointsData",
		)
	}
	if !ok {
		return nil, wanderer.TrailUpdate{}, fmt.Errorf("AllTrails v3 JSON had no route polyline")
	}
	points, err := decodePolyline(polyline, 5)
	if err != nil {
		return nil, wanderer.TrailUpdate{}, err
	}
	if len(points) == 0 {
		return nil, wanderer.TrailUpdate{}, fmt.Errorf("AllTrails v3 JSON polyline had no points")
	}

	metadata := providerkit.MetadataFromPoints(points)
	if name, ok := firstStringAt(payload, [][]string{
		{"trails", "0", "name"},
		{"maps", "0", "name"},
	}); ok {
		metadata.Name = &name
	}
	if description, ok := firstStringAt(payload, [][]string{
		{"trails", "0", "description"},
		{"maps", "0", "description"},
	}); ok {
		metadata.Description = &description
	}
	if category, ok := firstStringAt(payload, [][]string{
		{"trails", "0", "activityTypeName"},
		{"trails", "0", "activity_type_name"},
		{"trails", "0", "type"},
		{"maps", "0", "activityTypeName"},
	}); ok {
		normalized := normalizeCategory(category)
		if normalized != "" {
			metadata.Category = &normalized
		}
	}
	metadata.PhotoURLs = mergeStrings(metadata.PhotoURLs, alltrailsPhotoURLs(payload)...)
	return points, metadata, nil
}

func alltrailsPhotoURLs(payload any) []string {
	var urls []string
	extract := func(photos any) {
		items, ok := photos.([]any)
		if !ok {
			return
		}
		for _, item := range items {
			if u, ok := stringAt(item, "url"); ok {
				urls = append(urls, u)
			} else if u, ok := stringAt(item, "largeUrl"); ok {
				urls = append(urls, u)
			} else if u, ok := stringAt(item, "originalUrl"); ok {
				urls = append(urls, u)
			}
		}
	}
	if trails, ok := at(payload, "trails"); ok {
		if items, ok := trails.([]any); ok && len(items) > 0 {
			if photos, ok := at(items[0], "photos"); ok {
				extract(photos)
			}
		}
	}
	if maps, ok := at(payload, "maps"); ok {
		if items, ok := maps.([]any); ok && len(items) > 0 {
			if photos, ok := at(items[0], "photos"); ok {
				extract(photos)
			}
		}
	}
	return urls
}

func at(value any, part string) (any, bool) {
	typed, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	res, ok := typed[part]
	return res, ok
}

func mergeStrings(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(values))
	for _, v := range base {
		seen[v] = struct{}{}
	}
	result := append([]string(nil), base...)
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; !ok {
			result = append(result, v)
			seen[v] = struct{}{}
		}
	}
	return result
}

func firstStringAt(value any, paths [][]string) (string, bool) {
	for _, path := range paths {
		if value, ok := stringAt(value, path...); ok {
			return value, true
		}
	}
	return "", false
}

func stringAt(value any, path ...string) (string, bool) {
	for _, part := range path {
		switch typed := value.(type) {
		case map[string]any:
			value = typed[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil {
				return "", false
			}
			if index < 0 || index >= len(typed) {
				return "", false
			}
			value = typed[index]
		default:
			return "", false
		}
	}
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	return text, ok && text != ""
}

func normalizeCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "bike"), strings.Contains(value, "cycling"):
		return "Biking"
	case strings.Contains(value, "hike"), strings.Contains(value, "hiking"):
		return "Hiking"
	case strings.Contains(value, "walk"):
		return "Walking"
	case strings.Contains(value, "run"):
		return "Running"
	default:
		return ""
	}
}

func decodePolyline(encoded string, precision int) ([]providerkit.Point, error) {
	factor := 1.0
	for i := 0; i < precision; i++ {
		factor *= 10
	}
	var points []providerkit.Point
	var lat, lon int
	for index := 0; index < len(encoded); {
		dlat, next, err := decodePolylineValue(encoded, index)
		if err != nil {
			return nil, err
		}
		index = next
		dlon, next, err := decodePolylineValue(encoded, index)
		if err != nil {
			return nil, err
		}
		index = next
		lat += dlat
		lon += dlon
		points = append(points, providerkit.Point{
			Lat: float64(lat) / factor,
			Lon: float64(lon) / factor,
		})
	}
	return points, nil
}

func decodePolylineValue(encoded string, index int) (int, int, error) {
	var result int
	var shift uint
	for ; index < len(encoded); index++ {
		b := int(encoded[index]) - 63
		if b < 0 {
			return 0, index, fmt.Errorf("invalid polyline byte")
		}
		result |= (b & 0x1f) << shift
		shift += 5
		if b < 0x20 {
			index++
			if result&1 != 0 {
				return ^(result >> 1), index, nil
			}
			return result >> 1, index, nil
		}
	}
	return 0, index, fmt.Errorf("truncated polyline")
}
