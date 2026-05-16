package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
	"wanderer-import/internal/manifest"
	"wanderer-import/internal/providers"
	"wanderer-import/internal/session"
	"wanderer-import/internal/wanderer"
)

const version = "0.1.0"

type App struct {
	stdout io.Writer
	stderr io.Writer
}

func New(stdout, stderr io.Writer) *App {
	return &App{stdout: stdout, stderr: stderr}
}

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printUsage()
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		a.printUsage()
		return nil
	case "version", "--version":
		fmt.Fprintln(a.stdout, version)
		return nil
	case "providers":
		return a.printProviders()
	case "export":
		return a.runExport(ctx, args[1:])
	case "import":
		return a.runImport(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) runImport(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(a.stderr)

	wandererURL := flags.String("wanderer-url", env("WANDERER_URL", "http://localhost:3000"), "Wanderer base URL")
	username := flags.String("username", env("WANDERER_USERNAME", ""), "Wanderer username or email")
	password := flags.String("password", env("WANDERER_PASSWORD", ""), "Wanderer password")
	apiToken := flags.String("api-token", env("WANDERER_API_TOKEN", ""), "Wanderer API token")
	pbAuthCookie := flags.String("pb-auth-cookie", env("WANDERER_PB_AUTH", ""), "PocketBase pb_auth cookie value")
	providerName := flags.String("provider", "auto", "source provider")
	manifestPath := flags.String("manifest", "", "JSON manifest path")
	sourcesPath := flags.String("sources", "", "text file with one source URL/path per line")
	name := flags.String("name", "", "trail name to set after upload")
	description := flags.String("description", "", "trail description to set after upload")
	location := flags.String("location", "", "trail location to set after upload")
	date := flags.String("date", "", "trail date to set after upload")
	difficulty := flags.String("difficulty", "", "trail difficulty to set after upload")
	category := flags.String("category", "", "trail category to set after upload")
	lat := flags.String("lat", "", "trail start latitude to set after upload")
	lon := flags.String("lon", "", "trail start longitude to set after upload")
	distance := flags.String("distance", "", "trail distance in meters to set after upload")
	elevationGain := flags.String("elevation-gain", "", "trail elevation gain in meters to set after upload")
	elevationLoss := flags.String("elevation-loss", "", "trail elevation loss in meters to set after upload")
	duration := flags.String("duration", "", "trail duration in seconds to set after upload")
	thumbnail := flags.String("thumbnail", "", "trail thumbnail photo index to set after upload")
	public := flags.Bool("public", false, "mark imported trails public")
	private := flags.Bool("private", false, "mark imported trails private")
	ignoreDuplicates := flags.Bool("ignore-duplicates", false, "ask Wanderer to ignore duplicate trails")
	updateExisting := flags.Bool("update-existing", false, "update an existing trail previously imported from the same source URL")
	failFast := flags.Bool("fail-fast", false, "stop on the first failed import")
	parallel := flags.Int("parallel", 1, "number of parallel imports")
	dryRun := flags.Bool("dry-run", false, "show planned imports without contacting Wanderer")
	jsonOutput := flags.Bool("json", false, "write JSON output")
	browserFetch := flags.String("browser-fetch", env("WANDERER_IMPORT_BROWSER_FETCH", ""), "enable Playwright browser fallback for protected source requests; supported: chromium, chrome, firefox, webkit")
	browserFetchHeadful := flags.Bool("browser-fetch-headful", false, "run Playwright fallback with a visible browser instead of headless mode")
	sourceSession := sourceSessionFlags(flags)

	var tags stringList
	var photoURLs stringList
	var filterProviders stringList
	flags.Var(&tags, "tag", "trail tag metadata to preserve in JSON/export output; repeatable")
	flags.Var(&photoURLs, "photo-url", "remote photo URL to upload after importing; repeatable")
	flags.Var(&filterProviders, "filter-provider", "only process sources matching these provider names; repeatable")

	if err := flags.Parse(args); err != nil {
		return err
	}
	if *public && *private {
		return errors.New("--public and --private cannot be used together")
	}

	update, err := trailUpdateFromFlags(flagUpdateOptions{
		name:          *name,
		description:   *description,
		location:      *location,
		date:          *date,
		difficulty:    *difficulty,
		category:      *category,
		lat:           *lat,
		lon:           *lon,
		distance:      *distance,
		elevationGain: *elevationGain,
		elevationLoss: *elevationLoss,
		duration:      *duration,
		thumbnail:     *thumbnail,
		public:        *public,
		private:       *private,
		tags:          tags,
		photoURLs:     photoURLs,
	})
	if err != nil {
		return err
	}

	sourceArgs := append([]string(nil), flags.Args()...)
	if *sourcesPath != "" {
		sources, err := readSourcesFile(*sourcesPath)
		if err != nil {
			return err
		}
		sourceArgs = append(sourceArgs, sources...)
	}

	specs, err := a.importSpecs(sourceArgs, *manifestPath, *providerName, *ignoreDuplicates, *updateExisting, update)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return errors.New("no imports requested; provide sources, --sources, or --manifest")
	}

	sourceHTTPClient, err := newSourceHTTPClient(ctx, sourceSession)
	if err != nil {
		return err
	}
	browserFetcher, err := newBrowserFetcher(*browserFetch, *browserFetchHeadful)
	if err != nil {
		return err
	}
	if closeable, ok := browserFetcher.(browserfetch.Closeable); ok {
		defer closeable.Close()
	}
	registry := importer.NewRegistry(providers.BuiltinsWithOptions(providers.Options{
		HTTPClient:     sourceHTTPClient,
		BrowserFetcher: browserFetcher,
	})...)

	if len(filterProviders) > 0 {
		specs = a.filterSpecsByProvider(specs, registry, filterProviders)
		if len(specs) == 0 {
			return errors.New("no items to process after filtering by provider")
		}
	}

	if *dryRun {
		return a.printDryRun(specs, *jsonOutput, registry)
	}

	client, err := wanderer.NewClient(*wandererURL, wanderer.WithToken(*apiToken), wanderer.WithPBAuthCookie(*pbAuthCookie))
	if err != nil {
		return err
	}
	if strings.TrimSpace(*apiToken) == "" && strings.TrimSpace(*pbAuthCookie) == "" {
		if strings.TrimSpace(*username) == "" || *password == "" {
			return errors.New("authentication is required; set --api-token, --pb-auth-cookie, or --username and --password")
		}
		if err := client.Login(ctx, *username, *password); err != nil {
			return err
		}
	}

	startTime := time.Now()
	results, err := a.runImportSpecs(ctx, client, registry, specs, importRunOptions{
		failFast: *failFast,
		parallel: *parallel,
		json:     *jsonOutput,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	}

	elapsed := time.Since(startTime)
	successes := 0
	warnings := 0
	failures := 0
	for _, r := range results {
		if r.Error != "" {
			failures++
		} else {
			successes++
			if len(r.Warnings) > 0 {
				warnings++
			}
		}
	}
	fmt.Fprintf(a.stdout, "\nImport completed in %v\n", elapsed.Round(time.Millisecond))
	fmt.Fprintf(a.stdout, "Success: %d, Warnings: %d, Failures: %d\n", successes, warnings, failures)
	return nil
}

