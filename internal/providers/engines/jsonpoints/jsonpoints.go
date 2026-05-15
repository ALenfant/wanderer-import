package jsonpoints

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/wkt"
	orbgeojson "github.com/paulmach/orb/geojson"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

type IDExtractor func(*url.URL) (string, bool)
type Parser func([]byte) ([]providerkit.Point, error)
type MetadataParser func([]byte, []providerkit.Point) wanderer.TrailUpdate

type Config struct {
	ID            string
	Name          string
	Domains       []string
	Score         int
	Templates     []string
	ExtractID     IDExtractor
	Parse         Parser
	ParseMetadata MetadataParser
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
		Engine:  "jsonpoints",
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
	return importer.Match{OK: true, Score: p.cfg.Score, Reason: "JSON coordinates API"}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	parsed, ok := providerkit.ParseHTTPURL(strings.TrimSpace(spec.Source))
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}
	if p.cfg.Parse == nil {
		return nil, fmt.Errorf("%s has no JSON parser configured", p.Name())
	}

	id := ""
	if p.cfg.ExtractID != nil {
		var ok bool
		id, ok = p.cfg.ExtractID(parsed)
		if !ok {
			return nil, fmt.Errorf("%s could not extract an ID from %s", p.Name(), spec.Source)
		}
	}

	var lastErr error
	for _, template := range p.cfg.Templates {
		endpoint := strings.ReplaceAll(template, "{id}", url.PathEscape(id))
		data, points, err := p.fetchPoints(ctx, endpoint)
		if err != nil {
			lastErr = err
			continue
		}
		metadata := providerkit.MetadataFromPoints(points)
		if p.cfg.ParseMetadata != nil {
			metadata = wanderer.MergeTrailUpdates(metadata, p.cfg.ParseMetadata(data, points))
		}
		metadata = wanderer.MergeTrailUpdates(metadata, p.fetchSourceMetadata(ctx, parsed.String()))
		name := p.cfg.ID
		effectiveMetadata := wanderer.MergeTrailUpdates(metadata, spec.Update)
		if effectiveMetadata.Name != nil && strings.TrimSpace(*effectiveMetadata.Name) != "" {
			name = strings.TrimSpace(*effectiveMetadata.Name)
		} else if spec.Update.Name != nil && strings.TrimSpace(*spec.Update.Name) != "" {
			name = strings.TrimSpace(*spec.Update.Name)
		}
		body, err := providerkit.GPXReadCloser(name, points)
		if err != nil {
			return nil, err
		}
		return &importer.ResolvedTrail{
			Source:   endpoint,
			Filename: providerkit.SlugFilename(p.cfg.ID+"-"+id, ".gpx"),
			Body:     body,
			Metadata: metadata,
		}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%s has no API templates configured", p.Name())
}

func (p *Provider) fetchPoints(ctx context.Context, endpoint string) ([]byte, []providerkit.Point, error) {
	res, err := providerkit.GET(ctx, p.httpClient, endpoint)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, nil, err
	}
	points, err := p.cfg.Parse(data)
	return data, points, err
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

