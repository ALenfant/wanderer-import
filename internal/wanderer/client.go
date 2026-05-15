package wanderer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const userAgent = "wanderer-import/0.1"

const (
	DifficultyEasy      = "easy"
	DifficultyModerate  = "moderate"
	DifficultyDifficult = "difficult"
)

type Client struct {
	baseURL      *url.URL
	httpClient   *http.Client
	token        string
	pbAuthCookie string
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func WithToken(token string) Option {
	return func(c *Client) {
		c.token = strings.TrimSpace(token)
	}
}

func WithPBAuthCookie(cookie string) Option {
	return func(c *Client) {
		c.pbAuthCookie = strings.TrimSpace(cookie)
	}
}

func NewClient(rawBaseURL string, opts ...Option) (*Client, error) {
	baseURL, err := NormalizeBaseURL(rawBaseURL)
	if err != nil {
		return nil, err
	}

	jar, _ := cookiejar.New(nil)
	client := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Jar:     jar,
		},
	}

	for _, opt := range opts {
		opt(client)
	}

	if client.httpClient.Jar == nil {
		client.httpClient.Jar = jar
	}

	return client, nil
}

func NormalizeBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "http://localhost:3000"
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Wanderer URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Wanderer URL %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported Wanderer URL scheme %q", parsed.Scheme)
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawPath = ""

	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/api/v1"
	} else if !strings.HasSuffix(path, "/api/v1") {
		path += "/api/v1"
	}
	parsed.Path = path

	return parsed, nil
}

func (c *Client) BaseURL() string {
	return c.baseURL.String()
}

func (c *Client) Login(ctx context.Context, login, password string) error {
	login = strings.TrimSpace(login)
	if login == "" {
		return errors.New("username or email is required")
	}
	if password == "" {
		return errors.New("password is required")
	}

	body := map[string]string{"password": password}
	if strings.Contains(login, "@") {
		body["email"] = login
	} else {
		body["username"] = login
	}

	var response loginResponse
	if err := c.postJSON(ctx, "/auth/login", body, &response); err != nil {
		return err
	}
	if response.Token != "" {
		c.token = response.Token
	} else if response.Record.Token != "" {
		c.token = response.Record.Token
	}
	return nil
}

type loginResponse struct {
	Token  string `json:"token"`
	Record struct {
		Token string `json:"token"`
	} `json:"record"`
}

type UploadOptions struct {
	Name             string
	IgnoreDuplicates bool
}

type RemotePhoto struct {
	URL      string
	Filename string
	Data     []byte
}

func (c *Client) UploadTrail(ctx context.Context, filename string, body io.Reader, opts UploadOptions) (*Trail, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "trail.gpx"
	}

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	writeErr := make(chan error, 1)

	go func() {
		var err error
		defer close(writeErr)

		part, err := multipartWriter.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.Copy(part, body)
		}
		if err == nil && opts.Name != "" {
			err = multipartWriter.WriteField("name", opts.Name)
		}
		if err == nil && opts.IgnoreDuplicates {
			err = multipartWriter.WriteField("ignoreDuplicates", "true")
		}
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = writer.CloseWithError(err)
			writeErr <- err
			return
		}
		writeErr <- writer.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint("/trail/upload").String(), reader)
	if err != nil {
		_ = reader.CloseWithError(err)
		<-writeErr
		return nil, err
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())

	var trail Trail
	err = c.doJSON(req, &trail, http.StatusOK, http.StatusCreated)
	if err != nil {
		_ = reader.CloseWithError(err)
	}
	if writeErr := <-writeErr; err == nil && writeErr != nil {
		err = writeErr
	}
	if err != nil {
		return nil, err
	}
	return &trail, nil
}

func (c *Client) UploadTrailPhotoURLs(ctx context.Context, id string, urls []string) (*Trail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("trail ID is required")
	}
	photos, err := c.downloadRemotePhotos(ctx, urls)
	if err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return nil, errors.New("no usable photos to upload")
	}
	return c.uploadTrailPhotos(ctx, id, photos)
}

