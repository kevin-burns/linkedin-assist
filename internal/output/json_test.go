package output_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/output"
)

func mustURN(t *testing.T, s string) domain.URN {
	t.Helper()
	u, err := domain.ParseURN(s)
	if err != nil {
		t.Fatalf("ParseURN(%q): %v", s, err)
	}
	return u
}

func TestJobsJSON_ValidJSON(t *testing.T) {
	jobs := []domain.Job{
		domain.NewJob(
			mustURN(t, "urn:li:job:3001"),
			"Go Engineer",
			"London, UK",
			domain.NewCompany(mustURN(t, "urn:li:company:42"), "TechCo"),
			time.Date(2025, 5, 15, 10, 30, 0, 0, time.UTC),
			domain.NewPosting("", "", 0),
		),
	}

	b, err := output.JobsJSON(jobs)
	if err != nil {
		t.Fatalf("JobsJSON returned error: %v", err)
	}

	// Must parse as valid JSON array.
	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("output is not valid JSON array: %v\nbytes: %s", err, b)
	}
	if len(arr) != 1 {
		t.Fatalf("array len = %d; want 1", len(arr))
	}
}

func TestJobsJSON_FieldNames(t *testing.T) {
	jobs := []domain.Job{
		domain.NewJob(
			mustURN(t, "urn:li:job:3002"),
			"Staff Engineer",
			"Remote",
			domain.NewCompany(mustURN(t, "urn:li:company:99"), "Widgets Inc"),
			time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
			domain.NewPosting("", "", 0),
		),
	}

	b, err := output.JobsJSON(jobs)
	if err != nil {
		t.Fatalf("JobsJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rec := arr[0]

	checkString(t, rec, "urn", "urn:li:job:3002")
	checkString(t, rec, "title", "Staff Engineer")
	checkString(t, rec, "location", "Remote")

	compRaw, ok := rec["company"]
	if !ok {
		t.Fatal("missing 'company' field")
	}
	comp, ok := compRaw.(map[string]any)
	if !ok {
		t.Fatalf("company field is not an object, got %T", compRaw)
	}
	checkString(t, comp, "urn", "urn:li:company:99")
	checkString(t, comp, "name", "Widgets Inc")
}

func TestJobsJSON_PostedAtRFC3339(t *testing.T) {
	postedAt := time.Date(2025, 3, 20, 14, 0, 0, 0, time.UTC)
	jobs := []domain.Job{
		domain.NewJob(
			mustURN(t, "urn:li:job:3003"),
			"DevOps Engineer",
			"Seattle, WA",
			domain.NewCompany("", "CloudCo"),
			postedAt,
			domain.NewPosting("", "", 0),
		),
	}

	b, err := output.JobsJSON(jobs)
	if err != nil {
		t.Fatalf("JobsJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	raw, ok := arr[0]["posted_at"]
	if !ok {
		t.Fatal("missing 'posted_at' field")
	}
	s, ok := raw.(string)
	if !ok {
		t.Fatalf("posted_at is not a string, got %T", raw)
	}

	// Must be parseable as RFC3339 and round-trip to the same time.
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("posted_at %q not valid RFC3339: %v", s, err)
	}
	if !parsed.Equal(postedAt) {
		t.Errorf("posted_at round-trip = %v; want %v", parsed, postedAt)
	}
}

func TestJobsJSON_ZeroPostedAtOmitted(t *testing.T) {
	jobs := []domain.Job{
		domain.NewJob(
			mustURN(t, "urn:li:job:3004"),
			"Analyst",
			"Chicago, IL",
			domain.NewCompany("", "FinCo"),
			time.Time{}, // zero time -- should be omitted
			domain.NewPosting("", "", 0),
		),
	}

	b, err := output.JobsJSON(jobs)
	if err != nil {
		t.Fatalf("JobsJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, present := arr[0]["posted_at"]; present {
		t.Error("posted_at should be omitted when zero, but it is present")
	}
}

func TestJobsJSON_EmptySlice(t *testing.T) {
	b, err := output.JobsJSON([]domain.Job{})
	if err != nil {
		t.Fatalf("JobsJSON returned error: %v", err)
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("array len = %d; want 0", len(arr))
	}
}

func TestJobsOKF_NotImplemented(t *testing.T) {
	b, err := output.JobsOKF([]domain.Job{})
	if err == nil {
		t.Fatal("expected error from JobsOKF, got nil")
	}
	if b != nil {
		t.Errorf("expected nil bytes from JobsOKF, got %v", b)
	}
}

// checkString asserts that m[key] is a string equal to want.
func checkString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Errorf("missing field %q", key)
		return
	}
	s, ok := raw.(string)
	if !ok {
		t.Errorf("field %q is %T, want string", key, raw)
		return
	}
	if s != want {
		t.Errorf("field %q = %q; want %q", key, s, want)
	}
}

// -- JobJSON tests -----------------------------------------------------------

func TestJobJSON_ValidJSON(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:4427011736")
	co := mustURN(t, "urn:li:fsd_company:8738045")
	job := domain.NewJob(
		urn,
		"Senior Cloud Platform Engineer",
		"Berlin, Germany",
		domain.NewCompany(co, "MaibornWolff GmbH"),
		time.Time{}, // zero -- postedAt omitted
		domain.NewPosting("DevOps description text...", "https://example.com/apply", 0),
	)

	b, err := output.JobJSON(job)
	if err != nil {
		t.Fatalf("JobJSON returned error: %v", err)
	}

	// Must be valid JSON object.
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("output is not valid JSON object: %v\nbytes: %s", err, b)
	}
}

func TestJobJSON_FieldNames(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:4427011736")
	co := mustURN(t, "urn:li:fsd_company:8738045")
	job := domain.NewJob(
		urn,
		"Senior Cloud Platform Engineer",
		"Berlin, Germany",
		domain.NewCompany(co, "MaibornWolff GmbH"),
		time.Time{},
		domain.NewPosting("DevOps description", "https://example.com/apply", 19),
	)

	b, err := output.JobJSON(job)
	if err != nil {
		t.Fatalf("JobJSON returned error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	checkString(t, obj, "urn", "urn:li:fsd_jobPosting:4427011736")
	checkString(t, obj, "title", "Senior Cloud Platform Engineer")
	checkString(t, obj, "location", "Berlin, Germany")

	compRaw, ok := obj["company"]
	if !ok {
		t.Fatal("missing 'company' field")
	}
	comp, ok := compRaw.(map[string]any)
	if !ok {
		t.Fatalf("company is not an object, got %T", compRaw)
	}
	checkString(t, comp, "urn", "urn:li:fsd_company:8738045")
	checkString(t, comp, "name", "MaibornWolff GmbH")

	postingRaw, ok := obj["posting"]
	if !ok {
		t.Fatal("missing 'posting' field")
	}
	posting, ok := postingRaw.(map[string]any)
	if !ok {
		t.Fatalf("posting is not an object, got %T", postingRaw)
	}
	checkString(t, posting, "description", "DevOps description")
	checkString(t, posting, "apply_url", "https://example.com/apply")
	if v, ok := posting["applicant_count"]; !ok {
		t.Error("missing 'applicant_count' field")
	} else if count, ok := v.(float64); !ok || int(count) != 19 {
		t.Errorf("applicant_count = %v; want 19", v)
	}
}

func TestJobJSON_ZeroPostedAtOmitted(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:999")
	job := domain.NewJob(
		urn,
		"Test Job",
		"Test Location",
		domain.NewCompany("", "Test Co"),
		time.Time{}, // zero -- should be omitted
		domain.NewPosting("desc", "https://example.com", 0),
	)

	b, err := output.JobJSON(job)
	if err != nil {
		t.Fatalf("JobJSON returned error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := obj["posted_at"]; present {
		t.Error("posted_at should be omitted when zero, but it is present")
	}
}

func TestJobJSON_PostedAtRFC3339(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:998")
	postedAt := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	job := domain.NewJob(
		urn,
		"Test Job",
		"Test Location",
		domain.NewCompany("", "Test Co"),
		postedAt,
		domain.NewPosting("desc", "https://example.com", 0),
	)

	b, err := output.JobJSON(job)
	if err != nil {
		t.Fatalf("JobJSON returned error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	raw, ok := obj["posted_at"]
	if !ok {
		t.Fatal("missing 'posted_at' field")
	}
	s, ok := raw.(string)
	if !ok {
		t.Fatalf("posted_at is not a string, got %T", raw)
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("posted_at %q not valid RFC3339: %v", s, err)
	}
	if !parsed.Equal(postedAt) {
		t.Errorf("posted_at round-trip = %v; want %v", parsed, postedAt)
	}
}

// -- JobsDetailJSON tests ----------------------------------------------------

func makeDetailJob(t *testing.T, urnStr, title, desc, applyURL string, applicants int, postedAt time.Time) domain.Job {
	t.Helper()
	urn := mustURN(t, urnStr)
	return domain.NewJob(
		urn,
		title,
		"Remote",
		domain.NewCompany(mustURN(t, "urn:li:company:1"), "Acme"),
		postedAt,
		domain.NewPosting(desc, applyURL, applicants),
	)
}

// TestJobsDetailJSON_WithInsights: a job that has insights in the map produces
// an "insights" key in the output.
func TestJobsDetailJSON_WithInsights(t *testing.T) {
	job := makeDetailJob(t,
		"urn:li:fsd_jobPosting:6001", "Staff Engineer",
		"Build things.", "https://example.com/apply", 42,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	ins := &domain.Insights{
		RealSummary: "Great role",
		TopSkills:   []string{"Go", "K8s"},
	}
	insights := map[string]*domain.Insights{
		string(job.URN()): ins,
	}

	b, err := output.JobsDetailJSON([]domain.Job{job}, insights)
	if err != nil {
		t.Fatalf("JobsDetailJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("array len = %d; want 1", len(arr))
	}
	rec := arr[0]

	if _, ok := rec["insights"]; !ok {
		t.Error("insights key must be present when insights are provided")
	}
}

// TestJobsDetailJSON_NilInsights: when insights map is nil, the "insights" key
// is omitted from all records.
func TestJobsDetailJSON_NilInsights(t *testing.T) {
	job := makeDetailJob(t,
		"urn:li:fsd_jobPosting:6002", "SRE",
		"Reliability work.", "https://example.com/sre", 10,
		time.Time{},
	)

	b, err := output.JobsDetailJSON([]domain.Job{job}, nil)
	if err != nil {
		t.Fatalf("JobsDetailJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("array len = %d; want 1", len(arr))
	}
	if _, ok := arr[0]["insights"]; ok {
		t.Error("insights key must be omitted when insights map is nil")
	}
}

// TestJobsDetailJSON_MixedNewAndSeen: new jobs have insights, seen jobs do not.
// Simulates the --enrich --all output where seen jobs carry no insights key.
func TestJobsDetailJSON_MixedNewAndSeen(t *testing.T) {
	newJob := makeDetailJob(t,
		"urn:li:fsd_jobPosting:6003", "New Job",
		"Fresh posting.", "https://example.com/new", 5,
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	)
	seenJob := makeDetailJob(t,
		"urn:li:fsd_jobPosting:6004", "Seen Job",
		"Already in cache.", "", 0,
		time.Time{},
	)
	ins := &domain.Insights{RealSummary: "interesting"}
	insights := map[string]*domain.Insights{
		string(newJob.URN()): ins,
		// seenJob intentionally omitted → nil → insights key absent
	}

	b, err := output.JobsDetailJSON([]domain.Job{newJob, seenJob}, insights)
	if err != nil {
		t.Fatalf("JobsDetailJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("array len = %d; want 2", len(arr))
	}

	newRec := arr[0]
	seenRec := arr[1]

	if _, ok := newRec["insights"]; !ok {
		t.Error("newJob must have 'insights' key")
	}
	if _, ok := seenRec["insights"]; ok {
		t.Error("seenJob must NOT have 'insights' key (nil in map)")
	}
}

// TestJobsDetailJSON_PostingSubfields: description, apply_url, applicant_count,
// and posted_at omitempty all serialize correctly.
func TestJobsDetailJSON_PostingSubfields(t *testing.T) {
	postedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	job := makeDetailJob(t,
		"urn:li:fsd_jobPosting:6005", "Platform Lead",
		"Drive platform strategy.", "https://example.com/pl", 99,
		postedAt,
	)

	b, err := output.JobsDetailJSON([]domain.Job{job}, nil)
	if err != nil {
		t.Fatalf("JobsDetailJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rec := arr[0]

	// posted_at must be present and RFC3339.
	rawAt, ok := rec["posted_at"]
	if !ok {
		t.Fatal("missing 'posted_at'")
	}
	s, ok := rawAt.(string)
	if !ok {
		t.Fatalf("posted_at is %T, want string", rawAt)
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("posted_at %q not RFC3339: %v", s, err)
	}
	if !parsed.Equal(postedAt) {
		t.Errorf("posted_at round-trip = %v; want %v", parsed, postedAt)
	}

	// posting sub-fields.
	postingRaw, ok := rec["posting"]
	if !ok {
		t.Fatal("missing 'posting' field")
	}
	posting, ok := postingRaw.(map[string]any)
	if !ok {
		t.Fatalf("posting is %T, want object", postingRaw)
	}
	checkString(t, posting, "description", "Drive platform strategy.")
	checkString(t, posting, "apply_url", "https://example.com/pl")
	if v, ok := posting["applicant_count"]; !ok {
		t.Error("missing 'applicant_count'")
	} else if count, ok := v.(float64); !ok || int(count) != 99 {
		t.Errorf("applicant_count = %v; want 99", v)
	}
}

// TestJobsDetailJSON_ZeroPostedAtOmitted: a zero posted_at must be omitted.
func TestJobsDetailJSON_ZeroPostedAtOmitted(t *testing.T) {
	job := makeDetailJob(t,
		"urn:li:fsd_jobPosting:6006", "Analyst",
		"Analyse things.", "", 0,
		time.Time{}, // zero — must be omitted
	)

	b, err := output.JobsDetailJSON([]domain.Job{job}, nil)
	if err != nil {
		t.Fatalf("JobsDetailJSON returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := arr[0]["posted_at"]; present {
		t.Error("posted_at must be omitted when zero")
	}
}

// -- Intros output tests --------------------------------------------------------

func makeIntro(name, profileURL, company, position string, connectedOn time.Time) domain.Intro {
	return domain.Intro{
		Name:            name,
		ProfileURL:      profileURL,
		Company:         company,
		Position:        position,
		ConnectedOn:     connectedOn,
		MatchConfidence: "exact",
	}
}

// TestJobJSONWithIntros_IntrosPresent: when intros are provided, the "intros"
// key is present and has the expected shape.
func TestJobJSONWithIntros_IntrosPresent(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:7001")
	job := domain.NewJob(
		urn, "Staff Engineer", "Remote",
		domain.NewCompany("", "Acme Inc"),
		time.Time{},
		domain.NewPosting("desc", "https://example.com/apply", 5),
	)
	connectedOn := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	intros := []domain.Intro{makeIntro("Alice Alpha", "https://www.linkedin.com/in/alice", "Acme Inc", "Engineer", connectedOn)}

	b, err := output.JobJSONWithIntros(job, nil, intros)
	if err != nil {
		t.Fatalf("JobJSONWithIntros returned error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	introsRaw, ok := obj["intros"]
	if !ok {
		t.Fatal("missing 'intros' key")
	}
	arr, ok := introsRaw.([]any)
	if !ok {
		t.Fatalf("intros is %T, want array", introsRaw)
	}
	if len(arr) != 1 {
		t.Fatalf("intros len = %d; want 1", len(arr))
	}

	rec, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("intro[0] is %T, want object", arr[0])
	}
	if v, _ := rec["name"].(string); v != "Alice Alpha" {
		t.Errorf("name = %q; want %q", v, "Alice Alpha")
	}
	if v, _ := rec["profile_url"].(string); v != "https://www.linkedin.com/in/alice" {
		t.Errorf("profile_url = %q; want expected URL", v)
	}
	if v, _ := rec["company"].(string); v != "Acme Inc" {
		t.Errorf("company = %q; want %q", v, "Acme Inc")
	}
	if v, _ := rec["position"].(string); v != "Engineer" {
		t.Errorf("position = %q; want %q", v, "Engineer")
	}
	if v, _ := rec["match"].(string); v != "exact" {
		t.Errorf("match = %q; want %q", v, "exact")
	}
	// connected_on should be YYYY-MM-DD.
	if v, _ := rec["connected_on"].(string); v != "2026-01-15" {
		t.Errorf("connected_on = %q; want %q", v, "2026-01-15")
	}
}

// TestJobJSONWithIntros_IntrosOmittedWhenNil: when intros is nil, the "intros"
// key is absent from the output.
func TestJobJSONWithIntros_IntrosOmittedWhenNil(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:7002")
	job := domain.NewJob(
		urn, "SRE", "Berlin",
		domain.NewCompany("", "Foo Ltd"),
		time.Time{},
		domain.NewPosting("", "", 0),
	)

	b, err := output.JobJSONWithIntros(job, nil, nil)
	if err != nil {
		t.Fatalf("JobJSONWithIntros returned error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := obj["intros"]; present {
		t.Error("intros key must be absent when nil")
	}
}

// TestJobJSONWithIntros_ZeroConnectedOnOmitted: when ConnectedOn is zero, the
// "connected_on" key must be omitted from the intro object.
func TestJobJSONWithIntros_ZeroConnectedOnOmitted(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:7003")
	job := domain.NewJob(
		urn, "Dev", "Remote",
		domain.NewCompany("", "Foo Inc"),
		time.Time{},
		domain.NewPosting("", "", 0),
	)
	intro := makeIntro("Bob Beta", "https://www.linkedin.com/in/bob", "Foo Inc", "Dev", time.Time{})

	b, err := output.JobJSONWithIntros(job, nil, []domain.Intro{intro})
	if err != nil {
		t.Fatalf("JobJSONWithIntros returned error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	introsRaw := obj["intros"].([]any)
	rec := introsRaw[0].(map[string]any)
	if _, present := rec["connected_on"]; present {
		t.Error("connected_on must be omitted when zero")
	}
}

// TestJobsDetailJSONWithIntros_Shape: verifies that JobsDetailJSONWithIntros
// attaches intros by URN and omits the key when no intros are present.
func TestJobsDetailJSONWithIntros_Shape(t *testing.T) {
	job1 := makeDetailJob(t,
		"urn:li:fsd_jobPosting:7010", "Engineer A",
		"desc", "https://example.com", 1,
		time.Time{},
	)
	job2 := makeDetailJob(t,
		"urn:li:fsd_jobPosting:7011", "Engineer B",
		"desc", "https://example.com", 2,
		time.Time{},
	)
	introsByURN := map[string][]domain.Intro{
		string(job1.URN()): {makeIntro("Alice A", "https://www.linkedin.com/in/alice", "Acme", "Dev", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))},
		// job2 has no intros.
	}

	b, err := output.JobsDetailJSONWithIntros([]domain.Job{job1, job2}, nil, introsByURN)
	if err != nil {
		t.Fatalf("JobsDetailJSONWithIntros returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("len = %d; want 2", len(arr))
	}

	// job1 must have intros.
	if _, ok := arr[0]["intros"]; !ok {
		t.Error("job1: missing 'intros' key")
	}
	// job2 must not have intros.
	if _, ok := arr[1]["intros"]; ok {
		t.Error("job2: 'intros' key must be absent")
	}
}

// -- JobsJSONWithIntros (brief+intros shape) ------------------------------------

func makeBriefJob(t *testing.T, urnStr, title, company string) domain.Job {
	t.Helper()
	urn := mustURN(t, urnStr)
	return domain.NewJob(
		urn, title, "Remote",
		domain.NewCompany("", company),
		time.Time{},
		// Posting fields deliberately empty — this is a search-result job.
		domain.NewPosting("", "", 0),
	)
}

// TestJobsJSONWithIntros_BriefShape_NoPostingBlock: when --intros is used
// without --enrich, the output must use the brief shape with no posting block,
// even when intros are present.
func TestJobsJSONWithIntros_BriefShape_NoPostingBlock(t *testing.T) {
	job := makeBriefJob(t, "urn:li:fsd_jobPosting:8010", "SWE", "Acme")
	intros := []domain.Intro{makeIntro("Alice A", "https://www.linkedin.com/in/alice", "Acme", "Dev", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
	introsByURN := map[string][]domain.Intro{
		string(job.URN()): intros,
	}

	b, err := output.JobsJSONWithIntros([]domain.Job{job}, introsByURN)
	if err != nil {
		t.Fatalf("JobsJSONWithIntros returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("len = %d; want 1", len(arr))
	}
	rec := arr[0]

	// Must NOT have a posting block.
	if _, present := rec["posting"]; present {
		t.Error("posting block must be absent in brief+intros shape")
	}
	// Must NOT have an insights block.
	if _, present := rec["insights"]; present {
		t.Error("insights block must be absent in brief+intros shape")
	}
	// Must have intros.
	if _, present := rec["intros"]; !present {
		t.Error("intros key must be present")
	}
	// Standard brief fields must be present.
	if v, _ := rec["urn"].(string); v != string(job.URN()) {
		t.Errorf("urn = %q; want %q", v, string(job.URN()))
	}
}

// TestJobsJSONWithIntros_ZeroMatches_NoBriefIntrosKey: when --intros is set
// but no connections match (zero intros), the intros key must be absent and
// the brief shape must still be used (no posting block).
func TestJobsJSONWithIntros_ZeroMatches_NoBriefIntrosKey(t *testing.T) {
	job := makeBriefJob(t, "urn:li:fsd_jobPosting:8011", "SWE", "Acme")
	// Pass an empty (non-nil) introsByURN map — this simulates --intros with
	// connections loaded but no match for this particular job.
	introsByURN := map[string][]domain.Intro{}

	b, err := output.JobsJSONWithIntros([]domain.Job{job}, introsByURN)
	if err != nil {
		t.Fatalf("JobsJSONWithIntros returned error: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rec := arr[0]

	// No posting block.
	if _, present := rec["posting"]; present {
		t.Error("posting block must be absent")
	}
	// No intros key when slice is absent/empty.
	if _, present := rec["intros"]; present {
		t.Error("intros key must be omitted when no matches")
	}
}

// -- Byte-identity tests --------------------------------------------------------
// These assert that the no-intros paths produce byte-for-byte identical output
// to the pre-intros functions, so the no-intros output contract is unchanged.

// TestJobJSONWithInsights_ByteIdenticalToJobJSONWithIntros_NilIntros: passing
// nil intros to JobJSONWithIntros must produce identical bytes to
// JobJSONWithInsights on the same inputs.
func TestJobJSONWithInsights_ByteIdenticalToJobJSONWithIntros_NilIntros(t *testing.T) {
	urn := mustURN(t, "urn:li:fsd_jobPosting:8020")
	postedAt := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	job := domain.NewJob(
		urn, "Staff Engineer", "Berlin",
		domain.NewCompany(mustURN(t, "urn:li:company:42"), "Acme GmbH"),
		postedAt,
		domain.NewPosting("Build things.", "https://example.com/apply", 12),
	)
	ins := &domain.Insights{RealSummary: "great role", TopSkills: []string{"Go"}}

	a, err := output.JobJSONWithInsights(job, ins)
	if err != nil {
		t.Fatalf("JobJSONWithInsights: %v", err)
	}
	bBytes, err := output.JobJSONWithIntros(job, ins, nil)
	if err != nil {
		t.Fatalf("JobJSONWithIntros: %v", err)
	}
	if !bytes.Equal(a, bBytes) {
		t.Errorf("JobJSONWithInsights and JobJSONWithIntros(nil intros) differ:\nwith insights:\n%s\n\nwith intros=nil:\n%s", a, bBytes)
	}
}

// TestJobsDetailJSON_ByteIdenticalToJobsDetailJSONWithIntros_NilIntros: passing
// nil introsByURN to JobsDetailJSONWithIntros must produce identical bytes to
// JobsDetailJSON on the same inputs.
func TestJobsDetailJSON_ByteIdenticalToJobsDetailJSONWithIntros_NilIntros(t *testing.T) {
	jobs := []domain.Job{
		makeDetailJob(t, "urn:li:fsd_jobPosting:8021", "SRE",
			"Keep lights on.", "https://example.com/sre", 3,
			time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		),
		makeDetailJob(t, "urn:li:fsd_jobPosting:8022", "Platform Lead",
			"Drive strategy.", "https://example.com/pl", 99,
			time.Time{},
		),
	}
	insights := map[string]*domain.Insights{
		string(jobs[0].URN()): {RealSummary: "solid"},
	}

	a, err := output.JobsDetailJSON(jobs, insights)
	if err != nil {
		t.Fatalf("JobsDetailJSON: %v", err)
	}
	bBytes, err := output.JobsDetailJSONWithIntros(jobs, insights, nil)
	if err != nil {
		t.Fatalf("JobsDetailJSONWithIntros: %v", err)
	}
	if !bytes.Equal(a, bBytes) {
		t.Errorf("JobsDetailJSON and JobsDetailJSONWithIntros(nil intros) differ:\nJobsDetailJSON:\n%s\n\nJobsDetailJSONWithIntros(nil):\n%s", a, bBytes)
	}
}
