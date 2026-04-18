package example

import "github.com/lestrrat-go/jwx/v4/jwe"

func filters() (jwe.HeaderFilter, jwe.HeaderFilter) {
	return jwe.NewHeaderNameFilter("alg", "enc"), jwe.StandardHeadersFilter()
}
