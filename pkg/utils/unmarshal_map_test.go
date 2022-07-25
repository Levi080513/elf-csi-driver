// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

func TestParseTag(t *testing.T) {
	cases := []struct {
		tag         string
		wantKey     string
		wantOptions tagOptions
	}{
		{
			tag:         "",
			wantKey:     "",
			wantOptions: tagOptions(""),
		},
		{
			tag:         "testKey",
			wantKey:     "testKey",
			wantOptions: tagOptions(""),
		},
		{
			tag:         "testKey,opt",
			wantKey:     "testKey",
			wantOptions: tagOptions("opt"),
		},
		{
			tag:         "testKey,opt,second_opt=123",
			wantKey:     "testKey",
			wantOptions: tagOptions("opt,second_opt=123"),
		},
	}

	for _, c := range cases {
		key, opt := parseTag(c.tag)
		if key != c.wantKey {
			t.Errorf("want key=\"%s\", get=\"%s\"", c.wantKey, key)
		}

		if opt != c.wantOptions {
			t.Errorf("want options=\"%s\", get=\"%s\"", c.wantOptions, opt)
		}
	}
}

func TestTagOptions(t *testing.T) {
	type wantValue struct {
		value string
		exist bool
	}

	cases := []struct {
		opt  tagOptions
		want map[string]wantValue
	}{
		{
			opt: tagOptions(""),
			want: map[string]wantValue{
				"any": {
					value: "",
					exist: false,
				},
			},
		},
		{
			opt: tagOptions("required"),
			want: map[string]wantValue{
				"required": {
					value: "",
					exist: true,
				},
				"default": {
					value: "",
					exist: false,
				},
			},
		},
		{
			opt: tagOptions("required,default=123"),
			want: map[string]wantValue{
				"required": {
					value: "",
					exist: true,
				},
				"default": {
					value: "123",
					exist: true,
				},
			},
		},
	}

	for _, c := range cases {
		for k, want := range c.want {
			v, ok := c.opt.Get(k)
			if ok != want.exist {
				t.Errorf("want exist=\"%v\", get=\"%v\"", want.exist, ok)
			}

			if v != want.value {
				t.Errorf("want value=\"%s\", get=\"%s\"", want.value, v)
			}
		}
	}
}

type T struct {
	A string
	B int
}

type TT struct {
	A string `map:",required"`
	B int
}

type D struct {
	A string `map:"a,default=abc"`
	B int
	C uint
	D float32
	E bool
}

type X struct {
	A D
	B string
}

type All struct {
	Bool    bool
	Int     int
	Int8    int8
	Int16   int16
	Int32   int32
	Int64   int64
	Uint    uint
	Uint8   uint8
	Uint16  uint16
	Uint32  uint32
	Uint64  uint64
	Uintptr uintptr
	Float32 float32
	Float64 float64

	PBool    *bool
	PInt     *int
	PInt8    *int8
	PInt16   *int16
	PInt32   *int32
	PInt64   *int64
	PUint    *uint
	PUint8   *uint8
	PUint16  *uint16
	PUint32  *uint32
	PUint64  *uint64
	PUintptr *uintptr
	PFloat32 *float32
	PFloat64 *float64

	String  string
	PString *string
}

var allValue = All{
	Bool:    true,
	Int:     2,
	Int8:    3,
	Int16:   4,
	Int32:   5,
	Int64:   6,
	Uint:    7,
	Uint8:   8,
	Uint16:  9,
	Uint32:  10,
	Uint64:  11,
	Uintptr: 12,
	Float32: 13.1,
	Float64: 14.1,
	String:  "15",
}

var pallValue = All{
	PBool:    &allValue.Bool,
	PInt:     &allValue.Int,
	PInt8:    &allValue.Int8,
	PInt16:   &allValue.Int16,
	PInt32:   &allValue.Int32,
	PInt64:   &allValue.Int64,
	PUint:    &allValue.Uint,
	PUint8:   &allValue.Uint8,
	PUint16:  &allValue.Uint16,
	PUint32:  &allValue.Uint32,
	PUint64:  &allValue.Uint64,
	PUintptr: &allValue.Uintptr,
	PFloat32: &allValue.Float32,
	PFloat64: &allValue.Float64,
	PString:  &allValue.String,
}