type importRunOptions struct {
	failFast bool
	parallel int
	json     bool
}

type importResult struct {
	*importer.Result
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (a *App) runImportSpecs(ctx context.Context, client importer.WandererClient, registry *importer.Registry, specs []importer.Spec, opts importRunOptions) ([]importResult, error) {
	results := make([]importResult, len(specs))
	total := len(specs)
	parallel := opts.parallel
	if parallel < 1 {
		parallel = 1
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(parallel)

	var mu sync.Mutex
	for i, spec := range specs {
		i, spec := i, spec
		g.Go(func() error {
			progress := ""
			if !opts.json {
				progress = fmt.Sprintf("[%d/%d] ", i+1, total)
			}
			result, err := importer.Import(ctx, client, registry, spec)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				failed := importResult{Source: spec.Source, Error: err.Error()}
				results[i] = failed
				if !opts.json {
					writeWarning(a.stderr, "%swarning: failed to import %s: %s\n", progress, spec.Source, err)
				}
				if opts.failFast {
					return fmt.Errorf("import %q: %w", spec.Source, err)
				}
				return nil
			}
			results[i] = importResult{Result: result}
			if !opts.json {
				displayName := result.Name
				if displayName == "" {
					displayName = result.ID
				}
				fmt.Fprintf(a.stdout, "%simported %s via %s: %s\n", progress, result.Source, result.Provider, displayName)
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return results, err
	}
	return results, nil
}

func (a *App) runExport(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(a.stderr)

	providerName := flags.String("provider", "auto", "source provider")
	manifestPath := flags.String("manifest", "", "JSON manifest path")
	sourcesPath := flags.String("sources", "", "text file with one source URL/path per line")
	outputDir := flags.String("output-dir", "exports/gpx", "directory for exported trail files")
	continueOnError := flags.Bool("continue-on-error", false, "continue exporting after a source fails")
	jsonOutput := flags.Bool("json", false, "write JSON output")
	timeout := flags.Duration("timeout", 30*time.Second, "per-source resolve timeout")
	browserFetch := flags.String("browser-fetch", env("WANDERER_IMPORT_BROWSER_FETCH", ""), "enable Playwright browser fallback for protected source requests; supported: chromium, chrome, firefox, webkit")
	browserFetchHeadful := flags.Bool("browser-fetch-headful", false, "run Playwright fallback with a visible browser instead of headless mode")
	sourceSession := sourceSessionFlags(flags)

	var filterProviders stringList
	flags.Var(&filterProviders, "filter-provider", "only process sources matching these provider names; repeatable")

	if err := flags.Parse(args); err != nil {
		return err
	}

	sourceArgs := append([]string(nil), flags.Args()...)
	if *sourcesPath != "" {
		sources, err := readSourcesFile(*sourcesPath)
		if err != nil {
			return err
		}
		sourceArgs = append(sourceArgs, sources...)
	}

	specs, err := a.importSpecs(sourceArgs, *manifestPath, *providerName, false, false, wanderer.TrailUpdate{})
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return errors.New("no exports requested; provide sources, --sources, or --manifest")
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}

	sourceHTTPClient, err := newSourceHTTPClient(ctx, sourceSession)
	if err != nil {
		return err
	}
	browserFetcher, err := newBrowserFetcher(*browserFetch, *browserFetchHeadful)
	if err != nil {
		return err
	}
	if closeable, ok := browserFetcher.(browserfetch.Closeable); ok {
		defer closeable.Close()
	}
	registry := importer.NewRegistry(providers.BuiltinsWithOptions(providers.Options{
		HTTPClient:     sourceHTTPClient,
		BrowserFetcher: browserFetcher,
	})...)

	if len(filterProviders) > 0 {
		specs = a.filterSpecsByProvider(specs, registry, filterProviders)
		if len(specs) == 0 {
			return errors.New("no items to process after filtering by provider")
		}
	}

	results := make([]exportResult, 0, len(specs))
	for i, spec := range specs {
		result := a.exportOne(ctx, registry, spec, *outputDir, i+1, *timeout)
		results = append(results, result)

		if !*jsonOutput {
			if result.Error != "" {
				writeError(a.stderr, "failed %s via %s: %s\n", result.Source, result.Provider, result.Error)
			} else {
				fmt.Fprintf(a.stdout, "exported %s via %s: %s\n", result.Source, result.Provider, result.Path)
			}
		}
		if result.Error != "" && !*continueOnError {
			return fmt.Errorf("export %q: %s", result.Source, result.Error)
		}
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	}
	return nil
}

func (a *App) exportOne(ctx context.Context, registry *importer.Registry, spec importer.Spec, outputDir string, index int, timeout time.Duration) exportResult {
	result := exportResult{Index: index, Source: spec.Source}

	provider, err := registry.Select(spec.Provider, spec.Source)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Provider = provider.Name()

	resolveCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	resolved, err := provider.Resolve(resolveCtx, spec)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resolved.Close()
	metadata := wanderer.MergeTrailUpdates(resolved.Metadata, spec.Update)
	result.Metadata = metadata

	filename := exportFilename(index, provider.Name(), resolved.Filename)
	path := filepath.Join(outputDir, filename)
	file, err := os.Create(path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	bytes, copyErr := io.Copy(file, resolved.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		result.Error = copyErr.Error()
		return result
	}
	if closeErr != nil {
		_ = os.Remove(path)
		result.Error = closeErr.Error()
		return result
	}

	result.Path = path
	result.Bytes = bytes
	if !metadata.Empty() {
		metadataPath := path + ".metadata.json"
		if err := writeMetadataSidecar(metadataPath, exportMetadataSidecar{
			Source:   spec.Source,
			Provider: provider.Name(),
			File:     path,
			Metadata: metadata,
		}); err != nil {
			result.Error = err.Error()
			return result
		}
		result.MetadataPath = metadataPath
	}
	return result
}

type exportResult struct {
	Index        int                  `json:"index"`
	Source       string               `json:"source"`
	Provider     string               `json:"provider,omitempty"`
	Path         string               `json:"path,omitempty"`
	MetadataPath string               `json:"metadata_path,omitempty"`
	Bytes        int64                `json:"bytes,omitempty"`
	Metadata     wanderer.TrailUpdate `json:"metadata,omitempty"`
	Error        string               `json:"error,omitempty"`
}

type exportMetadataSidecar struct {
	Source   string               `json:"source"`
	Provider string               `json:"provider"`
	File     string               `json:"file"`
	Metadata wanderer.TrailUpdate `json:"metadata"`
}

type sourceSessionOptions struct {
	cookies            *string
	cookiesFromBrowser *string
	userAgent          *string
	referer            *string
	impersonate        *string
}

func sourceSessionFlags(flags *flag.FlagSet) sourceSessionOptions {
	return sourceSessionOptions{
		cookies:            flags.String("cookies", env("WANDERER_IMPORT_COOKIES", ""), "Netscape cookies.txt file for source websites"),
		cookiesFromBrowser: flags.String("cookies-from-browser", env("WANDERER_IMPORT_COOKIES_FROM_BROWSER", ""), "load source website cookies from a local browser store, optionally browser:profile"),
		userAgent:          flags.String("user-agent", env("WANDERER_IMPORT_USER_AGENT", ""), "User-Agent override for source website requests"),
		referer:            flags.String("referer", env("WANDERER_IMPORT_REFERER", ""), "Referer override for source website requests"),
		impersonate:        flags.String("impersonate", env("WANDERER_IMPORT_IMPERSONATE", ""), "header impersonation profile for source website requests; supported: chrome, firefox, safari"),
	}
}

func newSourceHTTPClient(ctx context.Context, opts sourceSessionOptions) (*http.Client, error) {
	return session.NewHTTPClient(ctx, session.Options{
		CookiesFile:        stringFlag(opts.cookies),
		CookiesFromBrowser: stringFlag(opts.cookiesFromBrowser),
		UserAgent:          stringFlag(opts.userAgent),
		Referer:            stringFlag(opts.referer),
		Impersonate:        stringFlag(opts.impersonate),
	})
}

func newBrowserFetcher(browser string, headful bool) (browserfetch.Fetcher, error) {
	browser = strings.TrimSpace(browser)
	if browser == "" || strings.EqualFold(browser, "off") || strings.EqualFold(browser, "false") {
		return nil, nil
	}
	return browserfetch.NewPlaywrightFetcher(browserfetch.PlaywrightOptions{
		Browser:  browser,
		Headless: !headful,
	})
}

func stringFlag(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (a *App) importSpecs(args []string, manifestPath, providerName string, ignoreDuplicates, updateExisting bool, update wanderer.TrailUpdate) ([]importer.Spec, error) {
	var specs []importer.Spec
	if manifestPath != "" {
		file, err := os.Open(manifestPath)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		manifestSpecs, err := manifest.Load(file)
		if err != nil {
			return nil, err
		}
		for i := range manifestSpecs {
			if manifestSpecs[i].Provider == "" && providerName != "auto" {
				manifestSpecs[i].Provider = providerName
			}
			if ignoreDuplicates {
				manifestSpecs[i].IgnoreDuplicates = true
			}
			if updateExisting {
				manifestSpecs[i].UpdateExisting = true
			}
		}
		specs = append(specs, manifestSpecs...)
	}

	for _, source := range args {
		specs = append(specs, importer.Spec{
			Source:           source,
			Provider:         providerName,
			IgnoreDuplicates: ignoreDuplicates,
			UpdateExisting:   updateExisting,
			Update:           update,
		})
	}
	return specs, nil
}

func (a *App) printDryRun(specs []importer.Spec, jsonOutput bool, registry *importer.Registry) error {
	results := make([]dryRunResult, 0, len(specs))
	for _, spec := range specs {
		provider, err := registry.Select(spec.Provider, spec.Source)
		if err != nil {
			return err
		}
		results = append(results, dryRunResult{
			Source:   spec.Source,
			Provider: provider.Name(),
			Spec:     spec,
		})
	}

	if jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	}

	for _, result := range results {
		fmt.Fprintf(a.stdout, "would import %s via %s\n", result.Source, result.Provider)
	}
	return nil
}

type dryRunResult struct {
	Source   string        `json:"source"`
	Provider string        `json:"provider"`
	Spec     importer.Spec `json:"spec"`
}

func (a *App) printProviders() error {
	for _, provider := range providers.Builtins(nil) {
		descriptor := importer.Descriptor{ID: provider.Name()}
		if described, ok := provider.(importer.DescribedProvider); ok {
			descriptor = described.Descriptor()
		}
		domains := strings.Join(descriptor.Domains, ",")
		if domains == "" {
			domains = "-"
		}
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", descriptor.ID, descriptor.Engine, domains)
	}
	return nil
}

func (a *App) printUsage() {
	fmt.Fprint(a.stdout, `wanderer-import imports trail files into Wanderer.

Usage:
  wanderer-import import [flags] <file-or-url>...
  wanderer-import import --manifest imports.json [flags]
  wanderer-import import --sources hike_urls.txt [flags]
  wanderer-import export [flags] <file-or-url>...
  wanderer-import export --sources hike_urls.txt --output-dir exports/gpx
  wanderer-import providers
  wanderer-import version

Common import flags:
  --wanderer-url URL       Wanderer base URL (env WANDERER_URL, default http://localhost:3000)
  --api-token TOKEN        API token (env WANDERER_API_TOKEN)
  --username USER          Username or email for /api/v1/auth/login (env WANDERER_USERNAME)
  --password PASS          Password for /api/v1/auth/login (env WANDERER_PASSWORD)
  --pb-auth-cookie COOKIE  Raw pb_auth cookie value (env WANDERER_PB_AUTH)
  --name NAME              Set the trail name after upload
  --description TEXT       Set the trail description after upload
  --location TEXT          Set the trail location after upload
  --difficulty VALUE       Set difficulty: easy, moderate, or difficult
  --distance METERS        Set distance after upload
  --elevation-gain METERS  Set elevation gain after upload
  --elevation-loss METERS  Set elevation loss after upload
  --duration SECONDS       Set duration after upload
  --tag TAG                Preserve tag metadata in JSON/export output; repeatable
  --photo-url URL          Upload a remote photo after import; repeatable
  --public                 Mark imported trails public
  --private                Mark imported trails private
  --manifest PATH          Import entries from a JSON manifest
  --sources PATH           Text file with one source URL/path per line
  --filter-provider NAME   Only process sources matching these provider names; repeatable
  --update-existing        Update a trail previously imported from the same source URL
  --fail-fast              Stop on the first failed import
  --dry-run                Show planned imports
  --json                   Write JSON output
  --cookies PATH           Netscape cookies.txt file for source websites (env WANDERER_IMPORT_COOKIES)
  --cookies-from-browser BROWSER
                           Load source cookies from browser store, optionally browser:profile
  --user-agent VALUE       User-Agent override for source website requests
  --referer URL            Referer override for source website requests
  --impersonate PROFILE    Header impersonation profile for source requests; supported: chrome, firefox, safari
  --browser-fetch BROWSER  Enable Playwright fallback for protected source requests; chromium, chrome, firefox, webkit
  --browser-fetch-headful  Run Playwright fallback with a visible browser instead of headless mode

Common export flags:
  --sources PATH           Text file with one source URL/path per line
  --manifest PATH          Import entries from a JSON manifest
  --output-dir PATH        Directory for exported trail files
  --provider NAME          Source provider (default auto)
  --filter-provider NAME   Only process sources matching these provider names; repeatable
  --continue-on-error      Continue exporting after failures
  --timeout DURATION       Per-source resolve timeout
  --json                   Write JSON output
  --cookies PATH           Netscape cookies.txt file for source websites
  --cookies-from-browser BROWSER
                           Load source cookies from browser store, optionally browser:profile
  --user-agent VALUE       User-Agent override for source website requests
  --referer URL            Referer override for source website requests
  --impersonate PROFILE    Header impersonation profile for source requests; supported: chrome, firefox, safari
  --browser-fetch BROWSER  Enable Playwright fallback for protected source requests; chromium, chrome, firefox, webkit
  --browser-fetch-headful  Run Playwright fallback with a visible browser instead of headless mode
`)
}

type flagUpdateOptions struct {
	name          string
	description   string
	location      string
	date          string
	difficulty    string
	category      string
	lat           string
	lon           string
	distance      string
	elevationGain string
	elevationLoss string
	duration      string
	thumbnail     string
	public        bool
	private       bool
	tags          []string
	photoURLs     []string
}

func trailUpdateFromFlags(opts flagUpdateOptions) (wanderer.TrailUpdate, error) {
	var public *bool
	if opts.public || opts.private {
		value := opts.public
		public = &value
	}

	lat, err := optionalFloatFlag("lat", opts.lat)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}
	lon, err := optionalFloatFlag("lon", opts.lon)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}
	distance, err := optionalFloatFlag("distance", opts.distance)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}
	elevationGain, err := optionalFloatFlag("elevation-gain", opts.elevationGain)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}
	elevationLoss, err := optionalFloatFlag("elevation-loss", opts.elevationLoss)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}
	duration, err := optionalFloatFlag("duration", opts.duration)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}
	thumbnail, err := optionalIntFlag("thumbnail", opts.thumbnail)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}
	difficulty, err := optionalDifficultyFlag(opts.difficulty)
	if err != nil {
		return wanderer.TrailUpdate{}, err
	}

	return wanderer.TrailUpdate{
		Name:          optionalString(opts.name),
		Description:   optionalString(opts.description),
		Location:      optionalString(opts.location),
		Date:          optionalString(opts.date),
		Difficulty:    difficulty,
		Category:      optionalString(opts.category),
		Public:        public,
		Lat:           lat,
		Lon:           lon,
		Distance:      distance,
		ElevationGain: elevationGain,
		ElevationLoss: elevationLoss,
		Duration:      duration,
		Thumbnail:     thumbnail,
		Tags:          nonEmptyStrings(opts.tags),
		PhotoURLs:     nonEmptyStrings(opts.photoURLs),
	}, nil
}

