package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

const (
	// jobsSearchPath is the Voyager REST path for job-card search results.
	jobsSearchPath = "/voyager/api/voyagerJobsDashJobCards"

	// jobsSearchDecorationID is the version-pinned decoration identifier.
	// Drift indicator: if LinkedIn changes the schema the replay test will fail.
	jobsSearchDecorationID = "com.linkedin.voyager.dash.deco.jobs.search.JobSearchCardsCollection-220"

	// jobDetailPath is the Voyager GraphQL path for job posting detail sections.
	jobDetailPath = "/voyager/api/graphql"

	// jobDetailQueryID is the version-pinned GraphQL query identifier for
	// voyagerJobsDashJobPostingDetailSections.
	// Drift indicator: if LinkedIn changes the query hash the replay test will fail.
	jobDetailQueryID = "voyagerJobsDashJobPostingDetailSections.77cb64956921ef397a36de4f7f8bce47"
)

// rawGetter abstracts the in-page fetch transport (Task 8). Keeping the parser
// package decoupled from chromedp lets replay tests inject any []byte source.
type rawGetter interface {
	Get(ctx context.Context, path string, query url.Values) ([]byte, error)
}

// JobSearchParams holds the inputs for a jobs-search request.
type JobSearchParams struct {
	Keywords string
	Location string
	// Start is the zero-based pagination offset (0, 25, 50, ...).
	Start int
	// Count is the page size; LinkedIn returns 25 by default.
	Count int
}

// JobsClient issues jobs-search requests via a rawGetter and returns parsed jobs.
type JobsClient struct {
	getter rawGetter
}

// NewJobsClient constructs a JobsClient backed by the given getter.
func NewJobsClient(g rawGetter) *JobsClient {
	return &JobsClient{getter: g}
}

// Search performs a jobs search and returns parsed domain.Job values.
func (c *JobsClient) Search(ctx context.Context, params JobSearchParams) ([]domain.Job, error) {
	body, err := c.getter.Get(ctx, jobsSearchPath, buildJobsSearchQuery(params))
	if err != nil {
		return nil, fmt.Errorf("voyager jobs search get: %w", err)
	}
	jobs, err := parseJobsSearch(body)
	if err != nil {
		// Wrap with ErrSchema so callers (e.g. doctor) can detect schema drift.
		return nil, fmt.Errorf("voyager jobs search parse (%w): %w", domain.ErrSchema, err)
	}
	return jobs, nil
}

// buildJobsSearchQuery constructs the url.Values for the voyagerJobsDashJobCards endpoint.
//
// The query parameter is a LinkedIn restli tuple string embedded verbatim (not
// URL-encoded key=value pairs). Keywords and location are substituted inline
// (restli-escaped so they cannot break the tuple).
//
// TRANSPORT NOTE (Task 8): the `query` value must be sent with its restli
// structural punctuation -- ( ) : , -- LITERAL on the wire (as the real captured
// request did), and the already-escaped %XX in user values must NOT be
// double-encoded. Do not blindly url.Values.Encode() this param; serialize to
// match the M0 captured URL (the working oracle).
func buildJobsSearchQuery(p JobSearchParams) url.Values {
	count := p.Count
	if count <= 0 {
		count = 25
	}

	// LinkedIn restli tuple format: parenthesised comma-separated key:value pairs.
	// Keywords/location must be restli-escaped so a value containing ',', ')',
	// ':' or '(' cannot break the tuple structure (e.g. "Go, Python", "C++(x)").
	// spellCorrectionEnabled is always true for search.
	// Only include the locationUnion clause when a location is given. An empty
	// location renders as "location:)" which LinkedIn rejects with HTTP 400;
	// omitting the clause performs a location-wide search.
	locationClause := ""
	if strings.TrimSpace(p.Location) != "" {
		locationClause = fmt.Sprintf(",locationUnion:(seoLocation:(location:%s))", restliEscape(p.Location))
	}

	queryTuple := fmt.Sprintf(
		"(origin:JOB_SEARCH_PAGE_OTHER_ENTRY,keywords:%s%s,spellCorrectionEnabled:true)",
		restliEscape(p.Keywords),
		locationClause,
	)

	q := url.Values{}
	q.Set("decorationId", jobsSearchDecorationID)
	q.Set("count", fmt.Sprintf("%d", count))
	q.Set("q", "jobSearch")
	q.Set("start", fmt.Sprintf("%d", p.Start))
	q.Set("query", queryTuple)
	return q
}

