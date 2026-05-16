package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"wanderer-import/internal/wanderer"
)

type Spec struct {
	Source           string               `json:"source"`
	Provider         string               `json:"provider,omitempty"`
	IgnoreDuplicates bool                 `json:"ignoreDuplicates"`
	UpdateExisting   bool                 `json:"updateExisting,omitempty"`
	Update           wanderer.TrailUpdate `json:"update,omitempty"`
}

type ResolvedTrail struct {
	Source   string
	Filename string
	Body     io.ReadCloser
	Metadata wanderer.TrailUpdate
}

func (r *ResolvedTrail) Close() error {
	if r == nil || r.Body == nil {
		return nil
	}
	return r.Body.Close()
}

type Provider interface {
	Name() string
	Match(source string) Match
	Resolve(ctx context.Context, spec Spec) (*ResolvedTrail, error)
}

type Match struct {
	OK     bool
	Score  int
	Reason string
}

type Descriptor struct {
	ID      string
	Name    string
	Engine  string
	Domains []string
	Status  string
}

type DescribedProvider interface {
	Descriptor() Descriptor
}

type Registry struct {
	providers []Provider
}

func NewRegistry(providers ...Provider) *Registry {
	return &Registry{providers: providers}
}

func (r *Registry) Select(name, source string) (Provider, error) {
	name = strings.TrimSpace(name)
	if name != "" && name != "auto" {
		for _, provider := range r.providers {
			if provider.Name() == name || descriptorID(provider) == name {
				return provider, nil
			}
		}
		return nil, fmt.Errorf("unknown provider %q", name)
	}

	var selected Provider
	var selectedMatch Match
	for _, provider := range r.providers {
		match := provider.Match(source)
		if !match.OK {
			continue
		}
		if match.Score <= 0 {
			match.Score = 1
		}
		if selected == nil || match.Score > selectedMatch.Score {
			selected = provider
			selectedMatch = match
		}
	}
	if selected != nil {
		return selected, nil
	}
	return nil, fmt.Errorf("no provider matched %q", source)
}

func descriptorID(provider Provider) string {
	described, ok := provider.(DescribedProvider)
	if !ok {
		return ""
	}
	return described.Descriptor().ID
}

type WandererClient interface {
	UploadTrail(ctx context.Context, filename string, body io.Reader, opts wanderer.UploadOptions) (*wanderer.Trail, error)
	UpdateTrail(ctx context.Context, id string, update wanderer.TrailUpdate) (*wanderer.Trail, error)
	UploadTrailPhotoURLs(ctx context.Context, id string, urls []string) (*wanderer.Trail, error)
}

type SourceFinder interface {
	FindTrailBySource(ctx context.Context, source string) (*wanderer.Trail, bool, error)
}

type DuplicateFinder interface {
	FindDuplicateTrail(ctx context.Context, update wanderer.TrailUpdate) (*wanderer.Trail, bool, error)
}

type Result struct {
	Source   string          `json:"source"`
	Provider string          `json:"provider"`
	ID       string          `json:"id"`
	Name     string          `json:"name,omitempty"`
	Updated  bool            `json:"updated"`
	Warnings []string        `json:"warnings,omitempty"`
	Trail    *wanderer.Trail `json:"trail,omitempty"`
}

