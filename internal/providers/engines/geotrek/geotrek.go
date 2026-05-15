package geotrek

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/gpxlinks"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

var trekIDPattern = regexp.MustCompile(`(?i)/(?:trek|treks)/([0-9]+)(?:[/-]|$)`)
var apiURLPattern = regexp.MustCompile(`(?i)"apiUrl"\s*:\s*"([^"]+/api/v2)"`)

type Config struct {
	ID      string
	Name    string
	Domains []string
	Score   int
}

type Provider struct {
	cfg        Config
	httpClient *http.Client
	fallback   *gpxlinks.Provider
}

func NewProvider(cfg Config, httpClient *http.Client) *Provider {
	if cfg.Score == 0 {
		cfg.Score = 90
	}
	client := providerkit.HTTPClient(httpClient)
	return &Provider{
		cfg:        cfg,
		httpClient: client,
		fallback: gpxlinks.NewProvider(gpxlinks.Config{
			ID:                 cfg.ID,
			Name:               cfg.Name,
			Domains:            cfg.Domains,
			AllowExternalLinks: true,
			Score:              cfg.Score - 1,
		}, client),
	}
}

func (p *Provider) Name() string {
	return p.cfg.ID
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      p.cfg.ID,
		Name:    p.cfg.Name,
		Engine:  "geotrek",
		Domains: append([]string(nil), p.cfg.Domains...),
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), p.cfg.Domains) {
		return importer.Match{}
	}
	return importer.Match{OK: true, Score: p.cfg.Score, Reason: "Geotrek route portal"}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return nil, fmt.Errorf("%s requires an HTTP URL", p.Name())
	}
	metadata := p.fetchSourceMetadata(ctx, source)

	if id := trekID(parsed); id != "" {
		for _, candidate := range p.candidates(parsed, id) {
			download, err := providerkit.DownloadVerifiedTrail(ctx, p.httpClient, candidate)
			if err == nil {
				return &importer.ResolvedTrail{
					Source:   download.Source,
					Filename: download.Filename,
					Body:     download.Body,
					Metadata: wanderer.MergeTrailUpdates(download.Metadata, metadata),
				}, nil
			}
		}
	}

	resolved, err := p.fallback.Resolve(ctx, spec)
	if err != nil {
		return nil, err
	}
	resolved.Metadata = wanderer.MergeTrailUpdates(resolved.Metadata, metadata)
	return resolved, nil
}

func (p *Provider) candidates(parsed *url.URL, id string) []string {
	base := parsed.Scheme + "://" + parsed.Host
	return []string{
		base + "/api/treks/" + id + ".gpx",
		base + "/api/treks/" + id + "/gpx/",
		base + "/trek/" + id + "/download-gpx/",
	}
}

func trekID(parsed *url.URL) string {
	match := trekIDPattern.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return ""
	}
	return match[1]
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
	metadata := providerkit.ExtractHTMLMetadata(base, data)
	if base == nil {
		return metadata
	}
	id := trekID(base)
	if id == "" {
		return metadata
	}
	if apiBase := geotrekAPIBase(data); apiBase != "" {
		apiMetadata := p.fetchAPIMetadata(ctx, apiBase, id)
		metadata = wanderer.MergeTrailUpdates(metadata, apiMetadata)
	}
	return metadata
}

func geotrekAPIBase(data []byte) string {
	text := strings.ReplaceAll(string(data), `\/`, `/`)
	match := apiURLPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func (p *Provider) fetchAPIMetadata(ctx context.Context, apiBase, id string) wanderer.TrailUpdate {
	detailURL := strings.TrimRight(apiBase, "/") + "/trek/" + id + "/?language=fr"
	res, err := providerkit.GET(ctx, p.httpClient, detailURL)
	if err != nil {
		return wanderer.TrailUpdate{}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return wanderer.TrailUpdate{}
	}

	var payload geotrekTrekDetail
	if err := json.Unmarshal(data, &payload); err != nil {
		return wanderer.TrailUpdate{}
	}
	return metadataFromAPIDetail(payload)
}

type geotrekTrekDetail struct {
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	DescriptionTeaser string  `json:"description_teaser"`
	Ambiance          string  `json:"ambiance"`
	Advice            string  `json:"advice"`
	Access            string  `json:"access"`
	Departure         string  `json:"departure"`
	Arrival           string  `json:"arrival"`
	AdvisedParking    string  `json:"advised_parking"`
	Ascent            float64 `json:"ascent"`
	Descent           float64 `json:"descent"`
	DurationHours     float64 `json:"duration"`
	Length2D          float64 `json:"length_2d"`
	Length3D          float64 `json:"length_3d"`
	Difficulty        int     `json:"difficulty"`
	Attachments       []struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		Thumbnail string `json:"thumbnail"`
	} `json:"attachments"`
}

