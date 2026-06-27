package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kevin-burns/linkedin-assist/internal/auth"
	"github.com/kevin-burns/linkedin-assist/internal/cache"
	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/enrich"
	"github.com/kevin-burns/linkedin-assist/internal/output"
	"github.com/kevin-burns/linkedin-assist/internal/ratelimit"
	"github.com/kevin-burns/linkedin-assist/internal/usecase"
	"github.com/kevin-burns/linkedin-assist/internal/voyager"
)

// newJobsCmd returns the "jobs" parent command.
func newJobsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "LinkedIn jobs commands",
	}
	cmd.AddCommand(newJobsSearchCmd())
	cmd.AddCommand(newJobsGetCmd())
	cmd.AddCommand(newJobsSweepCmd())
	return cmd
}

// openCacheForGet opens the local job cache and returns the adapter plus a
// flush function. On failure, returns a no-op cache and a no-op flush so
// callers can proceed unconditionally.
func openCacheForGet() (jobCache usecase.JobCache, flush func()) {
	s, err := cache.Open(cache.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not open job cache: %v -- running without cache\n", err)
		return usecase.NoopCache(), func() {}
	}
	adapter := cache.NewUCAdapter(s)
	return adapter, func() {
		if err := adapter.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not flush job cache: %v\n", err)
		}
	}
}

