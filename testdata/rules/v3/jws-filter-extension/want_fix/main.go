package example

import "github.com/lestrrat-go/jwx/v4/jws"

func filters() (jws.HeaderFilter, jws.HeaderFilter) {
	return jws.NewHeaderNameFilter("alg", "kid"), jws.StandardHeadersFilter()
}
