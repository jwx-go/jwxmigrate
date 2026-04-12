package example

import (
	"errors"
	"fmt"

	"github.com/lestrrat-go/jwx/v3/jwt"
)

func check(tok jwt.Token) error {
	if err := jwt.Validate(tok); err != nil {
		if errors.Is(err, jwt.TokenExpiredError()) {
			return fmt.Errorf("expired: %w", err)
		}
		return err
	}
	return nil
}
