package cirkwi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/urltemplate"
	"wanderer-import/internal/providers/providerkit"
	"wanderer-import/internal/wanderer"
)

var idPattern = regexp.MustCompile(`^([0-9]+(?:_[0-9]+)?)`)

type Provider struct {
	httpClient *http.Client
	fallback   *urltemplate.Provider
}

func New(httpClient *http.Client) *Provider {
	client := providerkit.HTTPClient(httpClient)
	return &Provider{
		httpClient: client,
		fallback: urltemplate.NewProvider(urltemplate.Config{
			ID:      "cirkwi",
			Name:    "Cirkwi",
			Domains: []string{"cirkwi.com"},
			Score:   90,
			Templates: []string{
				"https://www.cirkwi.com/fr/exporter-gpx/{id}_0",
				"https://www.cirkwi.com/fr/exporter-gpx/{id}",
			},
			ExtractID: extractID,
		}, client),
	}
}

func (p *Provider) Name() string {
	return "cirkwi"
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      "cirkwi",
		Name:    "Cirkwi",
		Engine:  "cirkwi-page-json",
		Domains: []string{"cirkwi.com"},
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), []string{"cirkwi.com"}) {
		return importer.Match{}
	}
	if _, ok := extractID(parsed); !ok {
		return importer.Match{}
	}
	return importer.Match{OK: true, Score: 90, Reason: "Cirkwi page-embedded route JSON"}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	page, err := p.fetchPage(ctx, source)
	if err == nil {
		if resolved, parseErr := resolvedFromPage(source, page, spec.Update); parseErr == nil {
			return resolved, nil
		}
	}
	return p.fallback.Resolve(ctx, spec)
}

func (p *Provider) fetchPage(ctx context.Context, source string) ([]byte, error) {
	res, err := providerkit.GET(ctx, p.httpClient, source)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(io.LimitReader(res.Body, 16<<20))
}