// restliEscape percent-encodes the restli structural characters so a user
// keyword or location value cannot break the query tuple. LinkedIn's restli
// protocol reserves '(', ')', ',', and ':' as structure; everything else is
// passed through (url.Values.Encode handles ordinary URL-encoding afterwards).
func restliEscape(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '(', ')', ',', ':':
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// -- JSON decode types -------------------------------------------------------

// jobsSearchData is the "data" field of the jobs-search envelope.
type jobsSearchData struct {
	Elements []jobCard `json:"elements"`
	Paging   struct {
		Count int `json:"count"`
		Start int `json:"start"`
		Total int `json:"total"`
	} `json:"paging"`
}

// jobCard is a single entry in data.elements[].
type jobCard struct {
	JobCardUnion struct {
		// *jobPostingCard is the URN pointer into included[].
		// The JSON key literally contains an asterisk.
		JobPostingCardPtr string `json:"*jobPostingCard"`
	} `json:"jobCardUnion"`
}

// textViewModel holds the ".text" field from LinkedIn's TextViewModel objects.
// Additional rich attributes (attributesV2, textDirection, etc.) are ignored.
type textViewModel struct {
	Text string `json:"text"`
}

// jobPostingCardIncluded is the typed shape we decode from an included entity
// whose entityUrn contains ",JOBS_SEARCH)". JOB_DETAILS stubs have only
// entityUrn; the other fields are absent (null / omitted) and decode to zero.
type jobPostingCardIncluded struct {
	EntityUrn       string          `json:"entityUrn"`
	JobPostingUrn   string          `json:"jobPostingUrn"`
	JobPostingTitle string          `json:"jobPostingTitle"`
	PrimaryDesc     *textViewModel  `json:"primaryDescription"`   // company name
	SecondaryDesc   *textViewModel  `json:"secondaryDescription"` // location
	TertiaryDesc    *textViewModel  `json:"tertiaryDescription"`  // salary (optional)
	FooterItems     []jobFooterItem `json:"footerItems"`
}

// jobFooterItem is one entry in a card's footerItems. The LISTED_DATE entry
// carries timeAt, a millisecond epoch of when the job was posted.
type jobFooterItem struct {
	Type   string `json:"type"`
	TimeAt int64  `json:"timeAt"`
}

// -- Parser ------------------------------------------------------------------

// parseJobsSearch decodes a raw Voyager jobs-search response body and returns
// one domain.Job per JOBS_SEARCH card. JOB_DETAILS prefetch stubs are filtered.
//
// Notes on intentional omissions:
//   - postedAt comes from the LISTED_DATE footer epoch when present, else zero.
//   - Company URN is empty ("") for v0: the card carries company name via
//     primaryDescription.text but no clean fsd_company URN per card.
//   - Posting is empty (no description, applyURL, or applicantCount): those
//     fields require a job-detail fetch (Task 12).
func parseJobsSearch(body []byte) ([]domain.Job, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response body")
	}

	var env voyagerEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}

	// Build the included URN map.
	includedMap := buildIncludedMap(env.Included)

	// Decode data.elements to get the card pointer URNs.
	var data jobsSearchData
	if env.Data == nil {
		return nil, nil
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("unmarshal data: %w", err)
	}

	jobs := make([]domain.Job, 0, len(data.Elements))
	for _, elem := range data.Elements {
		cardURN := elem.JobCardUnion.JobPostingCardPtr
		if cardURN == "" {
			continue
		}

		// Filter to JOBS_SEARCH variant only; skip JOB_DETAILS prefetch stubs.
		if !strings.Contains(cardURN, ",JOBS_SEARCH)") {
			continue
		}

		rawCard, ok := includedMap[cardURN]
		if !ok {
			// Missing from included -- skip gracefully.
			continue
		}

		var card jobPostingCardIncluded
		if err := json.Unmarshal(rawCard, &card); err != nil {
			// Malformed card -- skip and continue.
			continue
		}

		// A usable search card needs both a title and a posting URN; skip if
		// either is missing (JOB_DETAILS stubs and any malformed card).
		if card.JobPostingTitle == "" || card.JobPostingUrn == "" {
			continue
		}

		postingURN, err := domain.ParseURN(card.JobPostingUrn)
		if err != nil {
			// Unparseable URN -- skip.
			continue
		}

		companyName := ""
		if card.PrimaryDesc != nil {
			companyName = card.PrimaryDesc.Text
		}
		location := ""
		if card.SecondaryDesc != nil {
			location = card.SecondaryDesc.Text
		}

		// postedAt from the LISTED_DATE footer epoch (ms). Zero if absent.
		var postedAt time.Time
		for _, fi := range card.FooterItems {
			if fi.Type == "LISTED_DATE" && fi.TimeAt > 0 {
				postedAt = time.UnixMilli(fi.TimeAt).UTC()
				break
			}
		}

		job := domain.NewJob(
			postingURN,
			card.JobPostingTitle,
			location,
			domain.NewCompany("", companyName),
			postedAt,
			domain.NewPosting("", "", 0), // search has no description
		)
		jobs = append(jobs, job)
	}

	return jobs, nil
}

