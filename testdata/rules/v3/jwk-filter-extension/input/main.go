package example

import "github.com/lestrrat-go/jwx/v3/jwk"

func filters() []jwk.KeyFilter {
	return []jwk.KeyFilter{
		jwk.NewFieldNameFilter("kty", "kid"),
		jwk.RSAStandardFieldsFilter(),
		jwk.ECDSAStandardFieldsFilter(),
		jwk.OKPStandardFieldsFilter(),
		jwk.SymmetricStandardFieldsFilter(),
	}
}
