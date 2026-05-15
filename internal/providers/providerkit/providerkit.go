package providerkit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/tkrajina/gpxgo/gpx"

	"wanderer-import/internal/wanderer"
)

const UserAgent = "wanderer-import/0.1"

var trailFileExtensions = map[string]struct{}{
	".fit":     {},
	".geojson": {},
	".gpx":     {},
	".kml":     {},
	".kmz":     {},
	".tcx":     {},
}

type Point struct {
	Lat float64
	Lon float64
	Ele *float64
}

type Download struct {
	Source   string
	Filename string
	Body     io.ReadCloser
	Header   http.Header
	Metadata wanderer.TrailUpdate
}

func HTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}

func ParseHTTPURL(value string) (*url.URL, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return nil, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, false
	}
	if parsed.Host == "" {
		return nil, false
	}
	return parsed, true
}

func HostMatches(host string, domains []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func HasTrailFileExtension(value string) bool {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	_, ok := trailFileExtensions[strings.ToLower(path.Ext(value))]
	return ok
}

func TrailFileExtension(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	ext := strings.ToLower(path.Ext(value))
	if _, ok := trailFileExtensions[ext]; ok {
		return ext
	}
	return ""
}

func ResolveReference(base *url.URL, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	ref = strings.Trim(ref, `"'`)
	ref = strings.ReplaceAll(ref, `\/`, `/`)
	ref = strings.ReplaceAll(ref, `\u0026`, `&`)
	ref = strings.ReplaceAll(ref, `&amp;`, `&`)
	if ref == "" || strings.HasPrefix(ref, "javascript:") || strings.HasPrefix(ref, "mailto:") {
		return "", false
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return "", false
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	return parsed.String(), true
}

func GET(ctx context.Context, client *http.Client, source string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := HTTPClient(client).Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		_ = res.Body.Close()
		return nil, fmt.Errorf("download %s failed with %d", source, res.StatusCode)
	}
	return res, nil
}

func DownloadURL(ctx context.Context, client *http.Client, source string) (*Download, error) {
	res, err := GET(ctx, client, source)
	if err != nil {
		return nil, err
	}
	return &Download{
		Source:   source,
		Filename: FilenameFromResponse(source, res.Header, "trail"+DefaultExtension(source)),
		Body:     res.Body,
		Header:   res.Header,
	}, nil
}

func DownloadVerifiedTrail(ctx context.Context, client *http.Client, source string) (*Download, error) {
	download, err := DownloadURL(ctx, client, source)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(download.Body)
	_ = download.Body.Close()
	if err != nil {
		return nil, err
	}
	if !LooksLikeTrailData(source, download.Header, data) {
		return nil, fmt.Errorf("download %s did not look like a trail file", source)
	}
	download.Body = io.NopCloser(bytes.NewReader(data))
	download.Metadata = MetadataFromTrailData(source, download.Header, data)
	return download, nil
}

func LooksLikeTrailData(source string, header http.Header, data []byte) bool {
	if HasTrailFileExtension(source) {
		return true
	}
	if filename := filenameFromContentDisposition(header); HasTrailFileExtension(filename) {
		return true
	}
	contentType := strings.ToLower(header.Get("Content-Type"))
	if strings.Contains(contentType, "gpx") ||
		strings.Contains(contentType, "geo+json") ||
		strings.Contains(contentType, "geojson") ||
		strings.Contains(contentType, "kml") ||
		strings.Contains(contentType, "xml") {
		return true
	}
	prefix := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 512)])))
	return strings.Contains(prefix, "<gpx") ||
		strings.Contains(prefix, "<kml") ||
		strings.Contains(prefix, `"featurecollection"`) ||
		strings.Contains(prefix, `"linestring"`)
}

func FilenameFromResponse(source string, header http.Header, fallback string) string {
	if filename := filenameFromContentDisposition(header); filename != "" {
		return filename
	}

	if parsed, err := url.Parse(source); err == nil {
		if filename := SanitizeFilename(path.Base(parsed.Path)); filename != "" {
			return filename
		}
	}

	if fallback = SanitizeFilename(fallback); fallback != "" {
		return fallback
	}
	return "trail.gpx"
}

func filenameFromContentDisposition(header http.Header) string {
	if disposition := header.Get("Content-Disposition"); disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			return SanitizeFilename(params["filename"])
		}
	}
	return ""
}

func SanitizeFilename(filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), "\\", "/")
	filename = filepath.Base(filename)
	if filename == "." || filename == string(filepath.Separator) {
		return ""
	}
	return filename
}

func DefaultExtension(source string) string {
	if ext := TrailFileExtension(source); ext != "" {
		return ext
	}
	return ".gpx"
}

func SlugFilename(slug, ext string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = "trail"
	}
	slug = strings.ToLower(slug)
	var b strings.Builder
	dash := false
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		result = "trail"
	}
	if ext == "" {
		ext = ".gpx"
	}
	return result + ext
}

func GPXReadCloser(name string, points []Point) (io.ReadCloser, error) {
	if len(points) == 0 {
		return nil, fmt.Errorf("route has no coordinates")
	}

	segment := gpx.GPXTrackSegment{Points: make([]gpx.GPXPoint, 0, len(points))}
	for _, point := range points {
		gpxPoint := gpx.GPXPoint{
			Point: gpx.Point{
				Latitude:  point.Lat,
				Longitude: point.Lon,
			},
		}
		if point.Ele != nil {
			gpxPoint.Elevation = *gpx.NewNullableFloat64(*point.Ele)
		}
		segment.Points = append(segment.Points, gpxPoint)
	}

	doc := gpx.GPX{
		Version: "1.1",
		Creator: "wanderer-import",
		Tracks: []gpx.GPXTrack{{
			Name:     strings.TrimSpace(name),
			Segments: []gpx.GPXTrackSegment{segment},
		}},
	}
	data, err := doc.ToXml(gpx.ToXmlParams{Version: "1.1", Indent: true})
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