// -- Job Detail --------------------------------------------------------------

// Get fetches the detail for a single job posting and returns a populated
// domain.Job with description, applyURL, company, title, location, and postedAt.
//
// The request uses the voyagerJobsDashJobPostingDetailSections GraphQL endpoint
// with cardSectionTypes TOP_CARD and JOB_DESCRIPTION_CARD.
//
// ASSUMPTION (not yet live-verified): a single combined request for both card
// sections returns all related entities (JobPosting+title, Company,
// JobSeekerApplicationDetail, Geo from TOP_CARD; JobDescription from
// JOB_DESCRIPTION_CARD) in one included[] array. The M0 corpus was captured as
// two SEPARATE single-section responses and merged; we have no capture of a
// live combined request. parseJobDetail tolerates missing entities (every field
// defaults), so a partial response degrades gracefully rather than erroring. If
// a live `jobs get` returns partial data (e.g. missing description), switch to
// two per-section fetches merged client-side -- each single-section request is
// already proven to work.
func (c *JobsClient) Get(ctx context.Context, urn domain.URN) (domain.Job, error) {
	body, err := c.getter.Get(ctx, jobDetailPath, buildJobDetailQuery(urn))
	if err != nil {
		return domain.Job{}, fmt.Errorf("voyager job detail get: %w", err)
	}
	job, err := parseJobDetail(body)
	if err != nil {
		// Wrap with ErrSchema so callers (e.g. doctor) can detect schema drift.
		return domain.Job{}, fmt.Errorf("voyager job detail parse (%w): %w", domain.ErrSchema, err)
	}
	return job, nil
}

// buildJobDetailQuery constructs the url.Values for the job posting detail endpoint.
//
// The variables tuple follows the restli format used in the captured corpus:
//
//	(cardSectionTypes:List(TOP_CARD,JOB_DESCRIPTION_CARD),jobPostingUrn:<urn>,includeSecondaryActionsV2:true)
//
// The jobPostingUrn value is restliEscaped so its colons (':') do not break the
// tuple structure. cardSectionTypes names are LinkedIn enum values and are literal.
func buildJobDetailQuery(urn domain.URN) url.Values {
	// The URN contains ':' which are restli structural chars; escape them.
	escapedURN := restliEscape(string(urn))
	variables := fmt.Sprintf(
		"(cardSectionTypes:List(TOP_CARD,JOB_DESCRIPTION_CARD),jobPostingUrn:%s,includeSecondaryActionsV2:true)",
		escapedURN,
	)
	q := url.Values{}
	q.Set("includeWebMetadata", "true")
	q.Set("variables", variables)
	q.Set("queryId", jobDetailQueryID)
	return q
}

// -- Job Detail JSON types ---------------------------------------------------

// jobDetailEnvelope decodes the outer GraphQL response wrapper.
// Shape: { "data": { "data": { "jobsDashJobPostingDetailSectionsByCardSectionTypes": {...} } }, "included": [...] }
type jobDetailEnvelope struct {
	Data     jobDetailData     `json:"data"`
	Included []json.RawMessage `json:"included"`
}

