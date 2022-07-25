// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type ErrInvalidField struct {
	Key  string
	Kind string
	err  error
}

func (e *ErrInvalidField) Error() string {
	return fmt.Sprintf("converting field \"%s\" to %s failed: %v", e.Key, e.Kind, e.err)
}

type ErrFieldNotFound struct {
	Key string
}

func (e *ErrFieldNotFound) Error() string {
	return fmt.Sprintf("required field \"%s\", but not found", e.Key)
}

type ErrUnsupportedKind struct {
	Key  string
	Kind string
}

func (e *ErrUnsupportedKind) Error() string {
	return fmt.Sprintf("unsupported field key \"%s\" kind: \"%s\"", e.Key, e.Kind)
}

type ErrNilMap struct{}

func (e *ErrNilMap) Error() string {
	return "map is nil"
}

type ErrInvalidReceiver struct{}

func (e *ErrInvalidReceiver) Error() string {
	return "invalid reiciver"
}

/*
UnmarshalMap unmarshal map to struct

Don't support struct as field in the struct. Which means
only support unmarshal only one layer map to one layer struct

Support use struct field's tag name "map" to set unmarshal options.
The options store as comma-separated list. The first element is
to override the default key (the field name) to search value in map.

The option "required", mark the field **must** get value from map or
fill by default value.

The option "default", mark the default value of the field. With
format "default={default_value}"

Examples of struct field tags and their meanings:

	// Field appears in map as key "myName".
	Field int `map:"myName"`

	// Field appears in map as key "myName" and
	// the key "myName" must exists in map
	Field int `map:"myName,required"`

	// Field appears in map as key "myName" and
	// if the key "myName" dose not exists in map
	// the Field value will be Int(123)
	Field int `map:"myName,default=123"`

	// Field appears in map as key "Field" (the default) and
	// the key "Field" must exists in map
	Field int `map:",required"`
*/
func UnmarshalMap(m map[string]string, i interface{}) error {
	if m == nil {
		return &ErrNilMap{}
	}

	rv := reflect.ValueOf(i)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return &ErrInvalidReceiver{}
	}

	// If v is a pointer which point to a pointer
	if rv.Elem().Kind() == reflect.Ptr {
		if rv.Elem().IsNil() {
			i := reflect.New(reflect.TypeOf(i).Elem().Elem())
			rv.Elem().Set(i)
		}

		return UnmarshalMap(m, rv.Elem().Interface())
	}

	if rv.IsValid() {
		t := rv.Type().Elem()
		e := rv.Elem()

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			ef := e.Field(i)

			key, options := parseTag(f.Tag.Get("map"))
			if len(key) == 0 {
				key = f.Name
			}

			_, required := options.Get("required")
			defVal, haveDefVal := options.Get("default")

			value, ok := m[key]
			if !ok {
				if required && !haveDefVal {
					return &ErrFieldNotFound{Key: key}
				}

				if haveDefVal {
					value = defVal
				} else {
					continue
				}
			}

			if ef.Kind() == reflect.Ptr && ef.IsNil() {
				ef.Set(reflect.New(ef.Type().Elem()))
				ef = ef.Elem()
			}

			switch ef.Kind() {
			case reflect.String:
				ef.SetString(value)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				v, err := strconv.ParseInt(value, 10, 64)
				if err != nil || ef.OverflowInt(v) {
					return &ErrInvalidField{Key: key, Kind: ef.Kind().String(), err: err}
				}

				ef.SetInt(v)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				v, err := strconv.ParseUint(value, 10, 64)
				if err != nil || ef.OverflowUint(v) {
					return &ErrInvalidField{Key: key, Kind: ef.Kind().String(), err: err}
				}

				ef.SetUint(v)
			case reflect.Float32, reflect.Float64:
				v, err := strconv.ParseFloat(value, ef.Type().Bits())
				if err != nil || ef.OverflowFloat(v) {
					return &ErrInvalidField{Key: key, Kind: ef.Kind().String(), err: err}
				}

				ef.SetFloat(v)
			case reflect.Bool:
				v, err := strconv.ParseBool(value)
				if err != nil {
					return &ErrInvalidField{Key: key, Kind: ef.Kind().String(), err: err}
				}

				ef.SetBool(v)
			default:
				return &ErrUnsupportedKind{Key: key, Kind: ef.Kind().String()}
			}
		}
	}

	return nil
}

type tagOptions string

// parseTag parse the field tag convert to options
//
// The tag store as a comma-separated list. From the second element
// til the end are the options.
func parseTag(tag string) (string, tagOptions) {
	firstComma := strings.Index(tag, ",")
	if firstComma != -1 {
		return tag[:firstComma], tagOptions(tag[firstComma+1:])
	}

	return tag, tagOptions("")
}

func (t tagOptions) Get(key string) (string, bool) {
	if len(t) == 0 {
		return "", false
	}

	options := strings.Split(string(t), ",")
	for _, o := range options {
		if o == key {
			return "", true
		}

		equalIndex := strings.Index(o, "=")
		if equalIndex != -1 && o[:equalIndex] == key {
			return o[equalIndex+1:], true
		}
	}

	return "", false
}
