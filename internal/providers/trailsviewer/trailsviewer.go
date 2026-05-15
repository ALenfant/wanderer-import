package trailsviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

var (
	trailIDPattern  = regexp.MustCompile(`(?i)/trail-([^/]+)/`)
	versionPattern  = regexp.MustCompile(`VERSION\s*=\s*"([^"]+)"`)
	appTimePattern  = regexp.MustCompile(`appTime\s*=\s*"([^"]+)"`)
	appTrailPattern = regexp.MustCompile(`appTrail\s*=\s*\{[^}]*"id"\s*:\s*"([^"]+)"`)
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
	return "trails-viewer"
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      "trails-viewer",
		Name:    "Trails Viewer",
		Engine:  "trails-viewer-api",
		Domains: []string{"trails-viewer.com"},
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), []string{"trails-viewer.com"}) {
		return importer.Match{}
	}
	if _, ok := trailID(parsed); !ok {
		return importer.Match{}
	}
	return importer.Match{OK: true, Score: 90, Reason: "Trails Viewer public points API"}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}
	id, ok := trailID(parsed)
	if !ok {
		return nil, fmt.Errorf("%s could not extract a trail ID from %s", p.Name(), source)
	}

	page, metadata, err := p.fetchPage(ctx, parsed.String())
	if err != nil {
		return nil, err
	}
	if pageID := appTrailID(page); pageID != "" {
		id = pageID
	}
	version := pageVersion(page)
	tokenSeed := time.Now().Unix()
	if decodedAppTime, ok := pageAppTime(page); ok {
		tokenSeed = decodedAppTime
	}
	endpoint := trailsViewerEndpoint(parsed, id, version)
	data, err := p.fetchPoints(ctx, endpoint, parsed.String(), tokenSeed)
	if err != nil {
		if p.browserFetcher == nil || !shouldBrowserFetch(err) {
			return nil, err
		}
		data, err = p.browserFetcher.Fetch(ctx, parsed.String(), endpoint, browserfetch.RequestOptions{
			Headers: map[string]string{
				"Token": encodeToken(tokenSeed),
			},
			Cookies: trailsViewerBrowserCookies(),
		})
		if err != nil {
			return nil, fmt.Errorf("Trails Viewer browser fallback failed after HTTP fetch failed: %w", err)
		}
	}
	points, err := ParsePoints(data)
	if err != nil {
		return nil, err
	}

	metadata = wanderer.MergeTrailUpdates(providerkit.MetadataFromPoints(points), metadata)
	name := "trails-viewer-" + id
	effectiveMetadata := wanderer.MergeTrailUpdates(metadata, spec.Update)
	if effectiveMetadata.Name != nil && strings.TrimSpace(*effectiveMetadata.Name) != "" {
		name = strings.TrimSpace(*effectiveMetadata.Name)
	}
	body, err := providerkit.GPXReadCloser(name, points)
	if err != nil {
		return nil, err
	}
	return &importer.ResolvedTrail{
		Source:   endpoint,
		Filename: providerkit.SlugFilename(name, ".gpx"),
		Body:     body,
		Metadata: metadata,
	}, nil
}

func (p *Provider) fetchPage(ctx context.Context, source string) ([]byte, wanderer.TrailUpdate, error) {
	res, err := providerkit.GET(ctx, p.httpClient, source)
	if err != nil {
		return nil, wanderer.TrailUpdate{}, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, wanderer.TrailUpdate{}, err
	}
	base, _ := providerkit.ParseHTTPURL(source)
	return data, providerkit.ExtractHTMLMetadata(base, data), nil
}

func (p *Provider) fetchPoints(ctx context.Context, endpoint, referer string, tokenSeed int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", referer)
	req.Header.Set("Token", encodeToken(tokenSeed))
	res, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, pointsHTTPError{StatusCode: res.StatusCode}
	}
	return data, nil
}

type pointsHTTPError struct {
	StatusCode int
}

func (e pointsHTTPError) Error() string {
	return fmt.Sprintf("Trails Viewer points API returned %d; the endpoint may require a browser session", e.StatusCode)
}

func shouldBrowserFetch(err error) bool {
	var statusErr pointsHTTPError
	if !errors.As(err, &statusErr) {
		return false
	}
	switch statusErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError:
		return true
	default:
		return false
	}
}

