package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
	"os"
)

func load() {
	tok, _ := jwt.ParseFS(os.DirFS("."), "token.jwt")
	_ = tok
}
