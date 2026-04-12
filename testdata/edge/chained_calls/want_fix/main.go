package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
	"os"
)

// Each segment of a chained call should be evaluated independently by the
// scanner. Here jwt.ReadFile appears nested inside a selector chain along
// with other operations.

func example() {
	tok, _ := jwt.ParseFS(os.DirFS("."), "token.jwt")
	_ = tok
}
