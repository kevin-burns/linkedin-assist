package domain

// ProfileSlug is LinkedIn's URL slug for a profile (e.g. "kevin-burns-abc123").
type ProfileSlug string

// Person is an immutable snapshot of a LinkedIn member profile.
type Person struct {
	urn         URN
	slug        ProfileSlug
	displayName string
	headline    string
	location    string
}

// NewPerson constructs a Person value object.
func NewPerson(urn URN, slug ProfileSlug, displayName, headline, location string) Person {
	return Person{
		urn:         urn,
		slug:        slug,
		displayName: displayName,
		headline:    headline,
		location:    location,
	}
}

func (p Person) URN() URN            { return p.urn }
func (p Person) Slug() ProfileSlug   { return p.slug }
func (p Person) DisplayName() string { return p.displayName }
func (p Person) Headline() string    { return p.headline }
func (p Person) Location() string    { return p.location }
