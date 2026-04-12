package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v4/jwk"
)

func build(ctx context.Context, client any) {
	cache, _ := jwk.NewCache(ctx, client)
	_ = cache
	var set jwk.CachedSet
	_ = set
}