func Import(ctx context.Context, client WandererClient, registry *Registry, spec Spec) (*Result, error) {
	if strings.TrimSpace(spec.Source) == "" {
		return nil, errors.New("source is required")
	}

	provider, err := registry.Select(spec.Provider, spec.Source)
	if err != nil {
		return nil, err
	}

	resolved, err := provider.Resolve(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer resolved.Close()

	update := wanderer.MergeTrailUpdates(resolved.Metadata, spec.Update)
	appendImportMarkerToDescription(&update, spec.Source, provider.Name())
	uploadName := resolved.Filename
	if update.Name != nil && strings.TrimSpace(*update.Name) != "" {
		uploadName = strings.TrimSpace(*update.Name)
	}

	if spec.UpdateExisting {
		if finder, ok := client.(SourceFinder); ok {
			existing, found, err := finder.FindTrailBySource(ctx, spec.Source)
			if err != nil {
				return nil, err
			}
			if found {
				return updateExistingTrail(ctx, client, existing, spec, provider.Name(), update)
			}
		}
	}

	trail, err := client.UploadTrail(ctx, resolved.Filename, resolved.Body, wanderer.UploadOptions{
		Name:             uploadName,
		IgnoreDuplicates: spec.IgnoreDuplicates,
	})
	if err != nil {
		if wanderer.IsDuplicateTrailError(err) {
			if finder, ok := client.(DuplicateFinder); ok {
				existing, found, findErr := finder.FindDuplicateTrail(ctx, update)
				if findErr != nil {
					return nil, fmt.Errorf("%w; duplicate lookup failed: %v", err, findErr)
				}
				if found {
					return updateExistingTrail(ctx, client, existing, spec, provider.Name(), update)
				}
			}
		}
		return nil, err
	}

	updated := false
	var warnings []string
	if !update.APISendableEmpty() {
		if update.Name == nil && trail.Name != "" {
			update.Name = &trail.Name
		}
		trail, err = client.UpdateTrail(ctx, trail.ID, update)
		if err != nil {
			return nil, err
		}
		updated = true
	}
	if len(update.PhotoURLs) > 0 {
		updatedTrail, err := client.UploadTrailPhotoURLs(ctx, trail.ID, update.PhotoURLs)
		if err != nil {
			warnings = append(warnings, "photo upload failed: "+err.Error())
		} else {
			trail = updatedTrail
			updated = true
		}
	}

	return &Result{
		Source:   spec.Source,
		Provider: provider.Name(),
		ID:       trail.ID,
		Name:     trail.Name,
		Updated:  updated,
		Warnings: warnings,
		Trail:    trail,
	}, nil
}

func updateExistingTrail(ctx context.Context, client WandererClient, existing *wanderer.Trail, spec Spec, provider string, update wanderer.TrailUpdate) (*Result, error) {
	if existing == nil || strings.TrimSpace(existing.ID) == "" {
		return nil, errors.New("existing trail has no ID")
	}
	preserveExistingDescriptionForMarkerOnlyUpdate(existing, &update, spec.Source, provider)
	trail := existing
	updated := false
	var warnings []string
	if !update.APISendableEmpty() {
		if update.Name == nil && existing.Name != "" {
			update.Name = &existing.Name
		}
		var err error
		trail, err = client.UpdateTrail(ctx, existing.ID, update)
		if err != nil {
			return nil, err
		}
		updated = true
	}
	if len(update.PhotoURLs) > 0 {
		updatedTrail, err := client.UploadTrailPhotoURLs(ctx, existing.ID, update.PhotoURLs)
		if err != nil {
			warnings = append(warnings, "photo upload failed: "+err.Error())
		} else {
			trail = updatedTrail
			updated = true
		}
	}
	return &Result{
		Source:   spec.Source,
		Provider: provider,
		ID:       trail.ID,
		Name:     trail.Name,
		Updated:  updated,
		Warnings: warnings,
		Trail:    trail,
	}, nil
}

func appendImportMarkerToDescription(update *wanderer.TrailUpdate, source, provider string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	provider = strings.TrimSpace(provider)
	marker := importMarker(source, provider)
	if update.Description != nil && hasImportMarker(*update.Description, source) {
		return
	}
	if update.Description == nil || strings.TrimSpace(*update.Description) == "" {
		update.Description = &marker
		return
	}
	description := strings.TrimSpace(*update.Description) + "\n\n" + marker
	update.Description = &description
}

func preserveExistingDescriptionForMarkerOnlyUpdate(existing *wanderer.Trail, update *wanderer.TrailUpdate, source, provider string) {
	if existing == nil || update == nil || update.Description == nil {
		return
	}
	if strings.TrimSpace(existing.Description) == "" {
		return
	}
	if strings.TrimSpace(*update.Description) != importMarker(source, provider) {
		return
	}
	description := existing.Description
	markerUpdate := wanderer.TrailUpdate{Description: &description}
	appendImportMarkerToDescription(&markerUpdate, source, provider)
	update.Description = markerUpdate.Description
}

func importMarker(source, provider string) string {
	lines := []string{
		"Imported by wanderer-import",
		"wanderer-import-source: " + source,
	}
	if provider != "" {
		lines = append(lines, "wanderer-import-provider: "+provider)
	}
	return strings.Join(lines, "\n")
}

func hasImportMarker(description, source string) bool {
	return strings.Contains(description, "wanderer-import-source: "+source) ||
		strings.Contains(description, "Source: "+source)
}
