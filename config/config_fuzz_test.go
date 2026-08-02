package config

import (
	"reflect"
	"testing"
	"time"
)

func FuzzSetText(fuzz *testing.F) {
	fuzz.Add(uint8(0), "value")
	fuzz.Add(uint8(1), "false")
	fuzz.Add(uint8(2), "42")
	fuzz.Add(uint8(3), "1.5s")

	fuzz.Fuzz(func(t *testing.T, kind uint8, value string) {
		var destination any
		switch kind % 6 {
		case 0:
			destination = new(string)
		case 1:
			destination = new(bool)
		case 2:
			destination = new(int8)
		case 3:
			destination = new(uint8)
		case 4:
			destination = new(float32)
		case 5:
			destination = new(time.Duration)
		}
		err := setText(reflect.ValueOf(destination).Elem(), value)
		if text, ok := destination.(*string); ok && (err != nil || *text != value) {
			t.Fatalf("string configuration = %q, %v", *text, err)
		}
	})
}
