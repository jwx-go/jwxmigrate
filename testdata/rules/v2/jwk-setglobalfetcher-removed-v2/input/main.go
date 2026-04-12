package example

import (
	"github.com/lestrrat-go/jwx/v2/jwk"
)

func init() {
	jwk.SetGlobalFetcher(nil)
}
