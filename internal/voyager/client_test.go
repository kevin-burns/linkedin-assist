package voyager

import (
	"net/url"
	"strings"
	"testing"
)

// TestEncodeVoyagerURL_golden is the M0-oracle test: keyword "platform engineer",
// location "Berlin". The query param must appear on the wire as:
//
//	query=(origin:JOB_SEARCH_PAGE_OTHER_ENTRY,keywords:platform%20engineer,locationUnion:(seoLocation:(location:Berlin)),spellCorrectionEnabled:true)
//
// i.e. structural chars ( ) : , are LITERAL; spaces become %20; other standard
// params use normal encoding.
func TestEncodeVoyagerURL_golden(t *testing.T) {
	params := JobSearchParams{
		Keywords: "platform engineer",
		Location: "Berlin",
		Start:    0,
		Count:    25,
	}
	query := buildJobsSearchQuery(params)
	got := encodeVoyagerURL(jobsSearchPath, query)

	// Full-URL oracle: url.Values.Encode orders standard params alphabetically
	// (count, decorationId, q, start), then the literal restli query param is
	// appended. Exact match catches param-ordering and double-encoding regressions.
	const want = "https://www.linkedin.com/voyager/api/voyagerJobsDashJobCards" +
		"?count=25" +
		"&decorationId=com.linkedin.voyager.dash.deco.jobs.search.JobSearchCardsCollection-220" +
		"&q=jobSearch" +
		"&start=0" +
		"&query=(origin:JOB_SEARCH_PAGE_OTHER_ENTRY,keywords:platform%20engineer,locationUnion:(seoLocation:(location:Berlin)),spellCorrectionEnabled:true)"

	if got != want {
		t.Errorf("encodeVoyagerURL mismatch\nwant: %s\ngot:  %s", want, got)
	}
}

// TestEncodeVoyagerURL_structuralCharsLiteral verifies that the restli
// structural characters ( ) : , are NOT percent-encoded in the query value.
func TestEncodeVoyagerURL_structuralCharsLiteral(t *testing.T) {
	q := url.Values{}
	q.Set("query", "(origin:JOB_SEARCH,keywords:go)")

	got := encodeVoyagerURL("/voyager/api/test", q)
	// All structural chars must appear literally in the URL.
	for _, char := range []string{"(", ")", ":", ","} {
		if !strings.Contains(got, char) {
			t.Errorf("structural char %q was encoded away; URL: %s", char, got)
		}
	}
	// Standard percent-encoding of these chars must NOT appear.
	for pair := range map[string]string{
		"%28": "(",
		"%29": ")",
		"%3A": ":",
		"%2C": ",",
	} {
		if strings.Contains(strings.ToUpper(got), strings.ToUpper(pair)) {
			t.Errorf("structural char was percent-encoded as %q; URL: %s", pair, got)
		}
	}
}

// TestEncodeVoyagerURL_spacesBecome20 confirms that spaces in the restli
// query value are encoded as %20 (not + or %2B).
func TestEncodeVoyagerURL_spacesBecome20(t *testing.T) {
	q := url.Values{}
	q.Set("query", "(keywords:hello world)")

	got := encodeVoyagerURL("/voyager/api/test", q)

	if !strings.Contains(got, "%20") {
		t.Errorf("space not encoded as %%20; URL: %s", got)
	}
	if strings.Contains(got, "hello world") {
		t.Errorf("space appeared literally (unencoded) in URL: %s", got)
	}
	if strings.Contains(got, "hello+world") {
		t.Errorf("space encoded as + (wrong); URL: %s", got)
	}
}

// TestEncodeVoyagerURL_noDoubleEncoding ensures that a pre-existing %XX in the
// restli tuple (produced by restliEscape for structural chars in user input)
// is NOT double-encoded to %25XX on the wire.
func TestEncodeVoyagerURL_noDoubleEncoding(t *testing.T) {
	// Simulate a keyword that contains a restli structural char: "go, python".
	// restliEscape would turn the comma into %2C before embedding in the tuple.
	rawTuple := "(keywords:go%2C%20python)"
	q := url.Values{}
	q.Set("query", rawTuple)

	got := encodeVoyagerURL("/voyager/api/test", q)

	// The %2C must survive as-is -- NOT become %252C.
	if strings.Contains(got, "%252C") {
		t.Errorf("double-encoding detected (%%252C); URL: %s", got)
	}
	if !strings.Contains(got, "%2C") {
		t.Errorf("pre-encoded %%2C was lost; URL: %s", got)
	}
	// The %20 should also survive.
	if strings.Contains(got, "%2520") {
		t.Errorf("double-encoding detected (%%2520); URL: %s", got)
	}
}

// TestEncodeVoyagerURL_standardParamsEncodeNormally confirms that params other
// than "query" are URL-encoded following standard rules (e.g. decorationId dots
// and dashes pass through; no structural-char special treatment needed).
func TestEncodeVoyagerURL_standardParamsEncodeNormally(t *testing.T) {
	q := url.Values{}
	q.Set("decorationId", "com.linkedin.voyager.dash.deco.jobs.search.JobSearchCardsCollection-220")
	q.Set("count", "25")
	q.Set("q", "jobSearch")
	q.Set("start", "0")

	got := encodeVoyagerURL("/voyager/api/test", q)

	// decorationId should appear without modification (dots and dashes are safe).
	if !strings.Contains(got, "decorationId=com.linkedin.voyager.dash.deco.jobs.search.JobSearchCardsCollection-220") {
		t.Errorf("decorationId not present as expected; URL: %s", got)
	}
	if !strings.Contains(got, "count=25") {
		t.Errorf("count param missing; URL: %s", got)
	}
}

