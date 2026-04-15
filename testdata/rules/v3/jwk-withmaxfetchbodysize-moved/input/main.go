package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func fetchKeys(ctx context.Context, url string) (jwk.Set, error) {
	return jwk.Fetch(ctx, url, jwk.WithMaxFetchBodySize(1<<20))
}
