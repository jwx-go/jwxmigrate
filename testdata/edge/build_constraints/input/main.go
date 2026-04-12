//go:build !windows

package example

import (
	"github.com/lestrrat-go/jwx/v3/jws"
)

// A file with a build constraint should still be scanned and fixed; the
// constraint must be preserved verbatim through the gofmt pass.

var _ jws.Signer2 = nil
