package example

import (
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func load(path string, opts ...jwt.ReadFileOption) error {
	_, err := jwt.ReadFile(path, opts...)
	return err
}