var allMap = map[string]string{
	"Bool":    "true",
	"Int":     "2",
	"Int8":    "3",
	"Int16":   "4",
	"Int32":   "5",
	"Int64":   "6",
	"Uint":    "7",
	"Uint8":   "8",
	"Uint16":  "9",
	"Uint32":  "10",
	"Uint64":  "11",
	"Uintptr": "12",
	"Float32": "13.1",
	"Float64": "14.1",
	"String":  "15",
}

var pallMap = map[string]string{
	"PBool":    "true",
	"PInt":     "2",
	"PInt8":    "3",
	"PInt16":   "4",
	"PInt32":   "5",
	"PInt64":   "6",
	"PUint":    "7",
	"PUint8":   "8",
	"PUint16":  "9",
	"PUint32":  "10",
	"PUint64":  "11",
	"PUintptr": "12",
	"PFloat32": "13.1",
	"PFloat64": "14.1",
	"PString":  "15",
}

type unmarshalTest struct {
	in  map[string]string
	ptr interface{}
	out interface{}
	err error
}

var unmarshalTests = []unmarshalTest{
	{in: map[string]string{}, ptr: new(T), out: T{}, err: nil},
	{in: map[string]string{}, ptr: new(*T), out: &T{}, err: nil},
	{in: map[string]string{}, ptr: new(TT), out: TT{}, err: &ErrFieldNotFound{Key: "A"}},
	{in: map[string]string{}, ptr: new(D), out: D{A: "abc"}, err: nil},
	{in: map[string]string{"a": "test", "B": "1", "C": "2", "D": "1.1", "E": "true"}, ptr: new(D), out: D{A: "test", B: 1, C: 2, D: 1.1, E: true}, err: nil},
	{in: map[string]string{"A": "", "B": "abc"}, ptr: new(X), out: X{}, err: &ErrUnsupportedKind{Key: "A", Kind: "struct"}},
	{in: nil, ptr: new(X), out: X{}, err: fmt.Errorf("map is nil")},
	{
		in:  map[string]string{"A": "abc", "B": "abc"},
		ptr: new(TT),
		err: &ErrInvalidField{Key: "B", Kind: "int", err: &strconv.NumError{Func: "ParseInt", Num: "abc", Err: strconv.ErrSyntax}},
	},

	{in: allMap, ptr: new(All), out: allValue},
	{in: allMap, ptr: new(*All), out: &allValue},
	{in: pallMap, ptr: new(All), out: pallValue},
	{in: pallMap, ptr: new(*All), out: &pallValue},
}

func TestUnmarshalMap(t *testing.T) {
	for i, tt := range unmarshalTests {
		if tt.ptr == nil {
			continue
		}

		typ := reflect.TypeOf(tt.ptr)
		if typ.Kind() != reflect.Ptr {
			t.Errorf("#%d: unmarshalTest.ptr %T is not a pointer type", i, tt.ptr)
			continue
		}

		typ = typ.Elem()

		v := reflect.New(typ)

		if !reflect.DeepEqual(tt.ptr, v.Interface()) {
			t.Errorf("#%d: unmarshalTest.ptr %#v is not a pointer to a zero value", i, tt.ptr)
			continue
		}

		if err := UnmarshalMap(tt.in, v.Interface()); !equalError(err, tt.err) {
			t.Errorf("#%d: want err: %v, get: %v", i, tt.err, err)
			continue
		} else if err != nil {
			continue
		}

		if !reflect.DeepEqual(v.Elem().Interface(), tt.out) {
			t.Errorf("#%d: mismatch\nwant: %#+v\nhave: %#+v", i, v.Elem().Interface(), tt.out)
			continue
		}
	}
}

func equalError(a, b error) bool {
	if a == nil {
		return b == nil
	}

	if b == nil {
		return a == nil
	}

	return a.Error() == b.Error()
}
