// Package output contains pure presenters that convert domain values to
// serialised bytes. It may import only internal/domain.
package output

import (
	"encoding/json"
	"errors"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// jobJSON is the stable pure-Go representation of a domain.Job used for
// JSON serialisation. Using an explicit struct (rather than marshalling domain
// values directly) keeps the wire shape decoupled from the domain model.
type jobJSON struct {
	URN      string      `json:"urn"`
	Title    string      `json:"title"`
	Location string      `json:"location"`
	Company  companyJSON `json:"company"`
	PostedAt string      `json:"posted_at,omitempty"` // RFC3339; omitted when zero
}

// companyJSON is the stable JSON representation of a domain.Company.
type companyJSON struct {
	URN  string `json:"urn"`
	Name string `json:"name"`
}

// introJSON is the stable JSON representation of a single domain.Intro
// (a warm-intro candidate from the user's LinkedIn connections).
//
// connected_on is YYYY-MM-DD; the field is omitted when ConnectedOn is zero.
type introJSON struct {
	Name        string `json:"name"`
	ProfileURL  string `json:"profile_url"`
	Company     string `json:"company"`
	Position    string `json:"position"`
	ConnectedOn string `json:"connected_on,omitempty"` // YYYY-MM-DD; omitted when zero
	Match       string `json:"match"`                  // "exact" in v1
}

// jobBriefWithIntrosJSON is the brief shape (no posting block) plus an optional
// warm-intro array. Used by `jobs sweep --intros` (without --enrich) to avoid
// emitting hollow posting fields for search-result jobs that have not been
// individually fetched.
type jobBriefWithIntrosJSON struct {
	URN      string      `json:"urn"`
	Title    string      `json:"title"`
	Location string      `json:"location"`
	Company  companyJSON `json:"company"`
	PostedAt string      `json:"posted_at,omitempty"` // RFC3339; omitted when zero
	Intros   []introJSON `json:"intros,omitempty"`
}

// jobDetailJSON is the stable JSON representation of a single domain.Job with
// full posting detail (description, apply URL, applicant count, posted_at).
type jobDetailJSON struct {
	URN      string            `json:"urn"`
	Title    string            `json:"title"`
	Location string            `json:"location"`
	Company  companyJSON       `json:"company"`
	PostedAt string            `json:"posted_at,omitempty"` // RFC3339; omitted when zero
	Posting  postingDetailJSON `json:"posting"`
	Insights *domain.Insights  `json:"insights,omitempty"`
	Intros   []introJSON       `json:"intros,omitempty"`
}

// postingDetailJSON is the stable JSON representation of a domain.Posting.
type postingDetailJSON struct {
	Description    string `json:"description"`
	ApplyURL       string `json:"apply_url"`
	ApplicantCount int    `json:"applicant_count"`
}

// toIntroJSON converts a domain.Intro to its wire representation.
func toIntroJSON(intro domain.Intro) introJSON {
	rec := introJSON{
		Name:       intro.Name,
		ProfileURL: intro.ProfileURL,
		Company:    intro.Company,
		Position:   intro.Position,
		Match:      intro.MatchConfidence,
	}
	if !intro.ConnectedOn.IsZero() {
		rec.ConnectedOn = intro.ConnectedOn.UTC().Format("2006-01-02")
	}
	return rec
}

// toIntrosJSON converts a slice of domain.Intro values to their wire
// representation. Returns nil when intros is nil or empty so omitempty works.
func toIntrosJSON(intros []domain.Intro) []introJSON {
	if len(intros) == 0 {
		return nil
	}
	out := make([]introJSON, len(intros))
	for i, intro := range intros {
		out[i] = toIntroJSON(intro)
	}
	return out
}

// JobsJSON serialises a slice of domain.Job values to an indented JSON array.
// The wire shape per job is: urn, title, location, company{urn,name}, posted_at
// (RFC3339, omitted when zero). Returns an error only when json.MarshalIndent
// fails (should not happen in practice with well-formed Go values).
func JobsJSON(jobs []domain.Job) ([]byte, error) {
	out := make([]jobJSON, 0, len(jobs))
	for _, j := range jobs {
		rec := jobJSON{
			URN:      string(j.URN()),
			Title:    j.Title(),
			Location: j.Location(),
			Company: companyJSON{
				URN:  string(j.Company().URN()),
				Name: j.Company().Name(),
			},
		}
		if !j.PostedAt().IsZero() {
			rec.PostedAt = j.PostedAt().UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, rec)
	}
	return json.MarshalIndent(out, "", "  ")
}

// JobsOKF serialises jobs to the Ogham Knowledge Format bundle.
// TODO(okf): implement once the OKF bundle schema is provided.
func JobsOKF(_ []domain.Job) ([]byte, error) {
	return nil, errors.New("okf output not yet implemented: Ogham Knowledge Format schema not yet provided")
}

// JobsJSONWithIntros serialises a slice of domain.Job values in the brief shape
// (urn, title, location, company, posted_at) plus an optional intros array per
// job. It does NOT emit a posting{} block — search-result jobs have hollow
// posting fields and emitting them would be misleading.
//
// Use this for `jobs sweep --intros` (without --enrich). When --enrich is also
// set, use JobsDetailJSONWithIntros instead (posting fields are then real).
//
// introsByURN maps URN string to the job's intro slice; pass nil when no intros
// were computed. The "intros" key is omitted per job when absent or empty.
func JobsJSONWithIntros(jobs []domain.Job, introsByURN map[string][]domain.Intro) ([]byte, error) {
	out := make([]jobBriefWithIntrosJSON, 0, len(jobs))
	for _, j := range jobs {
		var introSlice []domain.Intro
		if introsByURN != nil {
			introSlice = introsByURN[string(j.URN())]
		}
		rec := jobBriefWithIntrosJSON{
			URN:      string(j.URN()),
			Title:    j.Title(),
			Location: j.Location(),
			Company: companyJSON{
				URN:  string(j.Company().URN()),
				Name: j.Company().Name(),
			},
			Intros: toIntrosJSON(introSlice),
		}
		if !j.PostedAt().IsZero() {
			rec.PostedAt = j.PostedAt().UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, rec)
	}
	return json.MarshalIndent(out, "", "  ")
}

// JobsDetailJSON serialises a slice of domain.Job values with full posting
// detail to an indented JSON array. insights maps URN string to LLM-generated
// insights; pass nil (or an empty map) when no enrichment was performed and
// the "insights" key will be omitted per job.
func JobsDetailJSON(jobs []domain.Job, insights map[string]*domain.Insights) ([]byte, error) {
	return JobsDetailJSONWithIntros(jobs, insights, nil)
}

// JobsDetailJSONWithIntros serialises a slice of domain.Job values with full
// posting detail, optional insights, and optional warm-intro candidates to an
// indented JSON array.
//
// introsByURN maps URN string to the job's intro slice; pass nil when no intros
// were computed. The "intros" key is omitted from each record when its slice is
// absent or empty.
func JobsDetailJSONWithIntros(jobs []domain.Job, insights map[string]*domain.Insights, introsByURN map[string][]domain.Intro) ([]byte, error) {
	out := make([]jobDetailJSON, 0, len(jobs))
	for _, j := range jobs {
		var ins *domain.Insights
		if insights != nil {
			ins = insights[string(j.URN())]
		}
		var introSlice []domain.Intro
		if introsByURN != nil {
			introSlice = introsByURN[string(j.URN())]
		}
		rec := jobDetailJSON{
			URN:      string(j.URN()),
			Title:    j.Title(),
			Location: j.Location(),
			Company: companyJSON{
				URN:  string(j.Company().URN()),
				Name: j.Company().Name(),
			},
			Posting: postingDetailJSON{
				Description:    j.Posting().Description(),
				ApplyURL:       j.Posting().ApplyURL(),
				ApplicantCount: j.Posting().ApplicantCount(),
			},
			Insights: ins,
			Intros:   toIntrosJSON(introSlice),
		}
		if !j.PostedAt().IsZero() {
			rec.PostedAt = j.PostedAt().UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, rec)
	}
	return json.MarshalIndent(out, "", "  ")
}

// JobJSON serialises a single domain.Job with its full posting detail to
// indented JSON. Returns an error only when json.MarshalIndent fails (should
// not happen in practice with well-formed Go values).
func JobJSON(job domain.Job) ([]byte, error) {
	return JobJSONWithInsights(job, nil)
}

// JobJSONWithInsights serialises a single domain.Job with optional LLM
// insights included in the output. Pass nil ins when no enrichment was
// performed; the "insights" key is omitted from the output in that case.
func JobJSONWithInsights(job domain.Job, ins *domain.Insights) ([]byte, error) {
	return JobJSONWithIntros(job, ins, nil)
}

// JobJSONWithIntros serialises a single domain.Job with optional LLM insights
// and optional warm-intro candidates. Pass nil for either when not applicable;
// the corresponding keys are omitted from the output.
func JobJSONWithIntros(job domain.Job, ins *domain.Insights, intros []domain.Intro) ([]byte, error) {
	rec := jobDetailJSON{
		URN:      string(job.URN()),
		Title:    job.Title(),
		Location: job.Location(),
		Company: companyJSON{
			URN:  string(job.Company().URN()),
			Name: job.Company().Name(),
		},
		Posting: postingDetailJSON{
			Description:    job.Posting().Description(),
			ApplyURL:       job.Posting().ApplyURL(),
			ApplicantCount: job.Posting().ApplicantCount(),
		},
		Insights: ins,
		Intros:   toIntrosJSON(intros),
	}
	if !job.PostedAt().IsZero() {
		rec.PostedAt = job.PostedAt().UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return json.MarshalIndent(rec, "", "  ")
}
