package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/wanderer"
)

type File struct {
	Imports []Entry `json:"imports"`
}

type Entry struct {
	Source           string   `json:"source"`
	Provider         string   `json:"provider,omitempty"`
	IgnoreDuplicates *bool    `json:"ignoreDuplicates,omitempty"`
	UpdateExisting   *bool    `json:"updateExisting,omitempty"`
	Name             string   `json:"name,omitempty"`
	Description      string   `json:"description,omitempty"`
	Location         string   `json:"location,omitempty"`
	Date             string   `json:"date,omitempty"`
	Difficulty       string   `json:"difficulty,omitempty"`
	Category         string   `json:"category,omitempty"`
	Public           *bool    `json:"public,omitempty"`
	Lat              *float64 `json:"lat,omitempty"`
	Lon              *float64 `json:"lon,omitempty"`
	Distance         *float64 `json:"distance,omitempty"`
	ElevationGain    *float64 `json:"elevation_gain,omitempty"`
	ElevationLoss    *float64 `json:"elevation_loss,omitempty"`
	Duration         *float64 `json:"duration,omitempty"`
	Thumbnail        *int     `json:"thumbnail,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	PhotoURLs        []string `json:"photo_urls,omitempty"`
}

func Load(reader io.Reader) ([]importer.Spec, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("manifest is empty")
	}

	var entries []Entry
	if data[0] == '[' {
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, err
		}
	} else {
		var file File
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, err
		}
		entries = file.Imports
		if len(entries) == 0 {
			var single Entry
			if err := json.Unmarshal(data, &single); err != nil {
				return nil, err
			}
			if single.Source != "" {
				entries = []Entry{single}
			}
		}
	}

	specs := make([]importer.Spec, 0, len(entries))
	for i, entry := range entries {
		spec, err := entry.Spec()
		if err != nil {
			return nil, fmt.Errorf("manifest entry %d: %w", i+1, err)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func (e Entry) Spec() (importer.Spec, error) {
	if strings.TrimSpace(e.Source) == "" {
		return importer.Spec{}, fmt.Errorf("source is required")
	}

	ignoreDuplicates := false
	if e.IgnoreDuplicates != nil {
		ignoreDuplicates = *e.IgnoreDuplicates
	}
	updateExisting := false
	if e.UpdateExisting != nil {
		updateExisting = *e.UpdateExisting
	}
	difficulty, err := optionalDifficulty(e.Difficulty)
	if err != nil {
		return importer.Spec{}, err
	}

	return importer.Spec{
		Source:           e.Source,
		Provider:         e.Provider,
		IgnoreDuplicates: ignoreDuplicates,
		UpdateExisting:   updateExisting,
		Update: wanderer.TrailUpdate{
			Name:          optionalString(e.Name),
			Description:   optionalString(e.Description),
			Location:      optionalString(e.Location),
			Date:          optionalString(e.Date),
			Difficulty:    difficulty,
			Category:      optionalString(e.Category),
			Public:        e.Public,
			Lat:           e.Lat,
			Lon:           e.Lon,
			Distance:      e.Distance,
			ElevationGain: e.ElevationGain,
			ElevationLoss: e.ElevationLoss,
			Duration:      e.Duration,
			Thumbnail:     e.Thumbnail,
			Tags:          nonEmptyStrings(e.Tags),
			PhotoURLs:     nonEmptyStrings(e.PhotoURLs),
		},
	}, nil
}

func optionalDifficulty(value string) (*string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	normalized, ok := wanderer.NormalizeDifficulty(value)
	if !ok {
		return nil, fmt.Errorf("invalid difficulty %q; expected %s, %s, or %s", value, wanderer.DifficultyEasy, wanderer.DifficultyModerate, wanderer.DifficultyDifficult)
	}
	return &normalized, nil
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
