package example

import (
	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
)

// Realistic v3 legacy signer / verifier registration. One fixture fires
// every rule in the factory/adapter subsystem:
//
//   - jws-register-signer            — jws.RegisterSigner(...)
//   - jws-register-verifier          — jws.RegisterVerifier(...)
//   - jws-signerfactory-removed      — jws.SignerFactory   (type reference)
//   - jws-signerfactoryfn-removed    — jws.SignerFactoryFn(fn)
//   - jws-signeradapter-removed      — jws.SignerAdapter(v)
//   - jws-verifierfactory-removed    — jws.VerifierFactory (type reference)
//   - jws-verifierfactoryfn-removed  — jws.VerifierFactoryFn(fn)
//   - jws-verifideradapter-removed   — jws.VerifierAdapter(v)
//   - jws-withlegacysigners-removed  — jws.WithLegacySigners()
//   - jws-signer2-to-signer          — jws.Signer2   return type
//   - jws-verifier2-to-verifier      — jws.Verifier2 return type
//
// Note: the fix pass does not delete the FactoryFn/Adapter calls from
// inside the return/composite expressions. kindRemoved's deletion path
// only targets ExprStmt / AssignStmt parents; calls nested inside other
// expressions are reported but not rewritten.

type legacySigner struct{}

func (legacySigner) Algorithm() jwa.SignatureAlgorithm { return jwa.RS256() }
func (legacySigner) Sign([]byte, any) ([]byte, error)  { return nil, nil }

type legacyVerifier struct{}

func (legacyVerifier) Algorithm() jwa.SignatureAlgorithm { return jwa.RS256() }
func (legacyVerifier) Verify([]byte, []byte, any) error  { return nil }

func registerLegacy() {
	var factory jws.SignerFactory = jws.SignerFactoryFn(func() (jws.Signer, error) {
		return jws.SignerAdapter(legacySigner{}), nil
	})
	_ = jws.RegisterSigner(jwa.RS256(), factory)

	var vf jws.VerifierFactory = jws.VerifierFactoryFn(func() (jws.Verifier, error) {
		return jws.VerifierAdapter(legacyVerifier{}), nil
	})
	_ = jws.RegisterVerifier(jwa.RS256(), vf)
}

func verifyWithLegacy(tok []byte) ([]byte, error) {
	return jws.Verify(tok, jws.WithLegacySigners())
}
