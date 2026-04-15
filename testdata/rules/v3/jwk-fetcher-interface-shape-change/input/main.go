package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

// myFetcher implements the v3 jwk.Fetcher shape (variadic FetchOption tail).
// In v4 the interface drops the variadic tail, so this type no longer
// satisfies jwk.Fetcher and must be updated.
type myFetcher struct{}

func (myFetcher) Fetch(ctx context.Context, url string, opts ...jwk.FetchOption) (jwk.Set, error) {
	return nil, nil
}

var _ jwk.Fetcher = myFetcher{}
