package example

import (
	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
)

// Realistic v2 legacy signer / verifier registration. Covers the seven
// factory/adapter rules that are exempt from per-rule fixtures because
// individual stubs for each symbol would test nothing:
//
//   - jws-signerfactory-removed-v2     — jws.SignerFactory   (type reference)
//   - jws-signerfactoryfn-removed-v2   — jws.SignerFactoryFn(fn)
//   - jws-signeradapter-removed-v2     — jws.SignerAdapter(v)
//   - jws-verifierfactory-removed-v2   — jws.VerifierFactory (type reference)
//   - jws-verifierfactoryfn-removed-v2 — jws.VerifierFactoryFn(fn)
//   - jws-verifideradapter-removed-v2  — jws.VerifierAdapter(v)
//   - jws-withlegacysigners-removed-v2 — jws.WithLegacySigners()

type legacySigner struct{}

func (legacySigner) Algorithm() jwa.SignatureAlgorithm { return jwa.RS256 }
func (legacySigner) Sign([]byte, any) ([]byte, error)  { return nil, nil }

type legacyVerifier struct{}

func (legacyVerifier) Algorithm() jwa.SignatureAlgorithm { return jwa.RS256 }
func (legacyVerifier) Verify([]byte, []byte, any) error  { return nil }

func registerLegacy() {
	var factory jws.SignerFactory = jws.SignerFactoryFn(func() (jws.Signer, error) {
		return jws.SignerAdapter(legacySigner{}), nil
	})
	_ = factory

	var vf jws.VerifierFactory = jws.VerifierFactoryFn(func() (jws.Verifier, error) {
		return jws.VerifierAdapter(legacyVerifier{}), nil
	})
	_ = vf
}

func verifyWithLegacy(tok []byte) ([]byte, error) {
	return jws.Verify(tok, jws.WithLegacySigners())
}
