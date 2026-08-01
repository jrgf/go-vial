package vial

import (
	"reflect"
	"testing"
)

func TestBindingMetadataIsCached(t *testing.T) {
	type input struct {
		ID   int    `path:"id"`
		Name string `query:"name"`
	}

	valueType := reflect.TypeOf(input{})
	bindingMetadataCache.Delete(valueType)
	first := bindingMetadataFor(valueType)
	second := bindingMetadataFor(valueType)
	if first != second {
		t.Fatal("binding metadata was rebuilt")
	}
}
