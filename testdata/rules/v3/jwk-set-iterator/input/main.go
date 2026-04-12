package example

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
)

// jwk-set-iterator's search patterns target method calls on jwk.Set
// values. Both set.Len() and set.Key(i) fire here — the scanner resolves
// the receiver "set" via the parser-populated ast.Object chain back to
// its declared type (func parameter jwk.Set), without needing go/packages
// type-checked loading.

func iterate(set jwk.Set) {
	for i := 0; i < set.Len(); i++ {
		key, ok := set.Key(i)
		if !ok {
			continue
		}
		_ = key
	}
}
