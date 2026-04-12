package example

import (
	"fmt"

	"github.com/lestrrat-go/jwx/v3/jwt"
)

// v3 code commonly enumerated claim names via tok.Keys(). In v4 the
// Token type exposes Claims() iter.Seq2[string, any] directly. The
// rule's search pattern is `\b[A-Za-z_][A-Za-z0-9_]*\.Keys\(\)`; the
// scanner resolves "tok" as a jwt.Token via the parser Obj chain
// (function parameter) and reports the call on that basis.

func dump(tok jwt.Token) {
	for _, name := range tok.Keys() {
		fmt.Println(name)
	}
}
