package asn

// Resolver returns an ASN label for a client IP when a database is configured.
// Default implementation always returns empty (null in JSON).
type Resolver interface {
	Lookup(ip string) string
}

type NullResolver struct{}

func (NullResolver) Lookup(string) string { return "" }

func NewResolver() Resolver {
	return NullResolver{}
}
