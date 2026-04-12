package example

import (
	"reflect"

	"github.com/lestrrat-go/jwx/v4/jwk"
)

func init() {
	jwk.RegisterProbeField(reflect.StructField{
		Name: "MyHint",
		Type: reflect.TypeOf(""),
		Tag:  `json:"my_hint"`,
	})
}
