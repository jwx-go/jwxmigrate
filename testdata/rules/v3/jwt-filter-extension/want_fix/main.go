package example

import "github.com/lestrrat-go/jwx/v4/jwt"

func filters() (jwt.TokenFilter, jwt.TokenFilter) {
	return jwt.NewClaimNameFilter("sub", "iss"), jwt.StandardClaimsFilter()
}
