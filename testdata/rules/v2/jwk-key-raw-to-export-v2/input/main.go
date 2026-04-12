package example

import (
	"crypto/rsa"

	"github.com/lestrrat-go/jwx/v2/jwk"
)

// v2 had (jwk.Key).Raw(&dst) method; v4 has the package-level
// jwk.Export(key) function. The rule's search pattern is `.Raw\(` which
// matches any .Raw( call — the fixture uses it on a jwk.Key.

func extract(key jwk.Key) error {
	var out rsa.PrivateKey
	return key.Raw(&out)
}