func optionalDifficultyFlag(value string) (*string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	normalized, ok := wanderer.NormalizeDifficulty(value)
	if !ok {
		return nil, fmt.Errorf("invalid --difficulty %q; expected %s, %s, or %s", value, wanderer.DifficultyEasy, wanderer.DifficultyModerate, wanderer.DifficultyDifficult)
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

func optionalFloatFlag(name, value string) (*float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, fmt.Errorf("--%s must be a number", name)
	}
	return &parsed, nil
}

func optionalIntFlag(name, value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("--%s must be an integer", name)
	}
	return &parsed, nil
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

func readSourcesFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var sources []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		source := strings.TrimSpace(scanner.Text())
		if source == "" || strings.HasPrefix(source, "#") {
			continue
		}
		sources = append(sources, source)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func writeMetadataSidecar(path string, sidecar exportMetadataSidecar) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(sidecar)
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(path)
		return encodeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	return nil
}

func exportFilename(index int, provider, filename string) string {
	filename = cleanFilename(filename)
	if filename == "" {
		filename = "trail.gpx"
	}
	if filepath.Ext(filename) == "" {
		filename += ".gpx"
	}
	return fmt.Sprintf("%03d-%s-%s", index, slugPart(provider), filename)
}

func cleanFilename(filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), "\\", "/")
	filename = filepath.Base(filename)
	filename = strings.Trim(filename, ". ")
	if filename == "" || filename == string(filepath.Separator) {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range filename {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func slugPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "provider"
	}
	return slug
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func (a *App) filterSpecsByProvider(specs []importer.Spec, registry *importer.Registry, allowedProviders stringList) []importer.Spec {
	allowed := make(map[string]bool)
	for _, p := range allowedProviders {
		allowed[strings.ToLower(strings.TrimSpace(p))] = true
	}
	var filtered []importer.Spec
	for _, spec := range specs {
		provider, err := registry.Select(spec.Provider, spec.Source)
		if err == nil && allowed[strings.ToLower(provider.Name())] {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}