func resolvedFromPage(source string, page []byte, update wanderer.TrailUpdate) (*importer.ResolvedTrail, error) {
	raw, err := extractObjetJSON(page)
	if err != nil {
		return nil, err
	}
	var payload pageObject
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	points := payload.points()
	if len(points) == 0 {
		return nil, fmt.Errorf("Cirkwi page JSON had no route coordinates")
	}

	metadata := providerkit.MetadataFromPoints(points)
	metadata = wanderer.MergeTrailUpdates(metadata, payload.metadata())
	if base, err := url.Parse(source); err == nil {
		metadata = wanderer.MergeTrailUpdates(metadata, providerkit.ExtractHTMLMetadata(base, page))
	}
	name := "cirkwi-" + payload.cleanID()
	effectiveMetadata := wanderer.MergeTrailUpdates(metadata, update)
	if effectiveMetadata.Name != nil && strings.TrimSpace(*effectiveMetadata.Name) != "" {
		name = strings.TrimSpace(*effectiveMetadata.Name)
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

type pageObject struct {
	IDObjet string `json:"id_objet"`
	Adresse struct {
		Ville string `json:"ville"`
		Lat   string `json:"lat"`
		Lng   string `json:"lng"`
	} `json:"adresse"`
	Traduction map[string]translation `json:"traduction"`
	Trace      struct {
		Altimetries []struct {
			Altitude *float64 `json:"altitude"`
			Position struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"position"`
		} `json:"altimetries"`
		Distance float64 `json:"distance"`
	} `json:"trace"`
	Locomotions []struct {
		Duree struct {
			TotalSecondes float64 `json:"total_secondes"`
		} `json:"duree"`
		Locomotion struct {
			IDCategorie int    `json:"id_categorie_locomotion"`
			Nom         string `json:"nom_locomotion"`
		} `json:"locomotion"`
	} `json:"locomotions"`
}

type translation struct {
	Information struct {
		Titre       string `json:"titre"`
		Description string `json:"description"`
	} `json:"information"`
	Tags []struct {
		Tag struct {
			Tag string `json:"tag"`
		} `json:"tag"`
	} `json:"tags"`
	InformationsComplementaires []struct {
		Titre           string `json:"titre"`
		Description     string `json:"description"`
		HTMLDescription string `json:"htmlDescription"`
	} `json:"informations_complementaires"`
	URLImage string `json:"url_image"`
}

func (p pageObject) cleanID() string {
	id := strings.TrimSpace(p.IDObjet)
	if id == "" {
		return "route"
	}
	return id
}

func (p pageObject) points() []providerkit.Point {
	points := make([]providerkit.Point, 0, len(p.Trace.Altimetries))
	for _, item := range p.Trace.Altimetries {
		if item.Position.Lat == 0 && item.Position.Lng == 0 {
			continue
		}
		points = append(points, providerkit.Point{
			Lat: item.Position.Lat,
			Lon: item.Position.Lng,
			Ele: item.Altitude,
		})
	}
	return points
}

func (p pageObject) metadata() wanderer.TrailUpdate {
	var update wanderer.TrailUpdate
	tr := p.primaryTranslation()
	if title := providerkit.CleanHTMLText(tr.Information.Titre); title != "" {
		update.Name = &title
	}
	if description := providerkit.CleanHTMLText(tr.Information.Description); description != "" {
		update.Description = &description
	}
	if location := providerkit.CleanHTMLText(p.Adresse.Ville); location != "" {
		update.Location = &location
	}
	if lat, err := strconv.ParseFloat(strings.TrimSpace(p.Adresse.Lat), 64); err == nil {
		update.Lat = &lat
	}
	if lon, err := strconv.ParseFloat(strings.TrimSpace(p.Adresse.Lng), 64); err == nil {
		update.Lon = &lon
	}
	if p.Trace.Distance > 0 {
		distance := p.Trace.Distance * 1000
		update.Distance = &distance
	}
	for _, locomotion := range p.Locomotions {
		if locomotion.Duree.TotalSecondes > 0 {
			duration := locomotion.Duree.TotalSecondes
			update.Duration = &duration
		}
		if category := normalizeCategory(locomotion.Locomotion.Nom, locomotion.Locomotion.IDCategorie); category != "" {
			update.Category = &category
		}
	}
	if image := strings.TrimSpace(strings.ReplaceAll(tr.URLImage, `\/`, `/`)); image != "" {
		update.PhotoURLs = append(update.PhotoURLs, image)
	}
	update.Tags = cirkwiTags(tr)
	if difficulty := difficultyFromTags(update.Tags); difficulty != "" {
		update.Difficulty = &difficulty
	}
	appendComplementaryInfo(&update, tr)
	return update
}

func (p pageObject) primaryTranslation() translation {
	if tr, ok := p.Traduction["fr_FR"]; ok {
		return tr
	}
	for _, tr := range p.Traduction {
		return tr
	}
	return translation{}
}

func appendComplementaryInfo(update *wanderer.TrailUpdate, tr translation) {
	var lines []string
	for _, info := range tr.InformationsComplementaires {
		title := providerkit.CleanHTMLText(info.Titre)
		description := providerkit.CleanHTMLText(firstNonEmpty(info.HTMLDescription, info.Description))
		if description == "" {
			continue
		}
		if title != "" {
			lines = append(lines, title+": "+description)
		} else {
			lines = append(lines, description)
		}
	}
	if len(lines) == 0 {
		return
	}
	section := strings.Join(lines, "\n\n")
	if update.Description == nil || strings.TrimSpace(*update.Description) == "" {
		update.Description = &section
		return
	}
	description := strings.TrimSpace(*update.Description) + "\n\n" + section
	update.Description = &description
}

func cirkwiTags(tr translation) []string {
	var tags []string
	for _, item := range tr.Tags {
		tag := strings.TrimSpace(item.Tag.Tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func difficultyFromTags(tags []string) string {
	for _, tag := range tags {
		tag = strings.ToLower(tag)
		switch {
		case strings.Contains(tag, "tres_difficile"), strings.Contains(tag, "très_difficile"), strings.Contains(tag, "noir"):
			return wanderer.DifficultyDifficult
		case strings.Contains(tag, "difficile"), strings.Contains(tag, "rouge"):
			return wanderer.DifficultyDifficult
		case strings.Contains(tag, "facile"), strings.Contains(tag, "vert"):
			return wanderer.DifficultyEasy
		case strings.Contains(tag, "moyen"), strings.Contains(tag, "bleu"):
			return wanderer.DifficultyModerate
		}
	}
	return ""
}

func normalizeCategory(name string, categoryID int) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(name, "vélo"), strings.Contains(name, "velo"), strings.Contains(name, "bike"), strings.Contains(name, "cycl"):
		return "Biking"
	case strings.Contains(name, "course"), strings.Contains(name, "running"), strings.Contains(name, "trail"):
		return "Running"
	case strings.Contains(name, "marche"), strings.Contains(name, "rando"), strings.Contains(name, "hike"), categoryID == 1:
		return "Hiking"
	default:
		return ""
	}
}

func extractObjetJSON(page []byte) ([]byte, error) {
	const marker = "var objetJSON ="
	start := bytes.Index(page, []byte(marker))
	if start < 0 {
		return nil, fmt.Errorf("Cirkwi page had no objetJSON payload")
	}
	start += len(marker)
	for start < len(page) && (page[start] == ' ' || page[start] == '\t' || page[start] == '\n' || page[start] == '\r') {
		start++
	}
	if start >= len(page) || page[start] != '{' {
		return nil, fmt.Errorf("Cirkwi objetJSON payload did not start with an object")
	}
	end, ok := scanJSONObject(page, start)
	if !ok {
		return nil, fmt.Errorf("Cirkwi objetJSON payload was incomplete")
	}
	return page[start:end], nil
}

func scanJSONObject(data []byte, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(data); i++ {
		ch := data[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func extractID(parsed *url.URL) (string, bool) {
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, segment := range segments {
		if (segment == "circuit" || segment == "exporter-gpx") && i+1 < len(segments) {
			match := idPattern.FindStringSubmatch(segments[i+1])
			return matchID(match)
		}
	}
	return "", false
}

func matchID(match []string) (string, bool) {
	if len(match) < 2 || match[1] == "" {
		return "", false
	}
	return match[1], true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