// TestEncodeVoyagerURL_noQuery checks the degenerate case where no query param
// is present: the URL must be well-formed without a trailing '?'.
func TestEncodeVoyagerURL_noQuery(t *testing.T) {
	q := url.Values{}
	q.Set("count", "10")

	got := encodeVoyagerURL("/voyager/api/test", q)

	if !strings.HasPrefix(got, "https://www.linkedin.com/voyager/api/test?") {
		t.Errorf("URL has unexpected prefix; URL: %s", got)
	}
	if strings.HasSuffix(got, "?") {
		t.Errorf("URL has trailing '?'; URL: %s", got)
	}
}

// TestEncodeVoyagerURL_emptyQuery checks that an empty url.Values produces
// just the base URL + path with no trailing '?'.
func TestEncodeVoyagerURL_emptyQuery(t *testing.T) {
	got := encodeVoyagerURL("/voyager/api/test", url.Values{})
	want := "https://www.linkedin.com/voyager/api/test"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestEncodeRestliValue_cases exercises the low-level encoder directly.
func TestEncodeRestliValue_cases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain restli tuple is unchanged",
			input: "(origin:JOB_SEARCH,spellCorrectionEnabled:true)",
			want:  "(origin:JOB_SEARCH,spellCorrectionEnabled:true)",
		},
		{
			name:  "space becomes %20",
			input: "platform engineer",
			want:  "platform%20engineer",
		},
		{
			name:  "pre-encoded %20 not double-encoded",
			input: "platform%20engineer",
			want:  "platform%20engineer",
		},
		{
			name:  "pre-encoded %2C not double-encoded",
			input: "go%2C%20python",
			want:  "go%2C%20python",
		},
		{
			name:  "ampersand is encoded",
			input: "a&b",
			want:  "a%26b",
		},
		{
			name:  "equals sign is encoded",
			input: "a=b",
			want:  "a%3Db",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := encodeRestliValue(tc.input)
			if got != tc.want {
				t.Errorf("encodeRestliValue(%q)\ngot:  %q\nwant: %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestEncodeVoyagerURL_queryOnlyURL checks a URL built with only the restli
// query param (no other standard params) has correct structure.
func TestEncodeVoyagerURL_queryOnlyURL(t *testing.T) {
	q := url.Values{}
	q.Set("query", "(origin:TEST)")

	got := encodeVoyagerURL("/voyager/api/test", q)

	wantPrefix := "https://www.linkedin.com/voyager/api/test?query="
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("URL does not start with %q; URL: %s", wantPrefix, got)
	}
	// The literal parens and colon must appear after "query=".
	if !strings.Contains(got, "query=(origin:TEST)") {
		t.Errorf("restli tuple not present literally; URL: %s", got)
	}
}

// TestEncodeVoyagerURL_variables asserts that "variables" is treated as a restli
// tuple (structural chars literal) just like "query", and that standard params
// (queryId, includeWebMetadata) are encoded normally.
func TestEncodeVoyagerURL_variables(t *testing.T) {
	q := url.Values{}
	q.Set("includeWebMetadata", "true")
	q.Set("queryId", "voyagerJobsDashJobPostingDetailSections.77cb64956921ef397a36de4f7f8bce47")
	q.Set("variables", "(cardSectionTypes:List(TOP_CARD,JOB_DESCRIPTION_CARD),jobPostingUrn:urn%3Ali%3Afsd_jobPosting%3A4427011736,includeSecondaryActionsV2:true)")

	got := encodeVoyagerURL("/voyager/api/graphql", q)

	// Structural chars in variables must appear LITERAL on the wire.
	for _, char := range []string{"(", ")", ":", ","} {
		if !strings.Contains(got, char) {
			t.Errorf("structural char %q was encoded away; URL: %s", char, got)
		}
	}

	// The List(...) syntax must survive intact.
	if !strings.Contains(got, "List(TOP_CARD,JOB_DESCRIPTION_CARD)") {
		t.Errorf("List(...) not present literally; URL: %s", got)
	}

	// The pre-encoded %3A in the URN must NOT be double-encoded to %253A.
	if strings.Contains(got, "%253A") {
		t.Errorf("double-encoding of %%3A detected (%%253A); URL: %s", got)
	}
	// The %3A must survive.
	if !strings.Contains(got, "%3A") {
		t.Errorf("pre-encoded %%3A was lost; URL: %s", got)
	}

	// Standard params must be normally encoded.
	if !strings.Contains(got, "queryId=voyagerJobsDashJobPostingDetailSections.77cb64956921ef397a36de4f7f8bce47") {
		t.Errorf("queryId not present normally-encoded; URL: %s", got)
	}
	if !strings.Contains(got, "includeWebMetadata=true") {
		t.Errorf("includeWebMetadata not present; URL: %s", got)
	}

	// variables param must appear as "variables=..." (not percent-encoded key).
	if !strings.Contains(got, "variables=(") {
		t.Errorf("variables param not present with literal opening paren; URL: %s", got)
	}

	t.Logf("variables URL: %s", got)
}
