package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v2/jwk"
)

// v2 exposed Set.Keys(ctx) returning a KeyIterator. v4 uses Go iterators
// via Set.All() instead. The rule fires on both the Keys(ctx) call and
// the KeyIterator type reference.

func iterate(ctx context.Context, set jwk.Set) {
	iter := set.Keys(ctx)
	for iter.Next(ctx) {
		_ = iter.Pair()
	}
}
