package providerkit

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tkrajina/gpxgo/gpx"
	"golang.org/x/net/html"

	"wanderer-import/internal/wanderer"
)

var numberPattern = regexp.MustCompile(`[-+]?[0-9]+(?:[,.][0-9]+)?`)
var imageURLPattern = regexp.MustCompile(`(?i)(https?:\\?/\\?/[^"'\s<>]+?\.(?:jpe?g|png|webp|svg)(?:\?[^"'\s<>]*)?|/[a-z0-9_./%?&=;:+#,-]+?\.(?:jpe?g|png|webp|svg)(?:\?[^"'\s<>]*)?)`)

func MetadataFromTrailData(source string, header http.Header, data []byte) wanderer.TrailUpdate {
	ext := TrailFileExtension(source)
	contentType := strings.ToLower(header.Get("Content-Type"))
	trimmed := bytes.TrimSpace(data)
	prefix := strings.ToLower(string(trimmed[:min(len(trimmed), 512)]))

	switch {
	case ext == ".gpx" || strings.Contains(contentType, "gpx") || strings.Contains(prefix, "<gpx"):
		return metadataFromGPX(data)
	case ext == ".kmz":
		return metadataFromKMZ(data)
	case ext == ".kml" || strings.Contains(contentType, "kml") || strings.Contains(prefix, "<kml"):
		return metadataFromKML(data)
	case ext == ".geojson" ||
		strings.Contains(contentType, "geojson") ||
		strings.Contains(contentType, "geo+json") ||
		strings.Contains(prefix, `"featurecollection"`) ||
		strings.Contains(prefix, `"linestring"`):
		return metadataFromGeoJSON(data)
	default:
		return wanderer.TrailUpdate{}
	}
}

func ExtractHTMLMetadata(base *url.URL, data []byte) wanderer.TrailUpdate {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return wanderer.TrailUpdate{}
	}

	var title string
	meta := map[string][]string{}
	var jsonLD []string
	var imageCandidates []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			name := strings.ToLower(node.Data)
			switch name {
			case "title":
				if title == "" {
					title = cleanText(nodeText(node))
				}
			case "meta":
				key := strings.ToLower(firstAttr(node, "property", "name", "itemprop"))
				value := firstAttr(node, "content")
				if key != "" && strings.TrimSpace(value) != "" {
					meta[key] = append(meta[key], cleanText(value))
				}
			case "link":
				if strings.Contains(strings.ToLower(firstAttr(node, "rel")), "image_src") {
					value := firstAttr(node, "href")
					if strings.TrimSpace(value) != "" {
						meta["image_src"] = append(meta["image_src"], cleanText(value))
					}
				}
			case "img", "source":
				imageCandidates = append(imageCandidates, imageCandidatesFromNode(node)...)
			case "script":
				if strings.Contains(strings.ToLower(firstAttr(node, "type")), "ld+json") {
					text := strings.TrimSpace(nodeText(node))
					if text != "" {
						jsonLD = append(jsonLD, text)
					}
				}
			}
			imageCandidates = append(imageCandidates, backgroundImageCandidates(firstAttr(node, "style"))...)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	var update wanderer.TrailUpdate
	setString(&update.Name, firstMeta(meta, "og:title", "twitter:title", "title", "name"))
	setString(&update.Description, firstMeta(meta, "og:description", "twitter:description", "description"))
	setString(&update.Location, firstMeta(meta, "geo.placename", "place:location:locality", "og:locality"))
	update.PhotoURLs = mergeStrings(update.PhotoURLs, imageURLsFromMeta(base, meta)...)
	update.PhotoURLs = mergeStrings(update.PhotoURLs, imageCandidates...)
	update.PhotoURLs = mergeStrings(update.PhotoURLs, imageURLsFromText(string(data))...)

	if update.Name == nil {
		setString(&update.Name, title)
	}
	if update.Description == nil {
		setString(&update.Description, descriptionFallback(doc))
	}
	if update.Category == nil {
		setString(&update.Category, categoryFromDocument(doc, title))
	}
	if lat, lon, ok := latLonFromMeta(meta); ok {
		update.Lat = &lat
		update.Lon = &lon
	}
	update.Tags = tagsFromMeta(meta)

	for _, raw := range jsonLD {
		applyJSONLD(&update, []byte(raw))
	}
	applyEmbeddedRoutePayloads(&update, data)

	if update.Name != nil {
		*update.Name = cleanupTitle(*update.Name, base)
	}
	update.PhotoURLs = resolvePhotoURLs(base, update.PhotoURLs)
	return update
}

