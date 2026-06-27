package domain

// Insights is the enriched analysis of a job posting produced by the LLM
// enrichment layer. Fields are exported with JSON tags so the struct can be
// marshalled directly to/from the cache's insights blob and to command output.
// This is a plain data record (not a value-object with unexported fields)
// because it travels across process boundaries as serialised JSON.
type Insights struct {
	RealSummary          string   `json:"real_summary"`
	TopSkills            []string `json:"top_skills"`
	SalaryRange          string   `json:"salary_range,omitempty"`
	Seniority            string   `json:"seniority,omitempty"`
	CondensedDescription string   `json:"condensed_description"`
	// Notes flags observations such as "JD appears AI-generated / boilerplate".
	Notes string `json:"notes,omitempty"`
}
