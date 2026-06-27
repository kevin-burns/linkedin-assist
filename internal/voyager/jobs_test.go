package voyager

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// TestParseJobsSearch_Replay reads the committed corpus and asserts the full mapping.
func TestParseJobsSearch_Replay(t *testing.T) {
	body, err := os.ReadFile("testdata/voyager/jobs-search/response.json")
	if err != nil {
		t.Fatalf("failed to read corpus: %v", err)
	}

	jobs, err := parseJobsSearch(body)
	if err != nil {
		t.Fatalf("parseJobsSearch returned error: %v", err)
	}

	// Corpus has paging.count=25 JOBS_SEARCH cards; JOB_DETAILS stubs must be filtered out.
	if len(jobs) != 25 {
		t.Errorf("want 25 jobs, got %d", len(jobs))
	}

	// Every job must have non-empty URN and Title.
	for i, j := range jobs {
		if j.URN() == "" {
			t.Errorf("job[%d] has empty URN", i)
		}
		if j.Title() == "" {
			t.Errorf("job[%d] has empty Title", i)
		}
	}

	// At least one job has a non-empty Company name.
	hasCompany := false
	for _, j := range jobs {
		if j.Company().Name() != "" {
			hasCompany = true
			break
		}
	}
	if !hasCompany {
		t.Error("expected at least one job with a non-empty company name")
	}

	// At least one job has a non-empty Location.
	hasLocation := false
	for _, j := range jobs {
		if j.Location() != "" {
			hasLocation = true
			break
		}
	}
	if !hasLocation {
		t.Error("expected at least one job with a non-empty location")
	}

	// Spot-check: find the Axel Springer / AWS Cloud Platform Engineer card
	// (urn:li:fsd_jobPosting:4426968077, known from corpus).
	var found *struct {
		title    string
		company  string
		location string
	}
	for _, j := range jobs {
		if j.Company().Name() == "Axel Springer" {
			found = &struct {
				title    string
				company  string
				location string
			}{
				title:    j.Title(),
				company:  j.Company().Name(),
				location: j.Location(),
			}
			break
		}
	}
	if found == nil {
		t.Fatal("spot-check: could not find Axel Springer job")
	}
	if !strings.Contains(found.title, "Cloud") {
		t.Errorf("spot-check: expected title to contain 'Cloud', got %q", found.title)
	}
	if !strings.Contains(found.location, "Berlin") {
		t.Errorf("spot-check: expected location to contain 'Berlin', got %q", found.location)
	}
	// URN must be the concrete job posting URN, not the compound card URN.
	for _, j := range jobs {
		if j.Company().Name() == "Axel Springer" {
			if string(j.URN()) != "urn:li:fsd_jobPosting:4426968077" {
				t.Errorf("spot-check: want URN urn:li:fsd_jobPosting:4426968077, got %q", j.URN())
			}
		}
	}

	// postedAt is pinned from the LISTED_DATE footer epoch: every card in the
	// corpus carries one, so every parsed job must have a non-zero postedAt.
	for _, j := range jobs {
		if j.PostedAt().IsZero() {
			t.Errorf("expected non-zero PostedAt for %q (LISTED_DATE epoch), got zero", j.Title())
		}
	}

	// Log a sample for orchestrator eyeballing.
	t.Logf("parsed %d jobs", len(jobs))
	for i, j := range jobs {
		if i >= 3 {
			break
		}
		t.Logf("job[%d]: urn=%s title=%q company=%q location=%q",
			i, j.URN(), j.Title(), j.Company().Name(), j.Location())
	}
}