func trailsViewerBrowserCookies() map[string]string {
	now := time.Now().Unix()
	consents := `{"storage":0,"adUserData":0,"adPersonalization":0}`
	fccdcf := fmt.Sprintf(`[null,null,null,null,null,null,[[[32,"[\"wanderer-import\",[%d,313000000]]"]]]]`, now)
	return map[string]string{
		"consents": consents,
		"FCCDCF":   fccdcf,
	}
}

func trailID(parsed *url.URL) (string, bool) {
	match := trailIDPattern.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return "", false
	}
	id := strings.TrimSpace(match[1])
	return id, id != ""
}

func appTrailID(data []byte) string {
	match := appTrailPattern.FindSubmatch(data)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

func pageVersion(data []byte) string {
	match := versionPattern.FindSubmatch(data)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

func pageAppTime(data []byte) (int64, bool) {
	match := appTimePattern.FindSubmatch(data)
	if len(match) != 2 {
		return 0, false
	}
	value, err := decodeShiftedBase36(string(match[1]))
	return value, err == nil
}

func trailsViewerEndpoint(source *url.URL, id, version string) string {
	endpoint := *source
	endpoint.Path = "/"
	values := url.Values{}
	values.Set("_path", "api.trails.getPoints")
	values.Set("trail", id)
	if version != "" {
		values.Set("version", version)
	}
	endpoint.RawQuery = values.Encode()
	endpoint.Fragment = ""
	return endpoint.String()
}

type encodedPoint struct {
	Lat       string `json:"latitude"`
	Lon       string `json:"longitude"`
	Elevation string `json:"elevation"`
}

func ParsePoints(data []byte) ([]providerkit.Point, error) {
	var payload []encodedPoint
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	points := make([]providerkit.Point, 0, len(payload))
	for _, item := range payload {
		lat, err := decodeShiftedBase36(item.Lat)
		if err != nil {
			return nil, fmt.Errorf("decode latitude: %w", err)
		}
		lon, err := decodeShiftedBase36(item.Lon)
		if err != nil {
			return nil, fmt.Errorf("decode longitude: %w", err)
		}
		point := providerkit.Point{
			Lat: float64(lat) / 100000,
			Lon: float64(lon) / 100000,
		}
		if strings.TrimSpace(item.Elevation) != "" {
			ele, err := decodeShiftedBase36(item.Elevation)
			if err == nil {
				value := float64(ele) / 10
				point.Ele = &value
			}
		}
		points = append(points, point)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("Trails Viewer response had no points")
	}
	return points, nil
}

func decodeShiftedBase36(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty value")
	}
	sign := int64(1)
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = strings.TrimPrefix(value, "-")
	}
	if len(value) < 2 {
		return 0, fmt.Errorf("short encoded value")
	}
	shift, ok := base36Digit(value[len(value)-1])
	if !ok {
		return 0, fmt.Errorf("invalid shift")
	}
	var decoded bytes.Buffer
	for _, r := range value[:len(value)-1] {
		digit, ok := base36Digit(byte(r))
		if !ok {
			return 0, fmt.Errorf("invalid digit %q", r)
		}
		decoded.WriteByte(base36Char((digit - shift + 36) % 36))
	}
	number, err := strconv.ParseInt(decoded.String(), 36, 64)
	if err != nil {
		return 0, err
	}
	return sign * number, nil
}

func encodeToken(unixSeconds int64) string {
	shift := rand.New(rand.NewSource(unixSeconds)).Intn(35) + 1
	value := strconv.FormatInt(unixSeconds, 36)
	var encoded bytes.Buffer
	for _, r := range value {
		digit, ok := base36Digit(byte(r))
		if !ok {
			continue
		}
		encoded.WriteByte(base36Char((digit + shift) % 36))
	}
	encoded.WriteByte(base36Char(shift))
	return encoded.String()
}

func base36Digit(ch byte) (int, bool) {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0'), true
	case ch >= 'a' && ch <= 'z':
		return int(ch-'a') + 10, true
	case ch >= 'A' && ch <= 'Z':
		return int(ch-'A') + 10, true
	default:
		return 0, false
	}
}

func base36Char(value int) byte {
	if value < 10 {
		return byte('0' + value)
	}
	return byte('a' + value - 10)
}
