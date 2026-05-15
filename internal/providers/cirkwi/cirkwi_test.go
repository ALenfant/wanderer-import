package cirkwi

import (
	"io"
	"net/url"
	"strings"
	"testing"

	"wanderer-import/internal/wanderer"
)

func TestResolvedFromPageObjectJSON(t *testing.T) {
	page := []byte(`<html><script>
var objetJSON = {"id_objet":"1070291_0","adresse":{"ville":"Sumene","lat":"43.98","lng":"3.71"},"traduction":{"fr_FR":{"information":{"titre":"Randonnée Le Ranc de Banes","description":"<p>Main description</p>"},"tags":[{"tag":{"tag":"gardtourisme_apied"}},{"tag":{"tag":"apidae_niveau_noir_tres_difficile"}}],"informations_complementaires":[{"titre":"Topo/pas à pas","description":"<p>Step one</p>","htmlDescription":"<p>Step one rich</p>"}],"url_image":"https:\/\/fichier0.cirkwi.com\/image\/photo\/circuit\/420x420\/1070291\/fr\/0.jpg"}},"trace":{"altimetries":[{"altitude":200.48,"position":{"lat":43.9802478,"lng":3.7169279}},{"altitude":201.48,"position":{"lat":43.9802613,"lng":3.7169382}}],"distance":14.87},"locomotions":[{"duree":{"total_secondes":23400},"locomotion":{"id_categorie_locomotion":1,"nom_locomotion":""}}]};
var objets_lies = [];
</script></html>`)

	resolved, err := resolvedFromPage("https://www.cirkwi.com/fr/circuit/1070291-randonnee-le-ranc-de-banes", page, wanderer.TrailUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	data, err := io.ReadAll(resolved.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<trkpt lat=\"43.9802478\" lon=\"3.7169279\">") {
		t.Fatalf("GPX did not contain expected point:\n%s", string(data))
	}
	if resolved.Metadata.Name == nil || *resolved.Metadata.Name != "Randonnée Le Ranc de Banes" {
		t.Fatalf("name = %#v", resolved.Metadata.Name)
	}
	if resolved.Metadata.Description == nil || !strings.Contains(*resolved.Metadata.Description, "Step one rich") {
		t.Fatalf("description = %#v", resolved.Metadata.Description)
	}
	if resolved.Metadata.Distance == nil || *resolved.Metadata.Distance != 14870 {
		t.Fatalf("distance = %#v", resolved.Metadata.Distance)
	}
	if resolved.Metadata.Duration == nil || *resolved.Metadata.Duration != 23400 {
		t.Fatalf("duration = %#v", resolved.Metadata.Duration)
	}
	if resolved.Metadata.Difficulty == nil || *resolved.Metadata.Difficulty != wanderer.DifficultyDifficult {
		t.Fatalf("difficulty = %#v", resolved.Metadata.Difficulty)
	}
	if resolved.Metadata.Category == nil || *resolved.Metadata.Category != "Hiking" {
		t.Fatalf("category = %#v", resolved.Metadata.Category)
	}
	if len(resolved.Metadata.PhotoURLs) != 1 {
		t.Fatalf("photo urls = %#v", resolved.Metadata.PhotoURLs)
	}
}

func TestExtractID(t *testing.T) {
	parsed, ok := parseURL("https://www.cirkwi.com/fr/circuit/1070291-randonnee-le-ranc-de-banes")
	if !ok {
		t.Fatal("parse failed")
	}
	id, ok := extractID(parsed)
	if !ok || id != "1070291" {
		t.Fatalf("id = %q, %v", id, ok)
	}
}

func parseURL(value string) (*url.URL, bool) {
	parsed, err := url.Parse(value)
	return parsed, err == nil
}
