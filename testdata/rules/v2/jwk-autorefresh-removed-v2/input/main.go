package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v2/jwk"
)

// jwk.AutoRefresh was the pre-v2 name for the JWK cache subsystem. Some
// code may still use it. In v4 the whole subsystem moved to the
// jwkfetch extension module.

func build(ctx context.Context) {
	ar := jwk.NewAutoRefresh(ctx)
	_ = ar
}
