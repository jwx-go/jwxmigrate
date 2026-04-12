package example

import (
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jws"
)

// Behavioral rule: jws.WithKey() now validates algorithm/key compatibility
// at construction time instead of at Sign/Verify. The rule has no
// search_patterns, so Check reports nothing for it — the rule is a pure
// advisory for humans reading the migration notes.

func signPayload(key any, payload []byte) ([]byte, error) {
	return jws.Sign(payload, jws.WithKey(jwa.RS256, key))
}
