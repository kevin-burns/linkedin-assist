package usecase

import (
	"sort"
	"strings"
	"unicode"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// legalSuffixes is the set of trailing words stripped during company name
// normalisation. All entries must be lowercase. Order does not matter.
//
// Deferred: a second or third suffix removal pass (e.g. "Foo & Co GmbH → Foo").
// v1 strips only the outermost suffix for simplicity.
var legalSuffixes = []string{
	"gmbh", "inc", "ltd", "llc", "bv", "ag", "limited", "co", "corp", "plc",
}

// normalizeCompany lowercases, trims whitespace, strips punctuation (.,&()),
// strips a leading "the ", then strips one trailing legal suffix.
// Returns empty string when the result is empty (e.g. blank input).
func normalizeCompany(name string) string {
	// Lowercase and trim surrounding whitespace.
	s := strings.TrimSpace(strings.ToLower(name))

	// Strip punctuation characters: . , & ( )
	s = strings.Map(func(r rune) rune {
		switch r {
		case '.', ',', '&', '(', ')':
			return -1
		default:
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}
	}, s)

	// Collapse any runs of whitespace created by punctuation removal.
	s = strings.Join(strings.Fields(s), " ")

	// Strip a leading "the " (with space).
	s = strings.TrimPrefix(s, "the ")
	s = strings.TrimSpace(s)

	// Strip one trailing legal suffix (word boundary: preceded by a space).
	for _, suf := range legalSuffixes {
		if s == suf {
			// The entire name is the suffix — don't reduce to empty.
			break
		}
		if strings.HasSuffix(s, " "+suf) {
			s = strings.TrimSuffix(s, " "+suf)
			s = strings.TrimSpace(s)
			break
		}
	}

	return s
}

// MatchIntros finds 1st-degree connections who appear to work at the same
// company as job.
//
// Matching strategy (v1): exact-normalized — both the job's company name and
// each connection's Company are normalized (lowercase, trim, strip punctuation
// and trailing legal suffixes, strip leading "the ") before comparison. Match
// when the normalized strings are equal and non-empty.
//
// Results are ordered by ConnectedOn descending (most-recent connection first).
// This ordering is a neutral recency default and is NOT a quality or seniority
// ranking; role-family / seniority ranking is deliberately deferred to v2 after
// an offline precision eval.
//
// Returns nil when:
//   - the job has no company name
//   - conns is nil or empty
//   - no connections match
//
// Deferred (v1 out of scope): strong/medium/weak seniority tiers, role-family
// ranking, fuzzy company matching, LLM "why this person" annotation.
func MatchIntros(job domain.Job, conns []domain.Connection) []domain.Intro {
	jobNorm := normalizeCompany(job.Company().Name())
	if jobNorm == "" {
		return nil
	}
	if len(conns) == 0 {
		return nil
	}

	var matches []domain.Intro
	for _, c := range conns {
		connNorm := normalizeCompany(c.Company)
		if connNorm == "" {
			continue
		}
		if connNorm != jobNorm {
			continue
		}
		matches = append(matches, domain.Intro{
			Name:            strings.TrimSpace(c.FirstName + " " + c.LastName),
			ProfileURL:      c.ProfileURL,
			Company:         c.Company,
			Position:        c.Position,
			ConnectedOn:     c.ConnectedOn,
			MatchConfidence: "exact",
		})
	}

	if len(matches) == 0 {
		return nil
	}

	// Sort by ConnectedOn descending (most-recent first).
	// Zero ConnectedOn values sort last (treated as the zero instant).
	// Tie-break by Name ascending so output order is deterministic regardless
	// of the input slice order (e.g. two connections joined on the same date).
	sort.SliceStable(matches, func(i, j int) bool {
		ci, cj := matches[i].ConnectedOn, matches[j].ConnectedOn
		if !ci.Equal(cj) {
			return ci.After(cj)
		}
		return matches[i].Name < matches[j].Name
	})

	return matches
}
