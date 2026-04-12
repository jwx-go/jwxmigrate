package example

import (
	"context"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

// Realistic jwk cache setup. One fixture covers every rule that fires on
// the Cache/Register subsystem in v3:
//
//   - jwk-cache-removed / jwk-cache-extension — jwk.NewCache(...)
//   - jwk-withconstantinterval-removed         — jwk.WithConstantInterval
//   - jwk-withmininterval-removed              — jwk.WithMinInterval
//   - jwk-withmaxinterval-removed              — jwk.WithMaxInterval
//   - jwk-withhttprcresourceoption-removed     — jwk.WithHttprcResourceOption
//   - jwk-cacheoption-removed                  — jwk.CacheOption (param type)
//   - jwk-resourceoption-removed               — jwk.ResourceOption (param type)
//   - jwk-registeroption-removed               — jwk.RegisterOption (param type)
//   - jwk-registerfetchoption-removed          — jwk.RegisterFetchOption (param type)
//
// The expectation is per-rule: running `jwxmigrate check` surfaces every
// symbol in context, not as isolated stubs.

func makeCache(ctx context.Context, client any, extra []jwk.CacheOption) (any, error) {
	cache, err := jwk.NewCache(ctx, client, extra...)
	if err != nil {
		return nil, err
	}
	return cache, nil
}

func registerURL(ctx context.Context, cache any, url string, resourceOpts []jwk.ResourceOption, regOpts []jwk.RegisterOption, fetchOpts []jwk.RegisterFetchOption) {
	_ = cache
	_ = resourceOpts
	_ = regOpts
	_ = fetchOpts
	_ = url
	_ = ctx
}

func defaultOptions() []jwk.RegisterOption {
	return []jwk.RegisterOption{
		jwk.WithConstantInterval(15 * time.Minute),
		jwk.WithMinInterval(5 * time.Minute),
		jwk.WithMaxInterval(60 * time.Minute),
		jwk.WithHttprcResourceOption(nil),
	}
}