func MetadataFromPoints(points []Point) wanderer.TrailUpdate {
	var update wanderer.TrailUpdate
	if len(points) == 0 {
		return update
	}
	update.Lat = &points[0].Lat
	update.Lon = &points[0].Lon

	var distance, gain, loss float64
	var previousEle *float64
	for i := 1; i < len(points); i++ {
		distance += gpx.HaversineDistance(points[i-1].Lat, points[i-1].Lon, points[i].Lat, points[i].Lon)
	}
	for _, point := range points {
		if point.Ele == nil {
			continue
		}
		if previousEle != nil {
			delta := *point.Ele - *previousEle
			if delta > 0 {
				gain += delta
			} else {
				loss -= delta
			}
		}
		value := *point.Ele
		previousEle = &value
	}
	if distance > 0 {
		update.Distance = &distance
	}
	if gain > 0 {
		update.ElevationGain = &gain
	}
	if loss > 0 {
		update.ElevationLoss = &loss
	}
	return update
}

func PointsFromGeoJSON(data []byte, order string) ([]Point, error) {
	var payload struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
		Geometry    geoJSONGeometry `json:"geometry"`
		Features    []struct {
			Geometry geoJSONGeometry `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	var geometries []geoJSONGeometry
	switch strings.ToLower(payload.Type) {
	case "featurecollection":
		for _, feature := range payload.Features {
			geometries = append(geometries, feature.Geometry)
		}
	case "feature":
		geometries = append(geometries, payload.Geometry)
	default:
		geometries = append(geometries, geoJSONGeometry{
			Type:        payload.Type,
			Coordinates: payload.Coordinates,
		})
	}

	var points []Point
	for _, geometry := range geometries {
		geometryPoints, err := pointsFromGeoJSONGeometry(geometry, order)
		if err != nil {
			continue
		}
		points = append(points, geometryPoints...)
	}
	if len(points) == 0 {
		return nil, jsonGeoError("GeoJSON had no LineString coordinates")
	}
	return points, nil
}

type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type jsonGeoError string

func (e jsonGeoError) Error() string {
	return string(e)
}

func metadataFromGPX(data []byte) wanderer.TrailUpdate {
	doc, err := gpx.ParseBytes(data)
	if err != nil {
		return wanderer.TrailUpdate{}
	}

	update := MetadataFromPoints(gpxPrimaryPoints(doc))
	setString(&update.Name, doc.Name)
	setString(&update.Description, doc.Description)
	if doc.Time != nil && !doc.Time.IsZero() {
		date := doc.Time.UTC().Format(time.RFC3339)
		update.Date = &date
	}
	if duration := doc.Duration(); duration > 0 {
		update.Duration = &duration
	}
	if distance := doc.Length2D(); distance > 0 {
		update.Distance = &distance
	}
	upDown := doc.UphillDownhill()
	if upDown.Uphill > 0 {
		update.ElevationGain = &upDown.Uphill
	}
	if upDown.Downhill > 0 {
		update.ElevationLoss = &upDown.Downhill
	}
	for _, track := range doc.Tracks {
		setString(&update.Name, track.Name)
		setString(&update.Description, firstNonEmpty(track.Description, track.Comment))
		update.Tags = mergeStringTags(update.Tags, track.Type)
	}
	for _, route := range doc.Routes {
		setString(&update.Name, route.Name)
		setString(&update.Description, firstNonEmpty(route.Description, route.Comment))
		update.Tags = mergeStringTags(update.Tags, route.Type)
	}
	update.Tags = mergeStringTags(update.Tags, doc.Keywords)
	return update
}

func metadataFromGeoJSON(data []byte) wanderer.TrailUpdate {
	points, err := PointsFromGeoJSON(data, "lonlat")
	if err != nil {
		return wanderer.TrailUpdate{}
	}
	update := MetadataFromPoints(points)
	for _, properties := range geoJSONProperties(data) {
		applyJSONObject(&update, properties)
	}
	return update
}

func metadataFromKMZ(data []byte) wanderer.TrailUpdate {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return wanderer.TrailUpdate{}
	}
	for _, file := range reader.File {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".kml") {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			continue
		}
		kmlData, readErr := io.ReadAll(io.LimitReader(rc, 64<<20))
		_ = rc.Close()
		if readErr == nil {
			return metadataFromKML(kmlData)
		}
	}
	return wanderer.TrailUpdate{}
}

func metadataFromKML(data []byte) wanderer.TrailUpdate {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var element string
	var text strings.Builder
	var name string
	var description string
	var points []Point

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return wanderer.TrailUpdate{}
		}
		switch typed := token.(type) {
		case xml.StartElement:
			local := strings.ToLower(typed.Name.Local)
			if local == "name" || local == "description" || local == "coordinates" {
				element = local
				text.Reset()
			}
		case xml.CharData:
			if element != "" {
				text.Write([]byte(typed))
			}
		case xml.EndElement:
			if strings.EqualFold(typed.Name.Local, element) {
				value := cleanText(text.String())
				switch element {
				case "name":
					if name == "" {
						name = value
					}
				case "description":
					if description == "" {
						description = value
					}
				case "coordinates":
					points = append(points, parseKMLCoordinates(value)...)
				}
				element = ""
				text.Reset()
			}
		}
	}

	update := MetadataFromPoints(points)
	setString(&update.Name, name)
	setString(&update.Description, description)
	return update
}

func gpxPrimaryPoints(doc *gpx.GPX) []Point {
	var points []Point
	for _, track := range doc.Tracks {
		for _, segment := range track.Segments {
			for _, point := range segment.Points {
				points = append(points, pointFromGPX(point))
			}
		}
	}
	if len(points) > 0 {
		return points
	}
	for _, route := range doc.Routes {
		for _, point := range route.Points {
			points = append(points, pointFromGPX(point))
		}
	}
	if len(points) > 0 {
		return points
	}
	for _, point := range doc.Waypoints {
		points = append(points, pointFromGPX(point))
	}
	return points
}

func pointFromGPX(point gpx.GPXPoint) Point {
	result := Point{Lat: point.Point.Latitude, Lon: point.Point.Longitude}
	if point.Elevation.NotNull() {
		value := point.Elevation.Value()
		result.Ele = &value
	}
	return result
}

func geoJSONProperties(data []byte) []map[string]any {
	var payload struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Features   []struct {
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	var result []map[string]any
	if len(payload.Properties) > 0 {
		result = append(result, payload.Properties)
	}
	for _, feature := range payload.Features {
		if len(feature.Properties) > 0 {
			result = append(result, feature.Properties)
		}
	}
	return result
}

func pointsFromGeoJSONGeometry(geometry geoJSONGeometry, order string) ([]Point, error) {
	switch strings.ToLower(geometry.Type) {
	case "linestring":
		var raw [][]float64
		if err := json.Unmarshal(geometry.Coordinates, &raw); err != nil {
			return nil, err
		}
		return pointsFromRawCoordinates(raw, order), nil
	case "multilinestring":
		var raw [][][]float64
		if err := json.Unmarshal(geometry.Coordinates, &raw); err != nil {
			return nil, err
		}
		var points []Point
		for _, line := range raw {
			points = append(points, pointsFromRawCoordinates(line, order)...)
		}
		return points, nil
	default:
		return nil, jsonGeoError("unsupported GeoJSON geometry")
	}
}

func pointsFromRawCoordinates(raw [][]float64, order string) []Point {
	points := make([]Point, 0, len(raw))
	for _, coordinate := range raw {
		if len(coordinate) < 2 {
			continue
		}
		var lat, lon float64
		switch order {
		case "latlon":
			lat, lon = coordinate[0], coordinate[1]
		default:
			lon, lat = coordinate[0], coordinate[1]
		}
		var ele *float64
		if len(coordinate) >= 3 {
			value := coordinate[2]
			ele = &value
		}
		points = append(points, Point{Lat: lat, Lon: lon, Ele: ele})
	}
	return points
}

func parseKMLCoordinates(value string) []Point {
	var points []Point
	for _, token := range strings.Fields(value) {
		parts := strings.Split(token, ",")
		if len(parts) < 2 {
			continue
		}
		lon, lonOK := parseFloatString(parts[0])
		lat, latOK := parseFloatString(parts[1])
		if !latOK || !lonOK {
			continue
		}
		var ele *float64
		if len(parts) >= 3 {
			if value, ok := parseFloatString(parts[2]); ok {
				ele = &value
			}
		}
		points = append(points, Point{Lat: lat, Lon: lon, Ele: ele})
	}
	return points
}

func applyJSONLD(update *wanderer.TrailUpdate, data []byte) {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	for _, object := range jsonLDObjects(payload) {
		applyJSONObject(update, object)
	}
}

func jsonLDObjects(value any) []map[string]any {
	var objects []map[string]any
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			objects = append(objects, jsonLDObjects(item)...)
		}
	case map[string]any:
		objects = append(objects, typed)
		for _, key := range []string{"@graph", "mainEntity", "about", "itemReviewed"} {
			if nested, ok := typed[key]; ok {
				objects = append(objects, jsonLDObjects(nested)...)
			}
		}
	}
	return objects
}

func applyJSONObject(update *wanderer.TrailUpdate, object map[string]any) {
	setString(&update.Name, stringValue(object["name"]))
	setString(&update.Description, stringValue(object["description"]))
	setString(&update.Category, categoryValue(stringValue(object["category"])))
	update.PhotoURLs = mergeStrings(update.PhotoURLs, imageValues(object["image"])...)
	update.PhotoURLs = mergeStrings(update.PhotoURLs, imageValues(object["photo"])...)
	update.PhotoURLs = mergeStrings(update.PhotoURLs, imageValues(object["primaryImageOfPage"])...)
	if update.Duration == nil {
		if duration := parseDurationSeconds(stringValue(object["duration"])); duration != nil {
			update.Duration = duration
		}
	}
	if update.Distance == nil {
		if distance := distanceMetersFromValue(object["distance"]); distance != nil {
			update.Distance = distance
		}
	}
	if geo, ok := object["geo"].(map[string]any); ok {
		lat, ok := floatValue(geo["latitude"])
		setFloat(&update.Lat, lat, ok)
		lon, ok := floatValue(geo["longitude"])
		setFloat(&update.Lon, lon, ok)
	}
	if address, ok := object["address"].(map[string]any); ok {
		setString(&update.Location, addressLocation(address))
	}
	applyAdditionalProperties(update, object["additionalProperty"])
	if tag := difficultyValue(stringValue(object["difficulty"])); tag != "" {
		setString(&update.Difficulty, tag)
	}
	update.Tags = mergeStringTags(update.Tags, routeTagsFromObject(object)...)
}

func applyAdditionalProperties(update *wanderer.TrailUpdate, value any) {
	properties, ok := value.([]any)
	if !ok {
		return
	}
	for _, item := range properties {
		property, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := normalizeKey(stringValue(property["name"]))
		value := propertyValueText(property)

		switch {
		case strings.Contains(name, "distance"):
			if update.Distance == nil {
				update.Distance = parseDistanceMeters(value)
			}
		case strings.Contains(name, "duree") || strings.Contains(name, "duration") || strings.Contains(name, "temps"):
			if update.Duration == nil {
				update.Duration = parseDurationSeconds(value)
			}
		case strings.Contains(name, "denivele positif") || strings.Contains(name, "elevation gain") || strings.Contains(name, "ascent"):
			if update.ElevationGain == nil {
				update.ElevationGain = parseMeters(value)
			}
		case strings.Contains(name, "denivele negatif") || strings.Contains(name, "elevation loss") || strings.Contains(name, "descent"):
			if update.ElevationLoss == nil {
				update.ElevationLoss = parseMeters(value)
			}
		case strings.Contains(name, "difficulte") || strings.Contains(name, "difficulty"):
			if difficulty := difficultyValue(value); difficulty != "" {
				setString(&update.Difficulty, difficulty)
			}
		case strings.Contains(name, "sport"):
			update.Tags = mergeStringTags(update.Tags, value)
			setString(&update.Category, categoryValue(value))
		}
	}
}

func applyEmbeddedRoutePayloads(update *wanderer.TrailUpdate, data []byte) {
	for _, raw := range extractFunctionJSONObjects(string(data), "DsioDetail.initialize") {
		var payload struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			TraceGPS    struct {
				Balises []struct {
					NumeroEtape int    `json:"numero_etape"`
					Titre       string `json:"titre"`
					Descriptif  string `json:"descriptif"`
				} `json:"balises"`
			} `json:"tracegps"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue
		}
		setString(&update.Name, payload.Title)
		setString(&update.Description, payload.Description)
		var steps []RouteStep
		for _, balise := range payload.TraceGPS.Balises {
			steps = append(steps, RouteStep{
				Number:      balise.NumeroEtape,
				Title:       balise.Titre,
				Description: balise.Descriptif,
			})
		}
		AppendRouteSteps(update, steps)
	}
}