func metadataFromAPIDetail(payload geotrekTrekDetail) wanderer.TrailUpdate {
	var update wanderer.TrailUpdate
	setString(&update.Name, payload.Name)
	setString(&update.Location, payload.Departure)
	if payload.Length2D > 0 {
		update.Distance = &payload.Length2D
	} else if payload.Length3D > 0 {
		update.Distance = &payload.Length3D
	}
	if payload.Ascent > 0 {
		update.ElevationGain = &payload.Ascent
	}
	if payload.Descent < 0 {
		descent := -payload.Descent
		update.ElevationLoss = &descent
	} else if payload.Descent > 0 {
		update.ElevationLoss = &payload.Descent
	}
	if payload.DurationHours > 0 {
		duration := payload.DurationHours * 3600
		update.Duration = &duration
	}
	if difficulty := geotrekDifficulty(payload.Difficulty); difficulty != "" {
		update.Difficulty = &difficulty
	}
	update.PhotoURLs = geotrekPhotoURLs(payload.Attachments)
	update.Description = geotrekDescription(payload)
	providerkit.AppendRouteSteps(&update, routeStepsFromHTML(payload.Description))
	return update
}

func geotrekDescription(payload geotrekTrekDetail) *string {
	var parts []string
	appendPart := func(label, value string) {
		text := providerkit.CleanHTMLText(value)
		if text == "" {
			return
		}
		if label != "" {
			parts = append(parts, label+": "+text)
		} else {
			parts = append(parts, text)
		}
	}
	appendPart("", payload.DescriptionTeaser)
	appendPart("", payload.Ambiance)
	if len(routeStepsFromHTML(payload.Description)) == 0 {
		appendPart("", payload.Description)
	} else {
		appendPart("Route", routeIntroFromHTML(payload.Description))
	}
	appendPart("Departure", payload.Departure)
	appendPart("Arrival", payload.Arrival)
	appendPart("Access", payload.Access)
	appendPart("Parking", payload.AdvisedParking)
	appendPart("Advice", payload.Advice)
	if len(parts) == 0 {
		return nil
	}
	description := strings.Join(parts, "\n\n")
	return &description
}

func routeIntroFromHTML(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	nodes, err := html.ParseFragment(strings.NewReader(value), nil)
	if err != nil {
		return ""
	}
	var parts []string
	var stopped bool
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if stopped {
			return
		}
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "ol") {
			stopped = true
			return
		}
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "p") {
			if text := providerkit.CleanHTMLText(nodeText(node)); text != "" {
				parts = append(parts, text)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return strings.Join(parts, " ")
}

func routeStepsFromHTML(value string) []providerkit.RouteStep {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	nodes, err := html.ParseFragment(strings.NewReader(value), nil)
	if err != nil {
		return nil
	}
	var steps []providerkit.RouteStep
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "li") {
			text := providerkit.CleanHTMLText(nodeText(node))
			if text != "" {
				steps = append(steps, providerkit.RouteStep{
					Number:      len(steps) + 1,
					Description: text,
				})
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return steps
}

func nodeText(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return b.String()
}

func geotrekPhotoURLs(attachments []struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Thumbnail string `json:"thumbnail"`
}) []string {
	var urls []string
	for _, attachment := range attachments {
		if !strings.EqualFold(attachment.Type, "image") {
			continue
		}
		if strings.TrimSpace(attachment.URL) != "" {
			urls = append(urls, strings.TrimSpace(attachment.URL))
			continue
		}
		if strings.TrimSpace(attachment.Thumbnail) != "" {
			urls = append(urls, strings.TrimSpace(attachment.Thumbnail))
		}
	}
	return urls
}

func geotrekDifficulty(value int) string {
	switch {
	case value <= 0:
		return ""
	case value <= 2:
		return wanderer.DifficultyEasy
	case value <= 4:
		return wanderer.DifficultyModerate
	default:
		return wanderer.DifficultyDifficult
	}
}

func setString(target **string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	*target = &value
}
