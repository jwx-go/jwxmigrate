package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
)

func load() {
	tok, _ := jwt.ParseFS("token.jwt")
	_ = tok
}
