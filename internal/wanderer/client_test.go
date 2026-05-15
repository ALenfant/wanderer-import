package wanderer

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := map[string]string{
		"":                                 "http://localhost:3000/api/v1",
		"localhost:8080":                   "http://localhost:8080/api/v1",
		"https://example.test":             "https://example.test/api/v1",
		"https://example.test/base":        "https://example.test/base/api/v1",
		"https://example.test/base/api/v1": "https://example.test/base/api/v1",
	}

	for input, want := range tests {
		got, err := NormalizeBaseURL(input)
		if err != nil {
			t.Fatalf("NormalizeBaseURL(%q): %v", input, err)
		}
		if got.String() != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", input, got.String(), want)
		}
	}
}

func TestUploadTrailSendsMultipartAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/v1/trail/upload" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("pb_auth"); got != "test-token" {
			t.Fatalf("pb_auth = %q", got)
		}

		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			fields[part.FormName()] = string(content)
			if part.FormName() == "file" && part.FileName() != "walk.gpx" {
				t.Fatalf("file name = %q", part.FileName())
			}
		}
		if fields["file"] != "<gpx />" {
			t.Fatalf("file = %q", fields["file"])
		}
		if fields["name"] != "walk.gpx" {
			t.Fatalf("name = %q", fields["name"])
		}
		if fields["ignoreDuplicates"] != "true" {
			t.Fatalf("ignoreDuplicates = %q", fields["ignoreDuplicates"])
		}

		_ = json.NewEncoder(w).Encode(Trail{ID: "abc123", Name: "Walk"})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithToken("test-token"))
	if err != nil {
		t.Fatal(err)
	}

	trail, err := client.UploadTrail(context.Background(), "walk.gpx", strings.NewReader("<gpx />"), UploadOptions{
		Name:             "walk.gpx",
		IgnoreDuplicates: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if trail.ID != "abc123" {
		t.Fatalf("trail ID = %q", trail.ID)
	}
}

func TestUpdateTrailFallsBackToJSON(t *testing.T) {
	for _, status := range []int{
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusUnsupportedMediaType,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			testUpdateTrailFallsBackToJSON(t, status)
		})
	}
}

