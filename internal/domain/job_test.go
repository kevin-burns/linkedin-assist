package domain

import (
	"testing"
	"time"
)

func TestJob_immutableAccessors(t *testing.T) {
	urn, _ := ParseURN("urn:li:job:42")
	companyURN, _ := ParseURN("urn:li:fsd_company:99")
	c := NewCompany(companyURN, "TestCo")
	posting := NewPosting("a job description", "https://apply", 17)
	postedAt := time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC)

	j := NewJob(urn, "Platform Engineer", "Berlin", c, postedAt, posting)

	if j.URN() != urn {
		t.Errorf("URN mismatch")
	}
	if j.Title() != "Platform Engineer" {
		t.Errorf("Title mismatch")
	}
	if j.Location() != "Berlin" {
		t.Errorf("Location mismatch")
	}
	if j.Company().URN() != companyURN {
		t.Errorf("Company.URN mismatch")
	}
	if !j.PostedAt().Equal(postedAt) {
		t.Errorf("PostedAt mismatch")
	}
	if j.Posting().Description() != "a job description" {
		t.Errorf("Posting.Description mismatch")
	}
	if j.Posting().ApplicantCount() != 17 {
		t.Errorf("ApplicantCount mismatch")
	}
}
