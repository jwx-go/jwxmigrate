package example

import (
	"regexp"

	"github.com/lestrrat-go/jwx/v4/jwk"
)

// Exercises every whitelist identifier the rule lists.

var (
	_ = jwk.InsecureWhitelist{}
	_ = jwk.NewMapWhitelist().Add("https://issuer.example/jwks.json")
	_ jwk.MapWhitelist
	_ = jwk.RegexpWhitelist{Patterns: []*regexp.Regexp{regexp.MustCompile(`^https://issuer\.example/`)}}
	_ = jwk.WhitelistFunc(func(url string) bool { return false })
	_ = jwk.WhitelistError()
)
