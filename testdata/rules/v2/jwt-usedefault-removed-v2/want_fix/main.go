package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

func parseDefault(data []byte, set jwk.Set) (jwt.Token, error) {
	return jwt.Parse(data, jwt.WithKeySet(set), jwt.UseDefault(true))
}
