package alltrails

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

type Provider struct {
	httpClient     *http.Client
	browserFetcher browserfetch.Fetcher
}

func New(httpClient *http.Client) *Provider {
	return NewWithOptions(Options{HTTPClient: httpClient})
}

type Options struct {
	HTTPClient     *http.Client
	BrowserFetcher browserfetch.Fetcher
}

func NewWithOptions(opts Options) *Provider {
	return &Provider{
		httpClient:     providerkit.HTTPClient(opts.HTTPClient),
		browserFetcher: opts.BrowserFetcher,
	}
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

var (
	googlebotUserAgent = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	publicAPIKey      = os.Getenv("ALLTRAILS_API_KEY")
)

func (p *Provider) readSource(ctx context.Context, source string) ([]byte, error) {
	if parsed, ok := providerkit.ParseHTTPURL(source); ok {
		// First try standard HTTP with Googlebot UA to bypass DataDome
		if publicAPIKey != "" {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
			req.Header.Set("User-Agent", googlebotUserAgent)
			res, err := p.httpClient.Do(req)
			if err == nil {
				defer res.Body.Close()
				data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
				if err == nil && len(data) > 0 && !isDataDomeChallenge(data) {
					// We got the page! Now extract the map ID and fetch the map API
					if mapID := extractMapID(data); mapID != "" {
						mapURL := fmt.Sprintf("https://www.alltrails.com/api/alltrails/v3/maps/%s?key=%s&detail=deep", mapID, publicAPIKey)
						reqMap, _ := http.NewRequestWithContext(ctx, http.MethodGet, mapURL, nil)
						reqMap.Header.Set("User-Agent", googlebotUserAgent)
						resMap, err := p.httpClient.Do(reqMap)
						if err == nil {
							defer resMap.Body.Close()
							mapData, err := io.ReadAll(io.LimitReader(resMap.Body, 16<<20))
							if err == nil && len(mapData) > 0 && !isDataDomeChallenge(mapData) {
								return mapData, nil
							}
						}
					}
					// If map fetch failed, return the page HTML and let the parser try to find JSON
					return data, nil
				}
			}
		}

		// Fallback to browser fetch if Googlebot is also blocked or if key is missing
		if p.browserFetcher != nil {
			return p.browserFetcher.Fetch(ctx, parsed.String(), parsed.String(), browserfetch.RequestOptions{
				Headers: map[string]string{"User-Agent": googlebotUserAgent},
				Script: `async () => {
					const getMapID = () => {
						const ogImage = document.querySelector('meta[property="og:image"]');
						if (ogImage) {
							const match = ogImage.content.match(/\/maps\/(\d+)/);
							if (match) return match[1];
						}
						return null;
					};
					const mapId = getMapID();
					if (mapId) {
						let apiUrl = "/api/alltrails/v3/maps/" + mapId + "?detail=deep";
						const key = "` + publicAPIKey + `";
						if (key) {
							apiUrl += "&key=" + key;
						}
						const response = await fetch(apiUrl);
						if (response.ok) return await response.text();
					}
					return document.documentElement.outerHTML;
				}`,
			})
		}
		return nil, fmt.Errorf("alltrails page fetch failed (DataDome)")
	}
	return os.ReadFile(source)
}

func isDataDomeChallenge(data []byte) bool {
	s := string(data)
	return strings.Contains(s, "captcha-delivery.com") || strings.Contains(s, "dd={'rt':'c'")
}

func extractMapID(data []byte) string {
	s := string(data)
	// Try og:image
	reImage := regexp.MustCompile(`property="og:image"\s+content="[^"]+/maps/(\d+)`)
	if match := reImage.FindStringSubmatch(s); len(match) > 1 {
		return match[1]
	}
	// Try al:android:url
	reAndroid := regexp.MustCompile(`property="al:android:url"\s+content="[^"]+/trails/(\d+)`)
	if match := reAndroid.FindStringSubmatch(s); len(match) > 1 {
		// Sometimes the trail ID and map ID are the same, or the map API works with trail ID
		return match[1]
	}
	// Try any link to /maps/ID
	reMapLink := regexp.MustCompile(`/maps/(\d+)`)
	if match := reMapLink.FindStringSubmatch(s); len(match) > 1 {
		return match[1]
	}
	return ""
}

func ParseV3Trail(data []byte) ([]providerkit.Point, wanderer.TrailUpdate, error) {
	if len(data) > 0 && data[0] == '<' {
		// It's HTML, try to extract JSON from script tags
		if extracted, err := extractJSONFromHTML(data); err == nil {
			data = extracted
		}
	}

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
		// Try __NEXT_DATA__ structure
		if props, ok := at(payload, "props"); ok {
			if pageProps, ok := at(props, "pageProps"); ok {
				if trail, ok := at(pageProps, "trail"); ok {
					payload = map[string]any{"trails": []any{trail}}
					polyline, _ = stringAt(payload, "trails", "0", "defaultMap", "routes", "0", "lineSegments", "0", "polyline", "pointsData")
				}
			}
		}
	}
	if polyline == "" {
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

func extractJSONFromHTML(data []byte) ([]byte, error) {
	s := string(data)
	// Try __NEXT_DATA__
	if start := strings.Index(s, `<script id="__NEXT_DATA__" type="application/json">`); start != -1 {
		s = s[start+len(`<script id="__NEXT_DATA__" type="application/json">`):]
		if end := strings.Index(s, `</script>`); end != -1 {
			return []byte(s[:end]), nil
		}
	}
	// Try a more generic regex for any JSON-like script tag that might contain trail data
	re := regexp.MustCompile(`(?s)<script[^>]*>(.*?trails":.*?)</script>`)
	if match := re.FindStringSubmatch(s); len(match) > 1 {
		return []byte(match[1]), nil
	}
	return nil, fmt.Errorf("no JSON found in HTML")
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

func init() {
	if publicAPIKey == "" {
		log.Println("Warning: ALLTRAILS_API_KEY environment variable is missing; falling back to browser-based extraction")
	}
}
