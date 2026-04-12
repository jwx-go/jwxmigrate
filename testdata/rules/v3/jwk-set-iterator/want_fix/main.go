package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

// KNOWN GAP: jwk-set-iterator's search patterns (set.Len(), .Key(<ident>))
// should match method calls on jwk.Set values, but the AST scanner only
// matches package-qualified calls — method calls on local vars whose type
// is a v3 type are missed unless type-checked loading succeeds. Golden is
// "(no findings)" today; regenerate it when the scanner grows type-aware
// method matching for this rule family.

func iterate(set jwk.Set) {
	for i := 0; i < set.Len(); i++ {
		key, ok := set.Key(i)
		if !ok {
			continue
		}
		_ = key
	}
}
