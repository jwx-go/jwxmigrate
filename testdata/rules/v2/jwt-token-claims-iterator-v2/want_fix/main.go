package example

import (
	"fmt"

	"github.com/lestrrat-go/jwx/v4/jwt"
)

// The v2 rule targets specifically `token.Keys()` (the pattern is
// anchored on that exact identifier). Realistic v2 code iterating
// claims called token.Keys() or iterated via a different mechanism.

func dump(token jwt.Token) {
	for _, name := range token.Keys() {
		fmt.Println(name)
	}
}