type jobDetailData struct {
	Data jobDetailInner `json:"data"`
}

type jobDetailInner struct {
	JobsDash jobDetailCollection `json:"jobsDashJobPostingDetailSectionsByCardSectionTypes"`
}

type jobDetailCollection struct {
	Elements []json.RawMessage `json:"elements"`
}

// jobDetailIncludedStub extracts the $type field from each included entry to
// identify which concrete type it is.
type jobDetailIncludedStub struct {
	Type      string `json:"$type"`
	EntityUrn string `json:"entityUrn"`
}

// jobPostingIncluded is the typed shape for com.linkedin.voyager.dash.jobs.JobPosting
// entries in the included array. Carries title and companyDetails reference.
type jobPostingIncluded struct {
	EntityUrn      string `json:"entityUrn"`
	Title          string `json:"title"`
	CompanyDetails struct {
		JobCompany struct {
			CompanyURNPtr string `json:"*company"`
		} `json:"jobCompany"`
	} `json:"companyDetails"`
}

// jobPostingCardIncludedDetail is the detail-endpoint shape for
// com.linkedin.voyager.dash.jobs.JobPostingCard. Unlike the search card it
// carries jobPostingTitle but no footerItems.
type jobPostingCardIncludedDetail struct {
	EntityUrn       string         `json:"entityUrn"`
	JobPostingTitle string         `json:"jobPostingTitle"`
	PrimaryDesc     *textViewModel `json:"primaryDescription"` // company name text
	JobPostingRef   string         `json:"*jobPosting"`        // pointer to fsd_jobPosting URN
}

// jobSeekerApplicationDetailIncluded carries the company-apply URL.
type jobSeekerApplicationDetailIncluded struct {
	EntityUrn       string `json:"entityUrn"`
	CompanyApplyURL string `json:"companyApplyUrl"`
}

// jobDescriptionIncluded carries the full description text and postedOnText.
type jobDescriptionIncluded struct {
	EntityUrn       string         `json:"entityUrn"`
	DescriptionText *textViewModel `json:"descriptionText"`
	PostedOnText    string         `json:"postedOnText"`
}

// companyIncluded carries the company name.
type companyIncluded struct {
	EntityUrn string `json:"entityUrn"`
	Name      string `json:"name"`
}

// geoIncluded carries the location string.
type geoIncluded struct {
	EntityUrn            string `json:"entityUrn"`
	DefaultLocalizedName string `json:"defaultLocalizedName"`
}

// -- parseJobDetail ----------------------------------------------------------

