package example

import (
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func load() {
	tok, _ := jwt.ReadFile("token.jwt")
	_ = tok
}