// TestParseJobsSearch_EmptyBody returns empty slice, no panic.
func TestParseJobsSearch_EmptyBody(t *testing.T) {
	jobs, err := parseJobsSearch([]byte("{}"))
	if err != nil {
		t.Fatalf("unexpected error on empty body: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("want 0 jobs, got %d", len(jobs))
	}
}

// TestParseJobsSearch_MalformedJSON returns an error for invalid JSON.
func TestParseJobsSearch_MalformedJSON(t *testing.T) {
	_, err := parseJobsSearch([]byte("{not valid json"))
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

// TestBuildJobsSearchQuery asserts that the generated query string has the
// required parameters for the voyagerJobsDashJobCards endpoint.
func TestBuildJobsSearchQuery(t *testing.T) {
	params := JobSearchParams{
		Keywords: "platform engineer",
		Location: "Berlin",
		Start:    0,
		Count:    25,
	}

	q := buildJobsSearchQuery(params)

	tests := []struct {
		key  string
		want string
	}{
		{"q", "jobSearch"},
		{"decorationId", "com.linkedin.voyager.dash.deco.jobs.search.JobSearchCardsCollection-220"},
		{"count", "25"},
		{"start", "0"},
	}
	for _, tc := range tests {
		got := q.Get(tc.key)
		if got != tc.want {
			t.Errorf("query param %q: want %q, got %q", tc.key, tc.want, got)
		}
	}

	// The query tuple must contain keywords and location.
	queryTuple := q.Get("query")
	if !strings.Contains(queryTuple, "platform engineer") {
		t.Errorf("query tuple missing keywords: %q", queryTuple)
	}
	if !strings.Contains(queryTuple, "Berlin") {
		t.Errorf("query tuple missing location: %q", queryTuple)
	}
	if !strings.Contains(queryTuple, "JOB_SEARCH") {
		t.Errorf("query tuple missing origin JOB_SEARCH: %q", queryTuple)
	}

	t.Logf("query string: %s", q.Encode())
}

// TestBuildJobsSearchQuery_locationClause verifies the locationUnion clause is
// included only when a location is given. An empty location previously rendered
// as "location:)" and LinkedIn rejected the request with HTTP 400.
func TestBuildJobsSearchQuery_locationClause(t *testing.T) {
	withLoc := buildJobsSearchQuery(JobSearchParams{Keywords: "go", Location: "Berlin"}).Get("query")
	if !strings.Contains(withLoc, "locationUnion:(seoLocation:(location:Berlin))") {
		t.Errorf("with location: want locationUnion clause, got %q", withLoc)
	}

	for _, loc := range []string{"", "   "} {
		got := buildJobsSearchQuery(JobSearchParams{Keywords: "go", Location: loc}).Get("query")
		if strings.Contains(got, "locationUnion") || strings.Contains(got, "location:") {
			t.Errorf("empty location %q: want NO location clause, got %q", loc, got)
		}
		if !strings.Contains(got, "keywords:go") || !strings.Contains(got, "spellCorrectionEnabled:true") {
			t.Errorf("empty location %q: malformed tuple %q", loc, got)
		}
	}
}

// TestBuildJobsSearchQuery_escapesRestliChars ensures keyword/location values
// containing restli structural characters cannot break the query tuple.
func TestBuildJobsSearchQuery_escapesRestliChars(t *testing.T) {
	q := buildJobsSearchQuery(JobSearchParams{
		Keywords: "Go, Python (senior)",
		Location: "New York, NY",
	})
	tuple := q.Get("query")
	// Raw structural chars from user input must not appear unescaped: there must
	// be exactly the structural punctuation the template itself contributes.
	// The escaped forms (%2C %28 %29) carry the user's literal chars.
	for _, frag := range []string{"%2C", "%28", "%29"} {
		if !strings.Contains(tuple, frag) {
			t.Errorf("expected escaped fragment %s in tuple: %q", frag, tuple)
		}
	}
	// The tuple must still be well-formed: balanced parens from the template only.
	if strings.Count(tuple, "(") != strings.Count(tuple, ")") {
		t.Errorf("unbalanced parens after escaping: %q", tuple)
	}
	// Default count when unset.
	if got := q.Get("count"); got != "25" {
		t.Errorf("default count: want 25, got %q", got)
	}
}

// TestBuildJobsSearchQuery_Pagination checks that start/count propagate.
func TestBuildJobsSearchQuery_Pagination(t *testing.T) {
	params := JobSearchParams{
		Keywords: "engineer",
		Location: "Berlin",
		Start:    50,
		Count:    25,
	}
	q := buildJobsSearchQuery(params)
	if q.Get("start") != "50" {
		t.Errorf("want start=50, got %q", q.Get("start"))
	}
}

// stubGetter is a minimal rawGetter that returns pre-set bytes, used to test
// JobsClient.Search without a real browser.
type stubGetter struct {
	body []byte
	err  error
	// captures the last path and query passed to Get.
	lastPath  string
	lastQuery url.Values
}

func (s *stubGetter) Get(_ context.Context, path string, query url.Values) ([]byte, error) {
	s.lastPath = path
	s.lastQuery = query
	return s.body, s.err
}

// TestJobsClientSearch_Integration wires a stub getter with the real corpus bytes
// and asserts that Search delegates to parseJobsSearch correctly.
func TestJobsClientSearch_Integration(t *testing.T) {
	body, err := os.ReadFile("testdata/voyager/jobs-search/response.json")
	if err != nil {
		t.Fatalf("failed to read corpus: %v", err)
	}

	getter := &stubGetter{body: body}
	client := NewJobsClient(getter)

	jobs, err := client.Search(context.Background(), JobSearchParams{
		Keywords: "engineer",
		Location: "Berlin",
		Start:    0,
		Count:    25,
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(jobs) != 25 {
		t.Errorf("want 25 jobs, got %d", len(jobs))
	}

	// Verify the path the getter received.
	if getter.lastPath != jobsSearchPath {
		t.Errorf("want path %q, got %q", jobsSearchPath, getter.lastPath)
	}
	// Verify query params forwarded.
	if getter.lastQuery.Get("q") != "jobSearch" {
		t.Errorf("want q=jobSearch, got %q", getter.lastQuery.Get("q"))
	}
}

// -- Job Detail Tests --------------------------------------------------------

// TestParseJobDetail_Replay reads the committed job-detail corpus and asserts
// the full field mapping. Pinned to real captured data: a field rename breaks it.
//
// Corpus: internal/voyager/testdata/voyager/job-detail/response.json
// (merged from capture files 19 + 15 for jobPostingUrn:4427011736)
func TestParseJobDetail_Replay(t *testing.T) {
	body, err := os.ReadFile("testdata/voyager/job-detail/response.json")
	if err != nil {
		t.Fatalf("failed to read corpus: %v", err)
	}

	job, err := parseJobDetail(body)
	if err != nil {
		t.Fatalf("parseJobDetail returned error: %v", err)
	}

	// URN must be the concrete fsd_jobPosting URN.
	const wantURN = "urn:li:fsd_jobPosting:4427011736"
	if string(job.URN()) == "" {
		t.Fatal("URN is empty")
	}
	if string(job.URN()) != wantURN {
		t.Errorf("URN = %q; want %q", job.URN(), wantURN)
	}

	// Title must be non-empty and contain "Cloud Platform Engineer".
	if job.Title() == "" {
		t.Fatal("Title is empty")
	}
	if !strings.Contains(job.Title(), "Cloud Platform Engineer") {
		t.Errorf("Title = %q; want it to contain %q", job.Title(), "Cloud Platform Engineer")
	}

	// Description must be non-empty and contain a stable ASCII-safe substring
	// from the German DevOps job description (captured from real response).
	if job.Posting().Description() == "" {
		t.Fatal("Description is empty")
	}
	// Stable substring from the real corpus: MaibornWolff company name appears
	// in the description body.
	if !strings.Contains(job.Posting().Description(), "MaibornWolff") {
		t.Errorf("Description does not contain expected corpus substring %q; got: %q",
			"MaibornWolff", job.Posting().Description()[:min(80, len(job.Posting().Description()))])
	}

	// Apply URL must be non-empty and point to personio (from corpus).
	if job.Posting().ApplyURL() == "" {
		t.Fatal("ApplyURL is empty")
	}
	if !strings.Contains(job.Posting().ApplyURL(), "personio") {
		t.Errorf("ApplyURL = %q; expected it to contain %q", job.Posting().ApplyURL(), "personio")
	}

	// Company name must be non-empty.
	if job.Company().Name() == "" {
		t.Fatal("Company name is empty")
	}
	if job.Company().Name() != "MaibornWolff GmbH" {
		t.Errorf("Company name = %q; want %q", job.Company().Name(), "MaibornWolff GmbH")
	}

	// Company URN must be non-empty.
	if job.Company().URN() == "" {
		t.Fatal("Company URN is empty")
	}

	// Location must be non-empty and contain "Berlin".
	if job.Location() == "" {
		t.Fatal("Location is empty")
	}
	if !strings.Contains(job.Location(), "Berlin") {
		t.Errorf("Location = %q; want it to contain %q", job.Location(), "Berlin")
	}

	// Log the parsed job for orchestrator eyeballing.
	descSnippet := job.Posting().Description()
	if len(descSnippet) > 80 {
		descSnippet = descSnippet[:80]
	}
	t.Logf("parsed job: urn=%s title=%q company=%q location=%q applyURL=%q desc[:80]=%q",
		job.URN(), job.Title(), job.Company().Name(), job.Location(),
		job.Posting().ApplyURL(), descSnippet)
}

// TestParseJobDetail_EmptyBody returns an error for empty input.
func TestParseJobDetail_EmptyBody(t *testing.T) {
	_, err := parseJobDetail([]byte{})
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}

// TestParseJobDetail_MalformedJSON returns an error for invalid JSON.
func TestParseJobDetail_MalformedJSON(t *testing.T) {
	_, err := parseJobDetail([]byte("{not valid json"))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// TestParseJobDetail_EmptyIncluded returns an error (no URN determinable).
func TestParseJobDetail_EmptyIncluded(t *testing.T) {
	body := []byte(`{"data":{"data":{"jobsDashJobPostingDetailSectionsByCardSectionTypes":{"elements":[]}}},"included":[]}`)
	_, err := parseJobDetail(body)
	if err == nil {
		t.Fatal("expected error when included is empty (no URN), got nil")
	}
}

// TestBuildJobDetailQuery asserts that the generated query values contain the
// required fields for the voyagerJobsDashJobPostingDetailSections endpoint.
func TestBuildJobDetailQuery(t *testing.T) {
	urn := "urn:li:fsd_jobPosting:4427011736"
	parsedURN, err := domain.ParseURN(urn)
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}

	q := buildJobDetailQuery(parsedURN)

	// queryId must be the pinned value.
	if q.Get("queryId") != jobDetailQueryID {
		t.Errorf("queryId = %q; want %q", q.Get("queryId"), jobDetailQueryID)
	}

	// includeWebMetadata must be present.
	if q.Get("includeWebMetadata") != "true" {
		t.Errorf("includeWebMetadata = %q; want %q", q.Get("includeWebMetadata"), "true")
	}

	// variables must contain jobPostingUrn (restliEscaped: colons become %3A).
	variables := q.Get("variables")
	if variables == "" {
		t.Fatal("variables param is empty")
	}
	if !strings.Contains(variables, "jobPostingUrn") {
		t.Errorf("variables missing jobPostingUrn: %q", variables)
	}
	// URN colons are escaped (%3A) so they don't break the restli tuple.
	if !strings.Contains(variables, "%3A") {
		t.Errorf("variables: URN colons should be escaped as %%3A: %q", variables)
	}
	// cardSectionTypes must contain TOP_CARD and JOB_DESCRIPTION_CARD.
	if !strings.Contains(variables, "TOP_CARD") {
		t.Errorf("variables missing TOP_CARD in cardSectionTypes: %q", variables)
	}
	if !strings.Contains(variables, "JOB_DESCRIPTION_CARD") {
		t.Errorf("variables missing JOB_DESCRIPTION_CARD in cardSectionTypes: %q", variables)
	}

	t.Logf("variables: %s", variables)
}

// TestJobsClientGet_Integration wires a stub getter with the corpus bytes and
// asserts that Get delegates to parseJobDetail correctly.
func TestJobsClientGet_Integration(t *testing.T) {
	body, err := os.ReadFile("testdata/voyager/job-detail/response.json")
	if err != nil {
		t.Fatalf("failed to read corpus: %v", err)
	}

	getter := &stubGetter{body: body}
	client := NewJobsClient(getter)

	urn, err := domain.ParseURN("urn:li:fsd_jobPosting:4427011736")
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}

	job, err := client.Get(context.Background(), urn)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	// Verify the path the getter received.
	if getter.lastPath != jobDetailPath {
		t.Errorf("want path %q, got %q", jobDetailPath, getter.lastPath)
	}
	// Verify queryId was forwarded.
	if getter.lastQuery.Get("queryId") != jobDetailQueryID {
		t.Errorf("want queryId %q, got %q", jobDetailQueryID, getter.lastQuery.Get("queryId"))
	}

	// Basic job assertions.
	if job.URN() == "" {
		t.Error("job URN is empty")
	}
	if job.Title() == "" {
		t.Error("job title is empty")
	}
}
