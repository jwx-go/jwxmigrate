package example

import "github.com/lestrrat-go/jwx/v4/transform"

func useTransform(m transform.Mappable) error {
	dst := map[string]any{}
	return transform.AsMap(m, dst)
}