func ParseKomootItems(data []byte) ([]providerkit.Point, error) {
	var payload struct {
		Items []struct {
			Lat float64  `json:"lat"`
			Lng float64  `json:"lng"`
			Alt *float64 `json:"alt"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	points := make([]providerkit.Point, 0, len(payload.Items))
	for _, item := range payload.Items {
		points = append(points, providerkit.Point{Lat: item.Lat, Lon: item.Lng, Ele: item.Alt})
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("Komoot response had no coordinates")
	}
	return points, nil
}

func ParseKomootMetadata(data []byte, points []providerkit.Point) wanderer.TrailUpdate {
	var payload struct {
		Items []struct {
			T *float64 `json:"t"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return wanderer.TrailUpdate{}
	}
	update := providerkit.MetadataFromPoints(points)
	var maxT float64
	for _, item := range payload.Items {
		if item.T != nil && *item.T > maxT {
			maxT = *item.T
		}
	}
	if maxT > 0 {
		seconds := maxT / 1000
		update.Duration = &seconds
	}
	return update
}

func ParseSityTrailWKT(data []byte) ([]providerkit.Point, error) {
	var payload struct {
		Data struct {
			Trail struct {
				TraceWKT string `json:"trace_wkt"`
			} `json:"trail"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return ParseLineStringWKT(payload.Data.Trail.TraceWKT)
}

func ParseSityTrailMetadata(data []byte, points []providerkit.Point) wanderer.TrailUpdate {
	var payload struct {
		Data struct {
			Trail struct {
				Name          string   `json:"name"`
				NameFr        string   `json:"nameFr"`
				DescriptionFr string   `json:"descFr"`
				Commune       string   `json:"commune"`
				Admin1        string   `json:"admin1"`
				Country       string   `json:"country"`
				Length        *float64 `json:"length"`
				Ascent        *float64 `json:"ascent"`
				Descent       *float64 `json:"descent"`
				Latitude      *float64 `json:"latitude"`
				Longitude     *float64 `json:"longitude"`
				MainCategory  string   `json:"mainCategory"`
				Meffort       string   `json:"meffort"`
			} `json:"trail"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return wanderer.TrailUpdate{}
	}
	trail := payload.Data.Trail
	update := providerkit.MetadataFromPoints(points)
	if trail.NameFr != "" {
		update.Name = stringPtr(trail.NameFr)
	} else if trail.Name != "" {
		update.Name = stringPtr(trail.Name)
	}
	if trail.DescriptionFr != "" {
		update.Description = stringPtr(trail.DescriptionFr)
	}
	locationParts := nonEmpty(trail.Commune, trail.Admin1, trail.Country)
	if len(locationParts) > 0 {
		location := strings.Join(locationParts, ", ")
		update.Location = &location
	}
	if trail.Length != nil && *trail.Length > 0 {
		value := *trail.Length
		if value < 1000 {
			value *= 1000
		}
		update.Distance = &value
	}
	if trail.Ascent != nil && *trail.Ascent > 0 {
		update.ElevationGain = trail.Ascent
	}
	if trail.Descent != nil && *trail.Descent > 0 {
		update.ElevationLoss = trail.Descent
	}
	if trail.Latitude != nil {
		update.Lat = trail.Latitude
	}
	if trail.Longitude != nil {
		update.Lon = trail.Longitude
	}
	update.Tags = append(update.Tags, nonEmpty(trail.MainCategory, trail.Meffort)...)
	return update
}

func ParseLineStringWKT(value string) ([]providerkit.Point, error) {
	value = strings.TrimSpace(value)
	const prefix = "LINESTRING"
	if !strings.HasPrefix(strings.ToUpper(value), prefix) {
		return nil, fmt.Errorf("unsupported WKT geometry")
	}
	start := strings.Index(value, "(")
	end := strings.LastIndex(value, ")")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("invalid WKT linestring")
	}
	values := strings.Split(value[start+1:end], ",")
	elevations := make([]*float64, 0, len(values))
	twoD := make([]string, 0, len(values))
	for _, item := range values {
		fields := strings.Fields(strings.TrimSpace(item))
		if len(fields) < 2 {
			continue
		}
		var ele *float64
		if len(fields) >= 3 {
			parsed, err := strconv.ParseFloat(fields[2], 64)
			if err != nil {
				return nil, err
			}
			ele = &parsed
		}
		twoD = append(twoD, fields[0]+" "+fields[1])
		elevations = append(elevations, ele)
	}
	line, err := wkt.UnmarshalLineString("LINESTRING (" + strings.Join(twoD, ", ") + ")")
	if err != nil {
		return nil, err
	}
	points := make([]providerkit.Point, 0, len(line))
	for i, point := range line {
		var ele *float64
		if i < len(elevations) {
			ele = elevations[i]
		}
		points = append(points, providerkit.Point{Lat: point[1], Lon: point[0], Ele: ele})
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("WKT linestring had no coordinates")
	}
	return points, nil
}

func ParseGeoJSONLineString(order string) Parser {
	return func(data []byte) ([]providerkit.Point, error) {
		points, err := providerkit.PointsFromGeoJSON(data, order)
		if err == nil {
			return points, nil
		}
		if order != "latlon" {
			if points, standardErr := parseStandardGeoJSON(data); standardErr == nil {
				return points, nil
			}
		}

		var payload struct {
			Type     string `json:"type"`
			Features []struct {
				Geometry struct {
					Type        string          `json:"type"`
					Coordinates json.RawMessage `json:"coordinates"`
				} `json:"geometry"`
			} `json:"features"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}

		geometryType := payload.Geometry.Type
		coordinates := payload.Geometry.Coordinates
		if len(payload.Features) > 0 {
			geometryType = payload.Features[0].Geometry.Type
			coordinates = payload.Features[0].Geometry.Coordinates
		}
		if !strings.EqualFold(geometryType, "LineString") {
			return nil, fmt.Errorf("unsupported GeoJSON geometry %q", geometryType)
		}

		var rawPoints [][]float64
		if err := json.Unmarshal(coordinates, &rawPoints); err != nil {
			return nil, err
		}
		points = make([]providerkit.Point, 0, len(rawPoints))
		for _, raw := range rawPoints {
			if len(raw) < 2 {
				continue
			}
			var lat, lon float64
			switch order {
			case "latlon":
				lat, lon = raw[0], raw[1]
			default:
				lon, lat = raw[0], raw[1]
			}
			var ele *float64
			if len(raw) >= 3 {
				value := raw[2]
				ele = &value
			}
			points = append(points, providerkit.Point{Lat: lat, Lon: lon, Ele: ele})
		}
		if len(points) == 0 {
			return nil, fmt.Errorf("GeoJSON linestring had no coordinates")
		}
		return points, nil
	}
}

func parseStandardGeoJSON(data []byte) ([]providerkit.Point, error) {
	if collection, err := orbgeojson.UnmarshalFeatureCollection(data); err == nil && len(collection.Features) > 0 {
		return pointsFromGeometry(collection.Features[0].Geometry)
	}
	if feature, err := orbgeojson.UnmarshalFeature(data); err == nil {
		return pointsFromGeometry(feature.Geometry)
	}
	if geometry, err := orbgeojson.UnmarshalGeometry(data); err == nil {
		return pointsFromGeometry(geometry.Geometry())
	}
	return nil, fmt.Errorf("unsupported GeoJSON payload")
}

func pointsFromGeometry(geometry orb.Geometry) ([]providerkit.Point, error) {
	var points []providerkit.Point
	switch typed := geometry.(type) {
	case orb.LineString:
		points = appendOrbLineString(points, typed)
	case orb.MultiLineString:
		for _, line := range typed {
			points = appendOrbLineString(points, line)
		}
	default:
		return nil, fmt.Errorf("unsupported GeoJSON geometry %T", geometry)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("GeoJSON geometry had no coordinates")
	}
	return points, nil
}

func appendOrbLineString(points []providerkit.Point, line orb.LineString) []providerkit.Point {
	for _, point := range line {
		points = append(points, providerkit.Point{Lat: point[1], Lon: point[0]})
	}
	return points
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func nonEmpty(values ...string) []string {
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
