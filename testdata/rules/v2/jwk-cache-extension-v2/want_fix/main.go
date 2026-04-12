package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v4/jwk"
)

func build(ctx context.Context) {
	cache := jwk.NewCache(ctx)
	_ = cache
}
