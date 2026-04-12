package example

import (
	"context"

	"github.com/lestrrat-go/jwx/v4/jwk"
)

// Realistic v2 jwk cache setup. Covers (in context) the option-type
// rules that would be trivial stubs as per-rule fixtures:
//
//   - jwk-cacheoption-removed-v2    — []jwk.CacheOption parameter
//   - jwk-resourceoption-removed-v2 — []jwk.ResourceOption parameter
//   - jwk-registeroption-removed-v2 — []jwk.RegisterOption parameter

func makeCache(ctx context.Context, opts []jwk.CacheOption, resourceOpts []jwk.ResourceOption, regOpts []jwk.RegisterOption) {
	cache := jwk.NewCache(ctx, opts...)
	_ = cache
	_ = resourceOpts
	_ = regOpts
}
