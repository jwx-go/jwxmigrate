package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

// v2 .Get(name) returned (interface{}, error) — different shape from v3's
// .Get(name, &dst) error. The rule fires on any .Get( method call; without
// go/packages type-checked loading the matcher doesn't distinguish
// receivers, so a realistic single-call fixture is enough.

func algorithm(key jwk.Key) (any, error) {
	return key.Get(jwk.AlgorithmKey)
}
