package v1

import "reflect"

// reflectTypeNew returns a pointer to a freshly allocated zero value of
// the given reflect.Type. Used by validateToolConfigFields so that a
// request's tool-config payload is decoded into the right *Params
// struct without switch statements keyed on name.
func reflectTypeNew(t reflect.Type) any {
	return reflect.New(t).Interface()
}