// newJobsSearchCmd returns the "jobs search" subcommand.
func newJobsSearchCmd() *cobra.Command {
	var location string
	var anywhere bool
	var limit int
	var format string
	var excludeCompanies []string
	var excludeTitles []string

	cmd := &cobra.Command{
		Use:   "search <keyword...>",
		Short: "Search LinkedIn jobs",
		Long: `Search LinkedIn job postings by keyword.

Multiple positional arguments are joined with a space to form the keyword
string, so the following are equivalent:

  li-assist jobs search senior platform engineer
  li-assist jobs search "senior platform engineer"

LinkedIn boolean operators are supported in the keyword and are passed
through to LinkedIn's search engine:

  li-assist jobs search '"platform engineer" OR devops NOT recruiter'

Use --exclude-title to drop results whose title contains a term
(case-insensitive substring). For server-side title exclusion, the
LinkedIn NOT operator in the keyword also works.

Location resolution (applied in order):
  1. --anywhere   → worldwide search (no location filter)
  2. --location X → use X as the location
  3. (default)    → use home_location from 'li-assist config location'

Requires an active login session (run 'li-assist auth login' first).
Results are printed to stdout as JSON by default.`,
		Args: cobra.MinimumNArgs(1),
		// Errors are printed once by main(); cobra should not reprint them or
		// dump usage on a runtime failure.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			keyword := strings.Join(args, " ")

			if format != "json" && format != "okf" {
				return fmt.Errorf("unsupported --format %q: allowed values are json, okf", format)
			}

			// Load home location from config (used as fallback when --location is absent).
			cfg, err := loadConfig(defaultConfigPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Resolve location with precedence: --anywhere > --location > home_location.
			resolvedLocation, err := resolveLocation(anywhere, location, cfg.HomeLocation)
			if err != nil {
				return err
			}

			// Load persistent exclusion list and merge with flag values.
			fileExclusions, err := loadExcludedCompanies(defaultExcludePath())
			if err != nil {
				return fmt.Errorf("load excluded companies: %w", err)
			}
			allExclusions := append(fileExclusions, excludeCompanies...)

			sess, err := auth.Open(cmd.Context(), true)
			if err != nil {
				return fmt.Errorf("open browser session: %w", err)
			}
			defer sess.Close()

			if !sess.LoggedIn() {
				// Return (not os.Exit) so the deferred sess.Close() runs and the
				// headless Chrome process is not leaked.
				return fmt.Errorf("not logged in -- run 'li-assist auth login' first")
			}

			limiter := ratelimit.NewLimiter(ratelimit.OptionsFromEnv())
			transport := voyager.NewTransport(sess, limiter)
			jobsClient := voyager.NewJobsClient(transport)

			adapter := &voyagerJobsRepoAdapter{client: jobsClient}
			uc := usecase.SearchJobs{Repo: adapter}

			result, err := uc.Execute(cmd.Context(), usecase.JobSearchRequest{
				Keyword:           keyword,
				Location:          resolvedLocation,
				Limit:             limit,
				ExcludeCompanies:  allExclusions,
				ExcludeTitleTerms: excludeTitles,
			})
			if err != nil {
				return fmt.Errorf("jobs search: %w", err)
			}

			var b []byte
			switch format {
			case "json":
				b, err = output.JobsJSON(result.Jobs)
			case "okf":
				b, err = output.JobsOKF(result.Jobs)
			}
			if err != nil {
				return fmt.Errorf("render output: %w", err)
			}

			fmt.Fprintln(os.Stdout, string(b))

			if result.Hidden > 0 {
				fmt.Fprintf(os.Stderr, "(%d result(s) hidden by exclusions)\n", result.Hidden)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&location, "location", "", "Filter by location (e.g. \"San Francisco, CA\"); overrides home_location from config")
	cmd.Flags().BoolVar(&anywhere, "anywhere", false, "Search worldwide (no location filter); mutually exclusive with --location")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum number of results to return")
	cmd.Flags().StringVar(&format, "format", "json", "Output format: json or okf")
	cmd.Flags().StringArrayVar(&excludeCompanies, "exclude-company", nil, "Company name to exclude from results (repeatable); also read from ~/.config/li-assist/excluded-companies.txt")
	cmd.Flags().StringArrayVar(&excludeTitles, "exclude-title", nil, "Drop results whose title contains this term (case-insensitive); repeatable")

	return cmd
}

// voyagerJobsRepoAdapter adapts voyager.JobsClient to the usecase.JobsRepo
// port. This is the only place allowed to import both voyager and usecase.
type voyagerJobsRepoAdapter struct {
	client *voyager.JobsClient
}

func (a *voyagerJobsRepoAdapter) Search(ctx context.Context, req usecase.JobSearchRequest) ([]domain.Job, error) {
	return a.client.Search(ctx, voyager.JobSearchParams{
		Keywords: req.Keyword,
		Location: req.Location,
		Count:    req.Limit,
		Start:    0,
	})
}

// voyagerJobGetterAdapter adapts voyager.JobsClient to the usecase.JobGetter
// port. This is the only place allowed to import both voyager and usecase.
type voyagerJobGetterAdapter struct {
	client *voyager.JobsClient
}

func (a *voyagerJobGetterAdapter) Get(ctx context.Context, urn domain.URN) (domain.Job, error) {
	return a.client.Get(ctx, urn)
}

// newJobsGetCmd returns the "jobs get <urn>" subcommand.
func newJobsGetCmd() *cobra.Command {
	var format string
	var refresh bool
	var enrichFlag bool
	var introsFlag bool
	var connectionsFlag string

	cmd := &cobra.Command{
		Use:   "get <urn>",
		Short: "Get full detail for a LinkedIn job posting",
		Long: `Fetch the full detail for a LinkedIn job posting by URN.

The URN must be in the form urn:li:fsd_jobPosting:<id>.

Requires an active login session (run 'li-assist auth login' first).
Result is printed to stdout as JSON by default.

The result is cached locally (~/.config/li-assist/cache/jobs.jsonl).
A subsequent call for the same URN returns the cached result without
a network call. Use --refresh to force a re-fetch from LinkedIn.

Use --enrich to run LLM-powered analysis of the job description. The
enrichment provider is selected automatically (Ollama → OpenAI → Gemini →
Anthropic); set LI_ASSIST_ENRICH_PROVIDER to force a specific one. Set
LI_ASSIST_ENRICH_MODEL to override the model (example; verify with your
provider). Insights are cached and reused on subsequent calls (enrich-once).

Use --intros to surface 1st-degree LinkedIn connections at the job's company
as warm-intro candidates. Requires a Connections.csv export from LinkedIn
(Settings → Data Privacy → Get a copy of your data → Connections). This is
100% offline — zero new network calls. The file path is resolved via:
  1. --connections <path>
  2. LI_ASSIST_CONNECTIONS_CSV env var
  3. connections_path in config (li-assist config connections)
  4. ~/.config/li-assist/connections.csv (default)`,
		Args: cobra.ExactArgs(1),
		// Errors are printed once by main(); cobra should not reprint them or
		// dump usage on a runtime failure.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rawURN := args[0]

			if format != "json" && format != "okf" {
				return fmt.Errorf("unsupported --format %q: allowed values are json, okf", format)
			}

			// Validate the URN before opening a browser session.
			urn, err := domain.ParseURN(rawURN)
			if err != nil {
				return fmt.Errorf("invalid URN %q: must be in the form urn:li:fsd_jobPosting:<id>", rawURN)
			}

			// Load config for connections path resolution.
			cfg, err := loadConfig(defaultConfigPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Load connections once if --intros is set. Failures are non-fatal.
			var conns []domain.Connection
			if introsFlag {
				connPath := resolveConnectionsPath(connectionsFlag, cfg)
				conns, _ = loadConnectionsForIntros(connPath)
			}

			// Open cache first -- if it's a hit we can skip the browser entirely.
			getCache, flushGetCache := openCacheForGet()
			defer flushGetCache()

			// Cache-first shortcut: if the cache has a record with description
			// and we are not refreshing, return immediately without a browser call.
			if !refresh {
				if j, hasDesc, ok := getCache.Get(string(urn)); ok && hasDesc {
					var ins *domain.Insights
					if enrichFlag {
						ins = enrichJobWithCache(cmd.Context(), j, getCache)
					}
					var intros []domain.Intro
					if introsFlag {
						intros = usecase.MatchIntros(j, conns)
					}
					var b []byte
					switch format {
					case "json":
						b, err = output.JobJSONWithIntros(j, ins, intros)
					case "okf":
						err = fmt.Errorf("okf output not yet implemented: Ogham Knowledge Format schema not yet provided")
					}
					if err != nil {
						return fmt.Errorf("render output: %w", err)
					}
					fmt.Fprintln(os.Stdout, string(b))
					return nil
				}
			}

			sess, err := auth.Open(cmd.Context(), true)
			if err != nil {
				return fmt.Errorf("open browser session: %w", err)
			}
			defer sess.Close()

			if !sess.LoggedIn() {
				// Return (not os.Exit) so the deferred sess.Close() runs and the
				// headless Chrome process is not leaked.
				return fmt.Errorf("not logged in -- run 'li-assist auth login' first")
			}

			limiter := ratelimit.NewLimiter(ratelimit.OptionsFromEnv())
			transport := voyager.NewTransport(sess, limiter)
			jobsClient := voyager.NewJobsClient(transport)

			getterAdapter := &voyagerJobGetterAdapter{client: jobsClient}
			uc := usecase.GetJob{Repo: getterAdapter, Cache: getCache, Refresh: refresh}

			job, err := uc.Execute(cmd.Context(), urn)
			if err != nil {
				return fmt.Errorf("jobs get: %w", err)
			}

			var ins *domain.Insights
			if enrichFlag {
				ins = enrichJobWithCache(cmd.Context(), job, getCache)
			}

			var intros []domain.Intro
			if introsFlag {
				intros = usecase.MatchIntros(job, conns)
			}

			var b []byte
			switch format {
			case "json":
				b, err = output.JobJSONWithIntros(job, ins, intros)
			case "okf":
				err = fmt.Errorf("okf output not yet implemented: Ogham Knowledge Format schema not yet provided")
			}
			if err != nil {
				return fmt.Errorf("render output: %w", err)
			}

			fmt.Fprintln(os.Stdout, string(b))
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "json", "Output format: json or okf")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Force re-fetch from LinkedIn even if cached")
	cmd.Flags().BoolVar(&enrichFlag, "enrich", false, "Run LLM analysis on the job description (enrich-once; cached per URN)")
	cmd.Flags().BoolVar(&introsFlag, "intros", false, "Surface 1st-degree LinkedIn connections at the job's company as warm-intro candidates (offline; requires Connections.csv export)")
	cmd.Flags().StringVar(&connectionsFlag, "connections", "", "Path to LinkedIn Connections.csv export (overrides env LI_ASSIST_CONNECTIONS_CSV and config)")

	return cmd
}

// enrichJobWithCache runs LLM enrichment for job using getCache for the
// enrich-once policy. Returns a pointer to the insights on success, or nil
// when no provider is configured or enrichment fails (with a note to stderr).
// Never causes the calling command to fail.
func enrichJobWithCache(ctx context.Context, job domain.Job, getCache usecase.JobCache) *domain.Insights {
	enricher, err := enrich.NewFromEnv()
	if err != nil {
		if enrich.IsErrNoProvider(err) {
			fmt.Fprintln(os.Stderr,
				"enrich: no provider configured (set OPENAI_API_KEY / GEMINI_API_KEY / ANTHROPIC_API_KEY or run Ollama); skipping")
		} else {
			fmt.Fprintf(os.Stderr, "enrich: provider setup failed: %v; skipping\n", err)
		}
		return nil
	}

	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: getCache}
	ins, err := enrichUC.Execute(ctx, job)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrich: %v; skipping\n", err)
		return nil
	}
	return &ins
}

// enrichMaxPerRun reads LI_ASSIST_ENRICH_MAX_PER_RUN from the environment and
// returns the cap as an int. Returns defaultCap when the variable is unset or
// unparseable.
func enrichMaxPerRun(defaultCap int) int {
	v := os.Getenv("LI_ASSIST_ENRICH_MAX_PER_RUN")
	if v == "" {
		return defaultCap
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		fmt.Fprintf(os.Stderr, "WARNING: LI_ASSIST_ENRICH_MAX_PER_RUN=%q is not a positive integer; using default %d\n", v, defaultCap)
		return defaultCap
	}
	return n
}

// newJobsSweepCmd returns the "jobs sweep <keyword...>" subcommand.
func newJobsSweepCmd() *cobra.Command {
	var location string
	var anywhere bool
	var limit int
	var excludeCompanies []string
	var excludeTitles []string
	var all bool
	var format string
	var enrichFlag bool
	var introsFlag bool
	var connectionsFlag string

	cmd := &cobra.Command{
		Use:   "sweep <keyword...>",
		Short: "Search jobs and surface only new postings (diff against local cache)",
		Long: `Run a job search and compare results against the local cache.

Multiple positional arguments are joined with a space to form the keyword
string, so the following are equivalent:

  li-assist jobs sweep senior platform engineer
  li-assist jobs sweep "senior platform engineer"

LinkedIn boolean operators are supported in the keyword and are passed
through to LinkedIn's search engine:

  li-assist jobs sweep '"platform engineer" OR devops NOT recruiter'

New postings (not previously seen) are printed to stdout as JSON.
An audit line is always printed to stderr:

  sweep: N new / M seen / K excluded (cache: P jobs)

Use --exclude-title to drop results whose title contains a term
(case-insensitive substring). For server-side title exclusion, the
LinkedIn NOT operator in the keyword also works.

Location resolution (applied in order):
  1. --anywhere   → worldwide search (no location filter)
  2. --location X → use X as the location
  3. (default)    → use home_location from 'li-assist config location'

Use --all to print both new and seen results.
Use --enrich to fetch full job details and run LLM analysis for each new
posting (enrich-once; cached per URN). The per-run cap defaults to 25 and is
overridable via LI_ASSIST_ENRICH_MAX_PER_RUN. Plain sweep (no flag) performs
only the search — no detail fetches.

Use --intros to surface 1st-degree LinkedIn connections at each new job's
company as warm-intro candidates. This is 100% offline — zero new network calls.
Results are cached locally at ~/.config/li-assist/cache/jobs.jsonl.

Requires an active login session (run 'li-assist auth login' first).`,
		Args: cobra.MinimumNArgs(1),
		// Errors are printed once by main(); cobra should not reprint them or
		// dump usage on a runtime failure.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			keyword := strings.Join(args, " ")

			if format != "json" {
				return fmt.Errorf("unsupported --format %q: only json is supported for sweep", format)
			}

			// Load config for location and connections path resolution.
			cfg, err := loadConfig(defaultConfigPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Resolve location with precedence: --anywhere > --location > home_location.
			resolvedLocation, err := resolveLocation(anywhere, location, cfg.HomeLocation)
			if err != nil {
				return err
			}

			// Load persistent exclusion list and merge with flag values.
			fileExclusions, err := loadExcludedCompanies(defaultExcludePath())
			if err != nil {
				return fmt.Errorf("load excluded companies: %w", err)
			}
			allExclusions := append(fileExclusions, excludeCompanies...)

			// Load connections once if --intros is set. Failures are non-fatal.
			var conns []domain.Connection
			if introsFlag {
				connPath := resolveConnectionsPath(connectionsFlag, cfg)
				conns, _ = loadConnectionsForIntros(connPath)
			}

			// Open cache before the browser session.
			var sweepCache usecase.JobCache
			cacheStore, cacheErr := cache.Open(cache.DefaultPath())
			if cacheErr != nil {
				fmt.Fprintf(os.Stderr, "WARNING: could not open job cache: %v -- running without cache\n", cacheErr)
				sweepCache = usecase.NoopCache()
			} else {
				adapter := cache.NewUCAdapter(cacheStore)
				sweepCache = adapter
				defer func() {
					if flushErr := cacheStore.Flush(); flushErr != nil {
						fmt.Fprintf(os.Stderr, "WARNING: could not flush job cache: %v\n", flushErr)
					}
				}()
			}

			sess, err := auth.Open(cmd.Context(), true)
			if err != nil {
				return fmt.Errorf("open browser session: %w", err)
			}
			defer sess.Close()

			if !sess.LoggedIn() {
				return fmt.Errorf("not logged in -- run 'li-assist auth login' first")
			}

			limiter := ratelimit.NewLimiter(ratelimit.OptionsFromEnv())
			transport := voyager.NewTransport(sess, limiter)
			jobsClient := voyager.NewJobsClient(transport)

			repoAdapter := &voyagerJobsRepoAdapter{client: jobsClient}
			uc := usecase.SweepJobs{Repo: repoAdapter, Cache: sweepCache}

			result, err := uc.Execute(cmd.Context(), usecase.JobSearchRequest{
				Keyword:           keyword,
				Location:          resolvedLocation,
				Limit:             limit,
				ExcludeCompanies:  allExclusions,
				ExcludeTitleTerms: excludeTitles,
			})
			if err != nil {
				return fmt.Errorf("jobs sweep: %w", err)
			}

			// --- Optional enrichment of new jobs ---
			//
			// enrichedInsights maps URN string → *domain.Insights for enriched new
			// jobs; nil when --enrich is not set or no provider is available. It is
			// populated before the output step so insights appear inline.
			var enrichedInsights map[string]*domain.Insights
			// detailedNewJobs holds full-detail cache records for enriched new jobs
			// so the output carries real description/apply_url/applicant_count.
			var detailedNewJobs []domain.Job
			// auditSuffix is appended to the base audit line when enrichment ran.
			var auditSuffix string

			if enrichFlag && result.NewCount > 0 {
				cap := enrichMaxPerRun(usecase.DefaultEnrichCap)

				// Build the enricher. If no provider is available, print ONE note
				// to stderr and continue the sweep without enriching — same pattern
				// as `jobs get --enrich`.
				enricher, enricherErr := enrich.NewFromEnv()
				if enricherErr != nil {
					if enrich.IsErrNoProvider(enricherErr) {
						fmt.Fprintln(os.Stderr,
							"enrich: no provider configured (set OPENAI_API_KEY / GEMINI_API_KEY / ANTHROPIC_API_KEY or run Ollama); skipping enrichment")
					} else {
						fmt.Fprintf(os.Stderr, "enrich: provider setup failed: %v; skipping enrichment\n", enricherErr)
					}
					// Fall through: enrichedInsights stays nil, audit suffix stays empty.
				} else {
					getterAdapter := &voyagerJobGetterAdapter{client: jobsClient}
					getUC := usecase.GetJob{Repo: getterAdapter, Cache: sweepCache}
					enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: sweepCache}
					enrichNewUC := usecase.EnrichNewJobs{
						GetJob:    getUC,
						EnrichJob: enrichUC,
						Cap:       cap,
						ErrLogger: func(urn domain.URN, stage string, e error) {
							fmt.Fprintf(os.Stderr, "enrich: %s %s: %v; skipping\n", stage, urn, e)
						},
					}

					enrichResult, _ := enrichNewUC.Execute(cmd.Context(), result.New)

					// Collect the persisted insights and full-detail jobs from cache.
					// GetJob upserts the full detail record (description, apply_url, etc.)
					// into sweepCache, so a cache.Get here returns the enriched shape —
					// not the hollow search-result job.
					enrichedInsights = make(map[string]*domain.Insights, enrichResult.Enriched)
					detailedNewJobs = make([]domain.Job, 0, len(result.New))
					for _, j := range result.New {
						id := string(j.URN())
						// Substitute the full-detail cached job when available; fall back
						// to the original search-result job for cap-skipped/errored jobs.
						if detailed, _, ok := sweepCache.Get(id); ok {
							detailedNewJobs = append(detailedNewJobs, detailed)
						} else {
							detailedNewJobs = append(detailedNewJobs, j)
						}
						if ins, ok := sweepCache.GetInsights(id); ok {
							insCopy := ins
							enrichedInsights[id] = &insCopy
						}
					}

					// Build audit suffix — always explicit, never silent.
					auditSuffix = fmt.Sprintf(" | enriched %d/%d new (cap %d)", enrichResult.Enriched, result.NewCount, cap)
					if enrichResult.Errors > 0 {
						auditSuffix += fmt.Sprintf(", %d error(s)", enrichResult.Errors)
					}
					if enrichResult.SkippedByCap > 0 {
						auditSuffix += fmt.Sprintf(", %d skipped (raise LI_ASSIST_ENRICH_MAX_PER_RUN)", enrichResult.SkippedByCap)
					}
				}
			}

			// --- Output ---

			// Determine what to print to stdout.
			// For enriched runs, use detailedNewJobs (full cache records) for the
			// new slice; seen jobs are appended from the search result as before.
			var newSlice []domain.Job
			if detailedNewJobs != nil {
				newSlice = detailedNewJobs
			} else {
				newSlice = result.New
			}
			jobsToShow := newSlice
			if all {
				jobsToShow = append(newSlice, result.Seen...)
			}

			// Build per-job intros map when --intros is set.
			//
			// The map is always initialised (non-nil) when --intros is requested so
			// the output shape is consistent regardless of whether the connections
			// file loaded (missing file → empty map → no intros key per job, but
			// still the brief+intros shape rather than the plain brief shape). This
			// removes the filesystem-dependent shape flip.
			var introsByURN map[string][]domain.Intro
			if introsFlag {
				introsByURN = make(map[string][]domain.Intro, len(jobsToShow))
				for _, j := range jobsToShow {
					if matches := usecase.MatchIntros(j, conns); len(matches) > 0 {
						introsByURN[string(j.URN())] = matches
					}
				}
			}

			// Output shape rules:
			//   --enrich (±--intros)   → full detail + insights + intros
			//   --intros, no --enrich  → brief shape + intros (no hollow posting{})
			//   plain sweep            → brief shape only (unchanged)
			var b []byte
			switch {
			case enrichedInsights != nil:
				// With enrichment: full detail + insights + intros.
				// When --all is set, seen jobs are included without insights/intros
				// (they use the same detail shape; absent keys are omitted).
				b, err = output.JobsDetailJSONWithIntros(jobsToShow, enrichedInsights, introsByURN)
			case introsByURN != nil:
				// Intros only (no enrich): brief shape to avoid hollow posting fields.
				b, err = output.JobsJSONWithIntros(jobsToShow, introsByURN)
			default:
				b, err = output.JobsJSON(jobsToShow)
			}
			if err != nil {
				return fmt.Errorf("render output: %w", err)
			}
			fmt.Fprintln(os.Stdout, string(b))

			// Always print audit line to stderr (one line, always complete).
			fmt.Fprintf(os.Stderr,
				"sweep: %d new / %d seen / %d excluded (cache: %d jobs)%s\n",
				result.NewCount, result.SeenCount, result.ExcludedCount, sweepCache.Len(),
				auditSuffix,
			)

			return nil
		},
	}

	cmd.Flags().StringVar(&location, "location", "", "Filter by location (e.g. \"Berlin\"); overrides home_location from config")
	cmd.Flags().BoolVar(&anywhere, "anywhere", false, "Search worldwide (no location filter); mutually exclusive with --location")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum number of results to return")
	cmd.Flags().StringArrayVar(&excludeCompanies, "exclude-company", nil, "Company name to exclude (repeatable); also read from ~/.config/li-assist/excluded-companies.txt")
	cmd.Flags().StringArrayVar(&excludeTitles, "exclude-title", nil, "Drop results whose title contains this term (case-insensitive); repeatable")
	cmd.Flags().BoolVar(&all, "all", false, "Print all results (new and seen), not just new ones")
	cmd.Flags().StringVar(&format, "format", "json", "Output format: json")
	cmd.Flags().BoolVar(&enrichFlag, "enrich", false, "Fetch full detail and run LLM analysis for each new job (enrich-once; cached per URN). Cap: LI_ASSIST_ENRICH_MAX_PER_RUN (default 25)")
	cmd.Flags().BoolVar(&introsFlag, "intros", false, "Surface 1st-degree LinkedIn connections at each job's company as warm-intro candidates (offline; requires Connections.csv export)")
	cmd.Flags().StringVar(&connectionsFlag, "connections", "", "Path to LinkedIn Connections.csv export (overrides env LI_ASSIST_CONNECTIONS_CSV and config)")

	return cmd
}
