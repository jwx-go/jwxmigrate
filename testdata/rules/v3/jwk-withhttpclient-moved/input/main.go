package example

import (
	"context"
	"net/http"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func fetchKeys(ctx context.Context, url string, client *http.Client) (jwk.Set, error) {
	return jwk.Fetch(ctx, url, jwk.WithHTTPClient(client))
}