func extractFunctionJSONObjects(text, functionName string) []string {
	var objects []string
	for offset := 0; ; {
		index := strings.Index(text[offset:], functionName+"(")
		if index < 0 {
			return objects
		}
		index += offset
		start := strings.Index(text[index:], "{")
		if start < 0 {
			return objects
		}
		start += index
		if object, end, ok := balancedJSONObject(text, start); ok {
			objects = append(objects, object)
			offset = end
		} else {
			offset = start + 1
		}
	}
}

func balancedJSONObject(text string, start int) (string, int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
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
				return text[start : i+1], i + 1, true
			}
		}
	}
	return "", start, false
}

type RouteStep struct {
	Number      int
	Title       string
	Description string
}

func AppendRouteSteps(update *wanderer.TrailUpdate, steps []RouteStep) {
	var lines []string
	for i, step := range steps {
		description := CleanHTMLText(step.Description)
		if description == "" {
			continue
		}
		number := step.Number
		if number <= 0 {
			number = i + 1
		}
		title := CleanHTMLText(step.Title)
		if title != "" {
			lines = append(lines, strconv.Itoa(number)+". "+title+": "+description)
		} else {
			lines = append(lines, strconv.Itoa(number)+". "+description)
		}
	}
	if len(lines) == 0 {
		return
	}
	section := "Itinerary steps:\n" + strings.Join(lines, "\n")
	if update.Description == nil || strings.TrimSpace(*update.Description) == "" {
		update.Description = &section
		return
	}
	if strings.Contains(*update.Description, "Itinerary steps:") {
		return
	}
	description := strings.TrimSpace(*update.Description) + "\n\n" + section
	update.Description = &description
}

