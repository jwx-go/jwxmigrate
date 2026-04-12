package example

import (
	_ "github.com/lestrrat-go/jwx/v4/jwk"
)

// A local variable named "jwk" shadows the package name. The scanner must
// not confuse a method call on the local value with a package-qualified
// jwk.Import call from the v3 API. Since this file imports jwk only as a
// blank import, there is no package-level "jwk" identifier here — but the
// local var uses the name anyway.

type fakeSet struct{}

func (fakeSet) Import(_ any) (any, error) { return nil, nil }

func example() {
	jwk := fakeSet{}
	_, _ = jwk.Import("not the real thing")
}