// parseJobDetail decodes a raw Voyager job-detail GraphQL response body and
// returns a populated domain.Job.
//
// Field mapping from corpus (internal/voyager/testdata/voyager/job-detail/response.json):
//
//	title        -- included[JobPosting].title (preferred)
//	             -- or included[JobPostingCard].jobPostingTitle (fallback)
//	companyURN   -- included[JobPosting].companyDetails.jobCompany.*company
//	companyName  -- included[Company].name (resolved via companyURN)
//	applyURL     -- included[JobSeekerApplicationDetail].companyApplyUrl
//	description  -- included[JobDescription].descriptionText.text
//	postedOnText -- included[JobDescription].postedOnText ("Posted on Jun 10, 2026.")
//	location     -- included[Geo].defaultLocalizedName
//	urn          -- included[JobPosting].entityUrn
//
// All nested map/pointer accesses are guarded; missing fields produce zero values.
// postedAt is left as zero time: the "Posted on Jun 10, 2026." string is
// locale-dependent and not reliably parseable without a fixed timezone assumption.
func parseJobDetail(body []byte) (domain.Job, error) {
	if len(body) == 0 {
		return domain.Job{}, fmt.Errorf("empty response body")
	}

	var env jobDetailEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return domain.Job{}, fmt.Errorf("unmarshal job detail envelope: %w", err)
	}

	// Walk the included array to collect the typed entities we need.
	var (
		jobPosting   *jobPostingIncluded
		jobCard      *jobPostingCardIncludedDetail
		appDetail    *jobSeekerApplicationDetailIncluded
		jobDesc      *jobDescriptionIncluded
		companyByURN = map[string]companyIncluded{}
		geo          *geoIncluded
	)

	for _, raw := range env.Included {
		var stub jobDetailIncludedStub
		if err := json.Unmarshal(raw, &stub); err != nil {
			continue
		}

		switch {
		case strings.HasSuffix(stub.Type, ".jobs.JobPosting"):
			var jp jobPostingIncluded
			if err := json.Unmarshal(raw, &jp); err == nil && jp.EntityUrn != "" {
				jobPosting = &jp
			}

		case strings.HasSuffix(stub.Type, ".jobs.JobPostingCard"):
			var jc jobPostingCardIncludedDetail
			if err := json.Unmarshal(raw, &jc); err == nil && jc.EntityUrn != "" {
				jobCard = &jc
			}

		case strings.HasSuffix(stub.Type, ".jobs.JobSeekerApplicationDetail"):
			var ad jobSeekerApplicationDetailIncluded
			if err := json.Unmarshal(raw, &ad); err == nil && ad.EntityUrn != "" {
				appDetail = &ad
			}

		case strings.HasSuffix(stub.Type, ".jobs.JobDescription"):
			var jd jobDescriptionIncluded
			if err := json.Unmarshal(raw, &jd); err == nil && jd.EntityUrn != "" {
				jobDesc = &jd
			}

		case strings.HasSuffix(stub.Type, ".organization.Company"):
			var co companyIncluded
			if err := json.Unmarshal(raw, &co); err == nil && co.EntityUrn != "" {
				companyByURN[co.EntityUrn] = co
			}

		case strings.HasSuffix(stub.Type, ".common.Geo"):
			var g geoIncluded
			if err := json.Unmarshal(raw, &g); err == nil && g.EntityUrn != "" {
				geo = &g
			}
		}
	}

	// Derive URN: prefer the concrete fsd_jobPosting URN from JobPosting, fall back
	// to the pointer in JobPostingCard.
	urnStr := ""
	if jobPosting != nil && jobPosting.EntityUrn != "" {
		urnStr = jobPosting.EntityUrn
	} else if jobCard != nil && jobCard.JobPostingRef != "" {
		urnStr = jobCard.JobPostingRef
	}
	if urnStr == "" {
		return domain.Job{}, fmt.Errorf("job detail: could not determine jobPosting URN from response")
	}
	postingURN, err := domain.ParseURN(urnStr)
	if err != nil {
		return domain.Job{}, fmt.Errorf("job detail: invalid jobPosting URN %q: %w", urnStr, err)
	}

	// Derive title.
	title := ""
	if jobPosting != nil && jobPosting.Title != "" {
		title = jobPosting.Title
	} else if jobCard != nil && jobCard.JobPostingTitle != "" {
		title = jobCard.JobPostingTitle
	}

	// Derive company URN + name.
	companyURNStr := ""
	if jobPosting != nil {
		companyURNStr = jobPosting.CompanyDetails.JobCompany.CompanyURNPtr
	}
	companyName := ""
	companyURN := domain.URN("")
	if companyURNStr != "" {
		if co, ok := companyByURN[companyURNStr]; ok {
			companyName = co.Name
			if u, parseErr := domain.ParseURN(companyURNStr); parseErr == nil {
				companyURN = u
			}
		}
	}

	// Derive location.
	location := ""
	if geo != nil {
		location = geo.DefaultLocalizedName
	}

	// Derive description.
	description := ""
	if jobDesc != nil && jobDesc.DescriptionText != nil {
		description = jobDesc.DescriptionText.Text
	}

	// Derive apply URL.
	applyURL := ""
	if appDetail != nil {
		applyURL = appDetail.CompanyApplyURL
	}

	// postedAt: the captured corpus carries "postedOnText" as a human-readable
	// string like "Posted on Jun 10, 2026." which is locale-dependent and not
	// reliably parseable. We leave postedAt as zero time. The JSON output omits
	// posted_at when zero.
	var postedAt time.Time

	job := domain.NewJob(
		postingURN,
		title,
		location,
		domain.NewCompany(companyURN, companyName),
		postedAt,
		domain.NewPosting(description, applyURL, 0),
	)
	return job, nil
}