func TestUpdateTrailResolvesCategoryName(t *testing.T) {
	categoryID := "bikecategory123"
	var sawCategoryLookup bool
	var sawUpdate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/category":
			sawCategoryLookup = true
			_ = json.NewEncoder(w).Encode(CategoryList{
				Page:       1,
				PerPage:    100,
				TotalItems: 1,
				TotalPages: 1,
				Items:      []Category{{ID: categoryID, Name: "Bike"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/trail/form/abc123":
			sawUpdate = true
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			fields := map[string]string{}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				fields[part.FormName()] = string(content)
			}
			if fields["category"] != categoryID {
				t.Fatalf("category = %q, want %q", fields["category"], categoryID)
			}
			_ = json.NewEncoder(w).Encode(Trail{ID: "abc123", Category: categoryID})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	category := "Biking"
	if _, err := client.UpdateTrail(context.Background(), "abc123", TrailUpdate{Category: &category}); err != nil {
		t.Fatal(err)
	}
	if !sawCategoryLookup {
		t.Fatal("category lookup was not used")
	}
	if !sawUpdate {
		t.Fatal("trail update was not used")
	}
}

func TestUploadTrailPhotoURLs(t *testing.T) {
	const imageData = "\xff\xd8\xfftest"
	var sawUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/photo.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte(imageData))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/trail/abc123/file":
			sawUpload = true
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			if part.FormName() != "photos" {
				t.Fatalf("form name = %q", part.FormName())
			}
			if part.FileName() != "photo.jpg" {
				t.Fatalf("filename = %q", part.FileName())
			}
			content, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != imageData {
				t.Fatalf("photo content = %q", content)
			}
			_ = json.NewEncoder(w).Encode(Trail{ID: "abc123", Photos: []string{"photo.jpg"}})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	trail, err := client.UploadTrailPhotoURLs(context.Background(), "abc123", []string{server.URL + "/photo.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if !sawUpload {
		t.Fatal("photo upload was not used")
	}
	if len(trail.Photos) != 1 {
		t.Fatalf("photos = %#v", trail.Photos)
	}
}

func TestFindTrailBySourceMatchesImportMarker(t *testing.T) {
	source := "https://example.test/trails/ridge"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/trail" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("perPage"); got != "100" {
			t.Fatalf("perPage = %q", got)
		}
		_ = json.NewEncoder(w).Encode(TrailList{
			Page:       1,
			PerPage:    100,
			TotalItems: 1,
			TotalPages: 1,
			Items: []Trail{
				{
					ID:          "abc123",
					Name:        "Ridge",
					Description: "Imported by wanderer-import\nwanderer-import-source: " + source,
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	trail, found, err := client.FindTrailBySource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("trail was not found")
	}
	if trail.ID != "abc123" {
		t.Fatalf("trail ID = %q", trail.ID)
	}
}

func TestFindTrailBySourceMatchesLegacySourceLine(t *testing.T) {
	source := "https://example.test/trails/ridge"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(TrailList{
			Page:       1,
			PerPage:    100,
			TotalItems: 1,
			TotalPages: 1,
			Items: []Trail{
				{ID: "legacy123", Description: "Source: " + source},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	trail, found, err := client.FindTrailBySource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("trail was not found")
	}
	if trail.ID != "legacy123" {
		t.Fatalf("trail ID = %q", trail.ID)
	}
}

func TestFindDuplicateTrailMatchesRouteMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/trail" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(TrailList{
			Page:       1,
			PerPage:    100,
			TotalItems: 1,
			TotalPages: 1,
			Items: []Trail{{
				ID:            "dup123",
				Name:          "Le chemin du Ranc de Banes",
				Location:      "Sumene",
				Lat:           43.980248,
				Lon:           3.716928,
				Distance:      14870,
				ElevationGain: 985,
				Duration:      23400,
			}},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	name := "Le chemin du Ranc de Banes"
	location := "Sumene"
	lat := 43.98025
	lon := 3.71693
	distance := 14875.0
	gain := 980.0
	duration := 23420.0
	trail, found, err := client.FindDuplicateTrail(context.Background(), TrailUpdate{
		Name:          &name,
		Location:      &location,
		Lat:           &lat,
		Lon:           &lon,
		Distance:      &distance,
		ElevationGain: &gain,
		Duration:      &duration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected duplicate")
	}
	if trail.ID != "dup123" {
		t.Fatalf("id = %q", trail.ID)
	}
}

func TestFindDuplicateTrailRejectsDifferentStart(t *testing.T) {
	distance := 14875.0
	lat := 44.5
	lon := 3.7
	update := TrailUpdate{Distance: &distance, Lat: &lat, Lon: &lon}

	if trailLooksDuplicate(Trail{Distance: 14870, Lat: 43.980248, Lon: 3.716928}, update) {
		t.Fatal("expected different start point to be rejected")
	}
}

func TestIsDuplicateTrailError(t *testing.T) {
	err := &APIError{StatusCode: http.StatusBadRequest, Message: "Duplicate trail"}
	if !IsDuplicateTrailError(err) {
		t.Fatal("expected duplicate trail error")
	}
	if IsDuplicateTrailError(&APIError{StatusCode: http.StatusBadRequest, Message: "bad request"}) {
		t.Fatal("did not expect generic bad request to match")
	}
}

func testUpdateTrailFallsBackToJSON(t *testing.T, formStatus int) {
	var sawJSON bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/trail/form/abc123":
			http.Error(w, http.StatusText(formStatus), formStatus)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/trail/abc123":
			sawJSON = true
			if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Fatalf("Content-Type = %q", got)
			}
			var update TrailUpdate
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Fatal(err)
			}
			if update.Name == nil || *update.Name != "New name" {
				t.Fatalf("update name = %#v", update.Name)
			}
			_ = json.NewEncoder(w).Encode(Trail{ID: "abc123", Name: "New name"})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	name := "New name"
	trail, err := client.UpdateTrail(context.Background(), "abc123", TrailUpdate{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if !sawJSON {
		t.Fatal("JSON fallback was not used")
	}
	if trail.Name != "New name" {
		t.Fatalf("trail name = %q", trail.Name)
	}
}

func TestWriteTrailUpdateFields(t *testing.T) {
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	name := "Trail"
	public := true
	update := TrailUpdate{Name: &name, Public: &public, Tags: []string{"hike", "ridge"}}

	if err := writeTrailUpdateFields(writer, update); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	content := body.String()
	for _, want := range []string{`name="name"`, "Trail", `name="public"`, "true"} {
		if !strings.Contains(content, want) {
			t.Fatalf("multipart body does not contain %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"hike", "ridge"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("multipart body contains unsupported tag value %q:\n%s", unwanted, content)
		}
	}
}
