package example

import "github.com/lestrrat-go/jwx/v3/jwt"

func filters() (jwt.TokenFilter, jwt.TokenFilter) {
	return jwt.NewClaimNameFilter("sub", "iss"), jwt.StandardClaimsFilter()
}