func CleanHTMLText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	nodes, err := html.ParseFragment(strings.NewReader(value), nil)
	if err != nil {
		return cleanText(value)
	}
	var parts []string
	for _, node := range nodes {
		parts = append(parts, nodeText(node))
	}
	return cleanText(strings.Join(parts, " "))
}

func firstAttr(node *html.Node, names ...string) string {
	for _, name := range names {
		for _, attr := range node.Attr {
			if strings.EqualFold(attr.Key, name) {
				return attr.Val
			}
		}
	}
	return ""
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

func firstMeta(meta map[string][]string, keys ...string) string {
	for _, key := range keys {
		values := meta[strings.ToLower(key)]
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func imageURLsFromMeta(base *url.URL, meta map[string][]string) []string {
	var urls []string
	for _, key := range []string{
		"og:image",
		"og:image:url",
		"og:image:secure_url",
		"twitter:image",
		"twitter:image:src",
		"image",
		"thumbnail",
		"thumbnailurl",
		"image_src",
	} {
		for _, value := range meta[key] {
			if resolved, ok := ResolveReference(base, value); ok && looksLikePhotoURL(resolved) {
				urls = mergeStrings(urls, resolved)
			}
		}
	}
	return urls
}

func imageCandidatesFromNode(node *html.Node) []string {
	var values []string
	for _, attr := range []string{
		"src",
		"data-src",
		"data-lazy-src",
		"data-original",
		"data-url",
		"data-image",
		"content",
	} {
		values = append(values, firstAttr(node, attr))
	}
	for _, attr := range []string{"srcset", "data-srcset"} {
		values = append(values, srcsetCandidates(firstAttr(node, attr))...)
	}
	return nonEmptyImageCandidates(values)
}

func srcsetCandidates(value string) []string {
	var urls []string
	for _, candidate := range strings.Split(value, ",") {
		fields := strings.Fields(strings.TrimSpace(candidate))
		if len(fields) == 0 {
			continue
		}
		urls = append(urls, fields[0])
	}
	return urls
}

func backgroundImageCandidates(style string) []string {
	var urls []string
	for _, match := range regexp.MustCompile(`(?i)url\(([^)]+)\)`).FindAllStringSubmatch(style, -1) {
		if len(match) > 1 {
			urls = append(urls, strings.Trim(match[1], ` "'`))
		}
	}
	return nonEmptyImageCandidates(urls)
}

func imageURLsFromText(text string) []string {
	text = strings.ReplaceAll(text, `\/`, `/`)
	text = strings.ReplaceAll(text, `\u002F`, `/`)
	text = strings.ReplaceAll(text, `\u0026`, `&`)
	var urls []string
	for _, match := range imageURLPattern.FindAllString(text, -1) {
		urls = mergeStrings(urls, match)
	}
	return urls
}

func nonEmptyImageCandidates(values []string) []string {
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func resolvePhotoURLs(base *url.URL, values []string) []string {
	var urls []string
	for _, value := range values {
		resolved, ok := ResolveReference(base, value)
		if !ok || !looksLikePhotoURL(resolved) {
			continue
		}
		urls = mergeStrings(urls, resolved)
	}
	return urls
}

func looksLikePhotoURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	rawQuery, _ := url.QueryUnescape(parsed.RawQuery)
	all := path + "?" + strings.ToLower(rawQuery)
	for _, reject := range []string{
		"logo",
		"avatar",
		"icon",
		"marker",
		"sprite",
		"placeholder",
		"theme",
		"/img/",
		"label",
		"marque",
		"popup",
		"menu",
		"cadenas",
		"triangle",
		"signin",
		"facebook",
		"google",
		"poi-patrimony",
		"flora",
		"faune",
		"vigilance",
		"profil.png",
		"hide.png",
		"actions.png",
		"sendedit.png",
		"suivi.png",
	} {
		if strings.Contains(all, reject) {
			return false
		}
	}
	if strings.HasSuffix(path, "/x.png") || strings.HasSuffix(path, "/x.svg") {
		return false
	}
	if strings.HasSuffix(path, "/upload.png") || strings.HasSuffix(path, "/3d.png") {
		return false
	}
	return strings.HasSuffix(path, ".jpg") ||
		strings.HasSuffix(path, ".jpeg") ||
		strings.HasSuffix(path, ".png") ||
		strings.HasSuffix(path, ".webp") ||
		strings.HasSuffix(path, ".svg") ||
		strings.Contains(all, "image") ||
		strings.Contains(all, "photo") ||
		strings.Contains(all, "media") ||
		strings.Contains(all, "upload")
}

func imageValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var values []string
		for _, item := range typed {
			values = append(values, imageValues(item)...)
		}
		return values
	case map[string]any:
		var values []string
		for _, key := range []string{"url", "contentUrl", "thumbnailUrl"} {
			values = append(values, imageValues(typed[key])...)
		}
		return values
	default:
		return nil
	}
}

func descriptionFallback(doc *html.Node) string {
	var best string
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, preferred bool) {
		if node.Type == html.ElementNode {
			name := strings.ToLower(node.Data)
			if name == "script" || name == "style" || name == "nav" || name == "footer" || name == "header" {
				return
			}
			if name == "main" || name == "article" || attrContainsAny(node, "class", "description", "resume", "intro", "content", "presentation") || attrContainsAny(node, "id", "description", "resume", "intro", "content", "presentation") {
				preferred = true
			}
			if name == "p" || name == "div" {
				candidate := cleanText(nodeText(node))
				if descriptionCandidate(candidate) && (best == "" || preferred || len(candidate) > len(best)) {
					best = candidate
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, preferred)
		}
	}
	walk(doc, false)
	if len(best) > 800 {
		return strings.TrimSpace(best[:800])
	}
	return best
}

func descriptionCandidate(value string) bool {
	value = cleanText(value)
	if len(value) < 60 || len(value) > 3000 {
		return false
	}
	normalized := normalizeKey(value)
	for _, reject := range []string{"cookie", "javascript", "newsletter", "instagram", "facebook", "menu", "navigation", "copyright"} {
		if strings.Contains(normalized, reject) {
			return false
		}
	}
	return strings.Count(value, " ") >= 8
}

func attrContainsAny(node *html.Node, attr string, needles ...string) bool {
	value := strings.ToLower(firstAttr(node, attr))
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func latLonFromMeta(meta map[string][]string) (float64, float64, bool) {
	lat, latOK := parseFloatString(firstMeta(meta, "place:location:latitude", "geo.position:latitude", "latitude"))
	lon, lonOK := parseFloatString(firstMeta(meta, "place:location:longitude", "geo.position:longitude", "longitude"))
	if latOK && lonOK {
		return lat, lon, true
	}
	position := firstMeta(meta, "geo.position", "icbm")
	parts := strings.FieldsFunc(position, func(r rune) bool { return r == ';' || r == ',' || r == ' ' })
	if len(parts) >= 2 {
		lat, latOK = parseFloatString(parts[0])
		lon, lonOK = parseFloatString(parts[1])
		if latOK && lonOK {
			return lat, lon, true
		}
	}
	return 0, 0, false
}

func tagsFromMeta(meta map[string][]string) []string {
	var tags []string
	for _, key := range []string{"keywords", "article:tag"} {
		for _, value := range meta[key] {
			for _, part := range strings.Split(value, ",") {
				tags = mergeStringTags(tags, part)
			}
		}
	}
	return tags
}

func routeTagsFromObject(object map[string]any) []string {
	var tags []string
	for _, key := range []string{"category", "keywords", "exerciseType"} {
		switch typed := object[key].(type) {
		case string:
			for _, part := range strings.Split(typed, ",") {
				tags = mergeStringTags(tags, part)
			}
		case []any:
			for _, item := range typed {
				tags = mergeStringTags(tags, stringValue(item))
			}
		}
	}
	return tags
}

func propertyValueText(property map[string]any) string {
	value := stringValue(property["value"])
	if unit := stringValue(property["unitText"]); unit != "" {
		value += " " + unit
	}
	return value
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return cleanText(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case json.Number:
		return typed.String()
	case map[string]any:
		for _, key := range []string{"name", "value", "@id"} {
			if value := stringValue(typed[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		return parseFloatString(typed)
	default:
		return 0, false
	}
}

func parseFloatString(value string) (float64, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", ".")
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil
}

func distanceMetersFromValue(value any) *float64 {
	switch typed := value.(type) {
	case map[string]any:
		text := stringValue(typed["value"])
		if unit := stringValue(typed["unitText"]); unit != "" {
			text += " " + unit
		}
		return parseDistanceMeters(text)
	default:
		return parseDistanceMeters(stringValue(value))
	}
}

func parseDistanceMeters(value string) *float64 {
	value = normalizeNumberText(value)
	number := firstNumber(value)
	if number == nil {
		return nil
	}
	switch {
	case strings.Contains(value, "km"):
		*number *= 1000
	case strings.Contains(value, "mi"):
		*number *= 1609.344
	}
	return number
}

func parseMeters(value string) *float64 {
	value = normalizeNumberText(value)
	number := firstNumber(value)
	if number == nil {
		return nil
	}
	if strings.Contains(value, "km") {
		*number *= 1000
	}
	return number
}

func parseDurationSeconds(value string) *float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if duration, ok := parseISODuration(value); ok {
		seconds := duration.Seconds()
		return &seconds
	}
	normalized := normalizeNumberText(strings.ToLower(value))
	normalized = strings.ReplaceAll(normalized, "heures", "h")
	normalized = strings.ReplaceAll(normalized, "heure", "h")
	normalized = strings.ReplaceAll(normalized, "hours", "h")
	normalized = strings.ReplaceAll(normalized, "hour", "h")
	normalized = strings.ReplaceAll(normalized, "minutes", "m")
	normalized = strings.ReplaceAll(normalized, "minute", "m")
	normalized = strings.ReplaceAll(normalized, "mins", "m")
	normalized = strings.ReplaceAll(normalized, "min", "m")
	normalized = strings.ReplaceAll(normalized, " ", "")
	if strings.Contains(normalized, "h") {
		parts := strings.SplitN(normalized, "h", 2)
		hours, _ := strconv.ParseFloat(parts[0], 64)
		minutes := 0.0
		if len(parts) > 1 {
			minuteText := strings.TrimSuffix(parts[1], "m")
			if minuteText != "" {
				minutes, _ = strconv.ParseFloat(minuteText, 64)
			}
		}
		seconds := hours*3600 + minutes*60
		return &seconds
	}
	if strings.HasSuffix(normalized, "m") {
		minutes, err := strconv.ParseFloat(strings.TrimSuffix(normalized, "m"), 64)
		if err == nil {
			seconds := minutes * 60
			return &seconds
		}
	}
	if strings.Contains(normalized, ":") {
		parts := strings.Split(normalized, ":")
		if len(parts) >= 2 {
			hours, _ := strconv.ParseFloat(parts[0], 64)
			minutes, _ := strconv.ParseFloat(parts[1], 64)
			seconds := hours*3600 + minutes*60
			return &seconds
		}
	}
	if number := firstNumber(normalized); number != nil {
		if strings.Contains(normalized, "s") {
			return number
		}
		seconds := *number * 60
		return &seconds
	}
	return nil
}

func parseISODuration(value string) (time.Duration, bool) {
	match := regexp.MustCompile(`(?i)^P(?:([0-9.]+)D)?(?:T(?:([0-9.]+)H)?(?:([0-9.]+)M)?(?:([0-9.]+)S)?)?$`).FindStringSubmatch(value)
	if len(match) == 0 {
		return 0, false
	}
	var seconds float64
	multipliers := []float64{86400, 3600, 60, 1}
	for i, multiplier := range multipliers {
		if match[i+1] == "" {
			continue
		}
		value, err := strconv.ParseFloat(match[i+1], 64)
		if err != nil {
			return 0, false
		}
		seconds += value * multiplier
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func difficultyValue(value string) string {
	value = normalizeKey(value)
	switch {
	case strings.Contains(value, "facile") || strings.Contains(value, "easy"):
		return wanderer.DifficultyEasy
	case strings.Contains(value, "modere") || strings.Contains(value, "moyen") || strings.Contains(value, "moderate") || strings.Contains(value, "intermediate"):
		return wanderer.DifficultyModerate
	case strings.Contains(value, "difficile") || strings.Contains(value, "hard") || strings.Contains(value, "challenging"):
		return wanderer.DifficultyDifficult
	default:
		return ""
	}
}

func categoryFromDocument(doc *html.Node, title string) string {
	if category := categoryValue(title); category != "" {
		return category
	}
	var category string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil || category != "" {
			return
		}
		if node.Type == html.ElementNode {
			class := firstAttr(node, "class")
			text := nodeText(node)
			switch {
			case strings.Contains(class, "lae-icon-velo"):
				category = "Biking"
				return
			case strings.Contains(class, "lae-icon-hiking"):
				category = "Hiking"
				return
			case attrContainsAny(node, "class", "lgrid-column-center-middle"):
				if value := categoryValue(text); value != "" {
					category = value
					return
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return category
}

func categoryValue(value string) string {
	value = normalizeKey(value)
	switch {
	case strings.Contains(value, "cyclo") ||
		strings.Contains(value, "cycling") ||
		strings.Contains(value, "biking") ||
		strings.Contains(value, "bicycle") ||
		strings.Contains(value, "bike") ||
		strings.Contains(value, "velo") ||
		strings.Contains(value, "vtt"):
		return "Biking"
	case strings.Contains(value, "randonnee") ||
		strings.Contains(value, "hiking") ||
		strings.Contains(value, "hike") ||
		strings.Contains(value, "pedestre"):
		return "Hiking"
	case strings.Contains(value, "walking") ||
		strings.Contains(value, "walk"):
		return "Walking"
	case strings.Contains(value, "canoe") || strings.Contains(value, "kayak"):
		return "Canoeing"
	case strings.Contains(value, "climbing") || strings.Contains(value, "escalade"):
		return "Climbing"
	case strings.Contains(value, "ski"):
		return "Skiing"
	default:
		return ""
	}
}

func addressLocation(address map[string]any) string {
	parts := []string{
		stringValue(address["addressLocality"]),
		stringValue(address["addressRegion"]),
		stringValue(address["addressCountry"]),
	}
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, ", ")
}

func setString(target **string, value string) {
	value = cleanText(value)
	if *target != nil || value == "" {
		return
	}
	*target = &value
}

func setFloat(target **float64, value float64, ok bool) {
	if *target != nil || !ok {
		return
	}
	*target = &value
}

func mergeStringTags(existing []string, values ...string) []string {
	seen := map[string]struct{}{}
	var merged []string
	for _, tag := range existing {
		tag = cleanTag(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		seen[key] = struct{}{}
		merged = append(merged, tag)
	}
	for _, value := range values {
		for _, tag := range strings.Split(value, ",") {
			tag = cleanTag(tag)
			if tag == "" {
				continue
			}
			key := strings.ToLower(tag)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, tag)
		}
	}
	return merged
}

func mergeStrings(existing []string, values ...string) []string {
	seen := map[string]struct{}{}
	var merged []string
	for _, value := range existing {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		seen[key] = struct{}{}
		merged = append(merged, value)
	}
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
		merged = append(merged, value)
	}
	return merged
}

func cleanTag(value string) string {
	value = cleanText(value)
	value = strings.Trim(value, "# ")
	if len(value) > 40 {
		return ""
	}
	return value
}

func cleanText(value string) string {
	value = strings.ReplaceAll(value, "\u202f", " ")
	value = strings.ReplaceAll(value, "\u00a0", " ")
	return strings.Join(strings.Fields(value), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cleanupTitle(value string, base *url.URL) string {
	value = cleanText(value)
	if base == nil || base.Hostname() == "" {
		return value
	}
	host := strings.TrimPrefix(base.Hostname(), "www.")
	for _, sep := range []string{" | ", " - ", " – "} {
		parts := strings.Split(value, sep)
		if len(parts) < 2 {
			continue
		}
		last := strings.ToLower(parts[len(parts)-1])
		fields := strings.Fields(last)
		if (len(fields) > 0 && strings.Contains(host, fields[0])) || strings.Contains(last, "tourisme") || strings.Contains(last, "rando") {
			return strings.TrimSpace(strings.Join(parts[:len(parts)-1], sep))
		}
	}
	return value
}

func normalizeNumberText(value string) string {
	value = strings.ToLower(cleanText(value))
	value = strings.ReplaceAll(value, ",", ".")
	return value
}

func firstNumber(value string) *float64 {
	match := numberPattern.FindString(value)
	if match == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(match, ",", "."), 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func normalizeKey(value string) string {
	value = strings.ToLower(cleanText(value))
	replacements := map[string]string{
		"é": "e", "è": "e", "ê": "e", "ë": "e",
		"à": "a", "â": "a",
		"î": "i", "ï": "i",
		"ô": "o",
		"ù": "u", "û": "u", "ü": "u",
		"ç": "c",
	}
	for old, newValue := range replacements {
		value = strings.ReplaceAll(value, old, newValue)
	}
	return value
}
