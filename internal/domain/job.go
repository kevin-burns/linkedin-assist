package domain

import "time"

// Posting is the listing-detail half of a Job (description, apply URL, applicants).
type Posting struct {
	description    string
	applyURL       string
	applicantCount int
}

// NewPosting constructs a Posting value object.
func NewPosting(description, applyURL string, applicantCount int) Posting {
	return Posting{
		description:    description,
		applyURL:       applyURL,
		applicantCount: applicantCount,
	}
}

func (p Posting) Description() string { return p.description }
func (p Posting) ApplyURL() string    { return p.applyURL }
func (p Posting) ApplicantCount() int { return p.applicantCount }

// Job is an immutable snapshot of a LinkedIn job listing.
type Job struct {
	urn      URN
	title    string
	location string
	company  Company
	postedAt time.Time
	posting  Posting
}

// NewJob constructs a Job value object.
func NewJob(urn URN, title, location string, company Company, postedAt time.Time, posting Posting) Job {
	return Job{
		urn:      urn,
		title:    title,
		location: location,
		company:  company,
		postedAt: postedAt,
		posting:  posting,
	}
}

func (j Job) URN() URN            { return j.urn }
func (j Job) Title() string       { return j.title }
func (j Job) Location() string    { return j.location }
func (j Job) Company() Company    { return j.company }
func (j Job) PostedAt() time.Time { return j.postedAt }
func (j Job) Posting() Posting    { return j.posting }
