package example

import (
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Each segment of a chained call should be evaluated independently by the
// scanner. Here jwt.ReadFile appears nested inside a selector chain along
// with other operations.

func example() {
	tok, _ := jwt.ReadFile("token.jwt")
	_ = tok
}
