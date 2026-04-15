package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func fetchKeys(ctx context.Context, url string, wl jwk.Whitelist) (jwk.Set, error) {
	return jwk.Fetch(ctx, url, jwk.WithFetchWhitelist(wl))
}
