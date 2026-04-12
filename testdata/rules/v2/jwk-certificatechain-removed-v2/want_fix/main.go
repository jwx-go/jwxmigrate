package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

// jwk.CertificateChain was removed in v2 in favor of *cert.Chain.
// Surviving v2 code that still references it needs migration.

var _ jwk.CertificateChain
