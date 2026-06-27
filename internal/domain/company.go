package domain

// Company is an immutable snapshot of a LinkedIn company.
type Company struct {
	urn  URN
	name string
}

// NewCompany constructs a Company value object.
func NewCompany(urn URN, name string) Company {
	return Company{urn: urn, name: name}
}

// URN returns the company's LinkedIn URN.
func (c Company) URN() URN { return c.urn }

// Name returns the company's display name.
func (c Company) Name() string { return c.name }
