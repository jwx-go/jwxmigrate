package example

import "github.com/lestrrat-go/jwx/v3/jws"

func filters() (jws.HeaderFilter, jws.HeaderFilter) {
	return jws.NewHeaderNameFilter("alg", "kid"), jws.StandardHeadersFilter()
}