type TrailUpdate struct {
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	Location      *string  `json:"location,omitempty"`
	Date          *string  `json:"date,omitempty"`
	Difficulty    *string  `json:"difficulty,omitempty"`
	Category      *string  `json:"category,omitempty"`
	Public        *bool    `json:"public,omitempty"`
	Lat           *float64 `json:"lat,omitempty"`
	Lon           *float64 `json:"lon,omitempty"`
	Distance      *float64 `json:"distance,omitempty"`
	ElevationGain *float64 `json:"elevation_gain,omitempty"`
	ElevationLoss *float64 `json:"elevation_loss,omitempty"`
	Duration      *float64 `json:"duration,omitempty"`
	Thumbnail     *int     `json:"thumbnail,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	PhotoURLs     []string `json:"photo_urls,omitempty"`
}

func NormalizeDifficulty(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DifficultyEasy:
		return DifficultyEasy, true
	case DifficultyModerate:
		return DifficultyModerate, true
	case DifficultyDifficult:
		return DifficultyDifficult, true
	default:
		return "", false
	}
}

func (u TrailUpdate) Empty() bool {
	return u.Name == nil &&
		u.Description == nil &&
		u.Location == nil &&
		u.Date == nil &&
		u.Difficulty == nil &&
		u.Category == nil &&
		u.Public == nil &&
		u.Lat == nil &&
		u.Lon == nil &&
		u.Distance == nil &&
		u.ElevationGain == nil &&
		u.ElevationLoss == nil &&
		u.Duration == nil &&
		u.Thumbnail == nil &&
		len(u.Tags) == 0 &&
		len(u.PhotoURLs) == 0
}

func (u TrailUpdate) APISendableEmpty() bool {
	return u.Name == nil &&
		u.Description == nil &&
		u.Location == nil &&
		u.Date == nil &&
		u.Difficulty == nil &&
		u.Category == nil &&
		u.Public == nil &&
		u.Lat == nil &&
		u.Lon == nil &&
		u.Distance == nil &&
		u.ElevationGain == nil &&
		u.ElevationLoss == nil &&
		u.Duration == nil &&
		u.Thumbnail == nil
}

func MergeTrailUpdates(base, override TrailUpdate) TrailUpdate {
	merged := base
	if override.Name != nil {
		merged.Name = override.Name
	}
	if override.Description != nil {
		merged.Description = override.Description
	}
	if override.Location != nil {
		merged.Location = override.Location
	}
	if override.Date != nil {
		merged.Date = override.Date
	}
	if override.Difficulty != nil {
		merged.Difficulty = override.Difficulty
	}
	if override.Category != nil {
		merged.Category = override.Category
	}
	if override.Public != nil {
		merged.Public = override.Public
	}
	if override.Lat != nil {
		merged.Lat = override.Lat
	}
	if override.Lon != nil {
		merged.Lon = override.Lon
	}
	if override.Distance != nil {
		merged.Distance = override.Distance
	}
	if override.ElevationGain != nil {
		merged.ElevationGain = override.ElevationGain
	}
	if override.ElevationLoss != nil {
		merged.ElevationLoss = override.ElevationLoss
	}
	if override.Duration != nil {
		merged.Duration = override.Duration
	}
	if override.Thumbnail != nil {
		merged.Thumbnail = override.Thumbnail
	}
	merged.Tags = mergeTags(base.Tags, override.Tags)
	merged.PhotoURLs = mergeTags(base.PhotoURLs, override.PhotoURLs)
	return merged
}

func mergeTags(base, override []string) []string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var merged []string
	for _, tag := range append(append([]string{}, base...), override...) {
		tag = strings.TrimSpace(tag)
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
	return merged
}

func descriptionHasSource(description, source string) bool {
	return strings.Contains(description, "wanderer-import-source: "+source) ||
		strings.Contains(description, "Source: "+source)
}

func trailLooksDuplicate(trail Trail, update TrailUpdate) bool {
	score := 0
	if update.Distance != nil && trail.Distance > 0 {
		delta := absFloat(trail.Distance - *update.Distance)
		tolerance := maxFloat(25, *update.Distance*0.005)
		if delta <= tolerance {
			score += 3
		} else {
			return false
		}
	}
	if update.Lat != nil && update.Lon != nil && trail.Lat != 0 && trail.Lon != 0 {
		if haversineMeters(trail.Lat, trail.Lon, *update.Lat, *update.Lon) <= 75 {
			score += 3
		} else {
			return false
		}
	}
	if update.ElevationGain != nil && trail.ElevationGain > 0 {
		if absFloat(trail.ElevationGain-*update.ElevationGain) <= maxFloat(25, *update.ElevationGain*0.1) {
			score++
		}
	}
	if update.Duration != nil && trail.Duration > 0 {
		if absFloat(trail.Duration-*update.Duration) <= maxFloat(120, *update.Duration*0.1) {
			score++
		}
	}
	if update.Name != nil && normalizedName(trail.Name) == normalizedName(*update.Name) {
		score += 2
	}
	if update.Location != nil && normalizedName(trail.Location) == normalizedName(*update.Location) {
		score++
	}
	return score >= 5
}

func (c *Client) UpdateTrail(ctx context.Context, id string, update TrailUpdate) (*Trail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("trail ID is required")
	}
	if update.APISendableEmpty() {
		return nil, errors.New("trail update is empty")
	}
	prepared, err := c.prepareTrailUpdate(ctx, update)
	if err != nil {
		return nil, err
	}

	trail, err := c.updateTrailForm(ctx, id, prepared)
	if err == nil {
		return trail, nil
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUnsupportedMediaType, http.StatusInternalServerError:
			return c.updateTrailJSON(ctx, id, prepared)
		}
	}
	return nil, err
}

func (c *Client) prepareTrailUpdate(ctx context.Context, update TrailUpdate) (TrailUpdate, error) {
	if update.Category == nil || looksLikeRecordID(*update.Category) {
		return update, nil
	}
	id, found, err := c.findCategoryID(ctx, *update.Category)
	if err != nil {
		return update, err
	}
	if found {
		update.Category = &id
	}
	return update, nil
}

func looksLikeRecordID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 15 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (c *Client) findCategoryID(ctx context.Context, name string) (string, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, nil
	}
	normalizedName := strings.ToLower(name)
	const perPage = 100
	for page := 1; ; page++ {
		values := url.Values{}
		values.Set("page", strconv.Itoa(page))
		values.Set("perPage", strconv.Itoa(perPage))
		values.Set("requestKey", "wanderer-import-category-lookup")
		endpoint := c.endpoint("/category")
		endpoint.RawQuery = values.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return "", false, err
		}
		var list CategoryList
		if err := c.doJSON(req, &list, http.StatusOK); err != nil {
			return "", false, err
		}
		for _, category := range list.Items {
			if categoryNameMatches(category.Name, normalizedName) {
				return category.ID, true, nil
			}
		}
		if list.TotalPages <= 0 || page >= list.TotalPages || len(list.Items) == 0 {
			return "", false, nil
		}
	}
}

func categoryNameMatches(candidate, wanted string) bool {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	if candidate == wanted {
		return true
	}
	aliases := map[string][]string{
		"biking":  {"bike", "cycling", "cyclisme", "velo", "vtt"},
		"hiking":  {"hike", "randonnee", "randonnée", "pedestre", "pédestre"},
		"walking": {"walk", "marche", "promenade"},
	}
	for canonical, values := range aliases {
		if wanted != canonical {
			continue
		}
		for _, value := range values {
			if candidate == value {
				return true
			}
		}
	}
	return false
}

func normalizedName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	space := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			space = false
		case r >= 'à' && r <= 'ÿ':
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMeters = 6371000
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusMeters * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func (c *Client) FindTrailBySource(ctx context.Context, source string) (*Trail, bool, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, false, nil
	}
	const perPage = 100
	for page := 1; ; page++ {
		values := url.Values{}
		values.Set("page", strconv.Itoa(page))
		values.Set("perPage", strconv.Itoa(perPage))
		values.Set("sort", "-updated")
		values.Set("requestKey", "wanderer-import-source-dedupe")
		endpoint := c.endpoint("/trail")
		endpoint.RawQuery = values.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, false, err
		}
		var list TrailList
		if err := c.doJSON(req, &list, http.StatusOK); err != nil {
			return nil, false, err
		}
		for i := range list.Items {
			if descriptionHasSource(list.Items[i].Description, source) {
				return &list.Items[i], true, nil
			}
		}
		if list.TotalPages <= 0 || page >= list.TotalPages || len(list.Items) == 0 {
			return nil, false, nil
		}
	}
}

func (c *Client) FindDuplicateTrail(ctx context.Context, update TrailUpdate) (*Trail, bool, error) {
	if update.Distance == nil && update.Lat == nil && update.Lon == nil && update.Name == nil {
		return nil, false, nil
	}
	const perPage = 100
	for page := 1; ; page++ {
		values := url.Values{}
		values.Set("page", strconv.Itoa(page))
		values.Set("perPage", strconv.Itoa(perPage))
		values.Set("sort", "-updated")
		values.Set("requestKey", "wanderer-import-duplicate-lookup")
		endpoint := c.endpoint("/trail")
		endpoint.RawQuery = values.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, false, err
		}
		var list TrailList
		if err := c.doJSON(req, &list, http.StatusOK); err != nil {
			return nil, false, err
		}
		for i := range list.Items {
			if trailLooksDuplicate(list.Items[i], update) {
				return &list.Items[i], true, nil
			}
		}
		if list.TotalPages <= 0 || page >= list.TotalPages || len(list.Items) == 0 {
			return nil, false, nil
		}
	}
}

type Trail struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Location      string   `json:"location"`
	Author        string   `json:"author"`
	Date          string   `json:"date"`
	Difficulty    string   `json:"difficulty"`
	Category      string   `json:"category"`
	Public        bool     `json:"public"`
	Lat           float64  `json:"lat"`
	Lon           float64  `json:"lon"`
	Distance      float64  `json:"distance"`
	ElevationGain float64  `json:"elevation_gain"`
	ElevationLoss float64  `json:"elevation_loss"`
	Duration      float64  `json:"duration"`
	Photos        []string `json:"photos"`
	Thumbnail     int      `json:"thumbnail"`
	LikeCount     int      `json:"like_count"`
	Tags          []string `json:"tags"`
	GPX           string   `json:"gpx"`
	Created       string   `json:"created"`
	Updated       string   `json:"updated"`
}

type TrailList struct {
	Page       int     `json:"page"`
	PerPage    int     `json:"perPage"`
	TotalItems int     `json:"totalItems"`
	TotalPages int     `json:"totalPages"`
	Items      []Trail `json:"items"`
}

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CategoryList struct {
	Page       int        `json:"page"`
	PerPage    int        `json:"perPage"`
	TotalItems int        `json:"totalItems"`
	TotalPages int        `json:"totalPages"`
	Items      []Category `json:"items"`
}

type trailUpdatePayload struct {
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	Location      *string  `json:"location,omitempty"`
	Date          *string  `json:"date,omitempty"`
	Difficulty    *string  `json:"difficulty,omitempty"`
	Category      *string  `json:"category,omitempty"`
	Public        *bool    `json:"public,omitempty"`
	Lat           *float64 `json:"lat,omitempty"`
	Lon           *float64 `json:"lon,omitempty"`
	Distance      *float64 `json:"distance,omitempty"`
	ElevationGain *float64 `json:"elevation_gain,omitempty"`
	ElevationLoss *float64 `json:"elevation_loss,omitempty"`
	Duration      *float64 `json:"duration,omitempty"`
	Thumbnail     *int     `json:"thumbnail,omitempty"`
}

func trailUpdatePayloadFrom(update TrailUpdate) trailUpdatePayload {
	return trailUpdatePayload{
		Name:          update.Name,
		Description:   update.Description,
		Location:      update.Location,
		Date:          update.Date,
		Difficulty:    update.Difficulty,
		Category:      update.Category,
		Public:        update.Public,
		Lat:           update.Lat,
		Lon:           update.Lon,
		Distance:      update.Distance,
		ElevationGain: update.ElevationGain,
		ElevationLoss: update.ElevationLoss,
		Duration:      update.Duration,
		Thumbnail:     update.Thumbnail,
	}
}

func (c *Client) updateTrailForm(ctx context.Context, id string, update TrailUpdate) (*Trail, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writeTrailUpdateFields(writer, update); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/trail/form/"+url.PathEscape(id)).String(), &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	var trail Trail
	if err := c.doJSON(req, &trail, http.StatusOK); err != nil {
		return nil, err
	}
	return &trail, nil
}

func (c *Client) updateTrailJSON(ctx context.Context, id string, update TrailUpdate) (*Trail, error) {
	var trail Trail
	if err := c.postJSON(ctx, "/trail/"+url.PathEscape(id), trailUpdatePayloadFrom(update), &trail); err != nil {
		return nil, err
	}
	return &trail, nil
}

func (c *Client) downloadRemotePhotos(ctx context.Context, urls []string) ([]RemotePhoto, error) {
	const maxPhotoBytes = 12 << 20

	var photos []RemotePhoto
	seen := map[string]struct{}{}
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		res, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, maxPhotoBytes+1))
		_ = res.Body.Close()
		if readErr != nil || res.StatusCode < 200 || res.StatusCode > 299 || len(data) == 0 || len(data) > maxPhotoBytes {
			continue
		}
		contentType := strings.ToLower(res.Header.Get("Content-Type"))
		if !looksLikeImage(raw, contentType, data) {
			continue
		}
		photos = append(photos, RemotePhoto{
			URL:      raw,
			Filename: photoFilename(raw, contentType),
			Data:     data,
		})
	}
	return photos, nil
}

func (c *Client) uploadTrailPhotos(ctx context.Context, id string, photos []RemotePhoto) (*Trail, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i, photo := range photos {
		filename := strings.TrimSpace(photo.Filename)
		if filename == "" {
			filename = fmt.Sprintf("photo-%d.jpg", i+1)
		}
		part, err := writer.CreateFormFile("photos", filename)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		if _, err := part.Write(photo.Data); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/trail/"+url.PathEscape(id)+"/file").String(), &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	var trail Trail
	if err := c.doJSON(req, &trail, http.StatusOK); err != nil {
		return nil, err
	}
	return &trail, nil
}

func looksLikeImage(source, contentType string, data []byte) bool {
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	ext := strings.ToLower(path.Ext(source))
	if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".svg" {
		return true
	}
	return bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) ||
		bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) ||
		bytes.HasPrefix(data, []byte("RIFF")) ||
		bytes.Contains(bytes.ToLower(data[:min(len(data), 256)]), []byte("<svg"))
}

func photoFilename(source, contentType string) string {
	if parsed, err := url.Parse(source); err == nil {
		if filename := strings.TrimSpace(path.Base(parsed.Path)); filename != "" && filename != "." && filename != "/" {
			return filename
		}
	}
	switch {
	case strings.Contains(contentType, "png"):
		return "photo.png"
	case strings.Contains(contentType, "webp"):
		return "photo.webp"
	case strings.Contains(contentType, "svg"):
		return "photo.svg"
	default:
		return "photo.jpg"
	}
}

func writeTrailUpdateFields(writer *multipart.Writer, update TrailUpdate) error {
	writeString := func(name string, value *string) error {
		if value == nil {
			return nil
		}
		return writer.WriteField(name, *value)
	}
	writeBool := func(name string, value *bool) error {
		if value == nil {
			return nil
		}
		return writer.WriteField(name, strconv.FormatBool(*value))
	}
	writeFloat := func(name string, value *float64) error {
		if value == nil {
			return nil
		}
		return writer.WriteField(name, strconv.FormatFloat(*value, 'f', -1, 64))
	}
	writeInt := func(name string, value *int) error {
		if value == nil {
			return nil
		}
		return writer.WriteField(name, strconv.Itoa(*value))
	}

	for _, write := range []func() error{
		func() error { return writeString("name", update.Name) },
		func() error { return writeString("description", update.Description) },
		func() error { return writeString("location", update.Location) },
		func() error { return writeString("date", update.Date) },
		func() error { return writeString("difficulty", update.Difficulty) },
		func() error { return writeString("category", update.Category) },
		func() error { return writeBool("public", update.Public) },
		func() error { return writeFloat("lat", update.Lat) },
		func() error { return writeFloat("lon", update.Lon) },
		func() error { return writeFloat("distance", update.Distance) },
		func() error { return writeFloat("elevation_gain", update.ElevationGain) },
		func() error { return writeFloat("elevation_loss", update.ElevationLoss) },
		func() error { return writeFloat("duration", update.Duration) },
		func() error { return writeInt("thumbnail", update.Thumbnail) },
	} {
		if err := write(); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) postJSON(ctx context.Context, path string, body any, target any) error {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(body); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(path).String(), &encoded)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, target, http.StatusOK, http.StatusCreated)
}

func (c *Client) endpoint(path string) *url.URL {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return &endpoint
}

func (c *Client) doJSON(req *http.Request, target any, okStatuses ...int) error {
	c.prepare(req)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}

	if !statusOK(res.StatusCode, okStatuses) {
		return &APIError{
			StatusCode: res.StatusCode,
			Method:     req.Method,
			URL:        req.URL.String(),
			Message:    responseMessage(body),
			Body:       string(body),
		}
	}

	if target == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Wanderer response from %s: %w", req.URL, err)
	}
	return nil
}

func (c *Client) prepare(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("pb_auth", c.token)
	}
	if c.pbAuthCookie != "" {
		req.AddCookie(&http.Cookie{Name: "pb_auth", Value: c.pbAuthCookie})
	}
}

func statusOK(status int, expected []int) bool {
	for _, ok := range expected {
		if status == ok {
			return true
		}
	}
	return false
}

func responseMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}

	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		return string(body)
	}
	for _, key := range []string{"message", "error", "detail"} {
		if value, ok := object[key].(string); ok {
			return value
		}
	}
	if response, ok := object["response"].(map[string]any); ok {
		if value, ok := response["message"].(string); ok {
			return value
		}
	}
	return string(body)
}

func IsDuplicateTrailError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != http.StatusBadRequest && apiErr.StatusCode != http.StatusConflict {
		return false
	}
	text := strings.ToLower(apiErr.Message + " " + apiErr.Body)
	return strings.Contains(text, "duplicate") && strings.Contains(text, "trail")
}

type APIError struct {
	StatusCode int
	Method     string
	URL        string
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s %s failed with %d: %s", e.Method, e.URL, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s %s failed with %d", e.Method, e.URL, e.StatusCode)
}
