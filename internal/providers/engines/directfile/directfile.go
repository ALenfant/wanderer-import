package directfile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/providerkit"
)

type Engine struct {
	httpClient *http.Client
}

func New(httpClient *http.Client) *Engine {
	return &Engine{httpClient: providerkit.HTTPClient(httpClient)}
}

func (e *Engine) Match(source string) importer.Match {
	source = strings.TrimSpace(source)
	if source == "" {
		return importer.Match{}
	}
	if _, ok := providerkit.ParseHTTPURL(source); ok {
		if providerkit.HasTrailFileExtension(source) {
			return importer.Match{OK: true, Score: 20, Reason: "direct trail-file URL"}
		}
		return importer.Match{}
	}
	return importer.Match{OK: true, Score: 20, Reason: "local file path"}
}

func (e *Engine) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	if _, ok := providerkit.ParseHTTPURL(source); ok {
		download, err := providerkit.DownloadVerifiedTrail(ctx, e.httpClient, source)
		if err != nil {
			return nil, err
		}
		return &importer.ResolvedTrail{
			Source:   download.Source,
			Filename: download.Filename,
			Body:     download.Body,
			Metadata: download.Metadata,
		}, nil
	}
	return e.resolveFile(source)
}

func (e *Engine) resolveFile(source string) (*importer.ResolvedTrail, error) {
	file, err := os.Open(source)
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = file.Close()
		return nil, fmt.Errorf("%q is a directory", source)
	}

	data, err := io.ReadAll(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	_ = file.Close()

	return &importer.ResolvedTrail{
		Source:   source,
		Filename: filepath.Base(source),
		Body:     io.NopCloser(bytes.NewReader(data)),
		Metadata: providerkit.MetadataFromTrailData(source, nil, data),
	}, nil
}
