// Package deepcopy makes deep copies of things. A standard copy will copy the
// pointers: deep copy copies the values pointed to.  Unexported field
// values are not copied.
//
// Copyright (c)2014-2016, Joel Scoble (github.com/mohae), all rights reserved.
// License: MIT, for more details check the included LICENSE file.
package deepcopy

import (
	"reflect"
	"time"
)

// Interface for delegating copy process to type
type Interface interface {
	DeepCopy() any
}

// Iface is an alias to Copy; this exists for backwards compatibility reasons.
func Iface(iface any) any {
	return Copy(iface)
}

// Copy creates a deep copy of whatever is passed to it and returns the copy
// in an any.  The returned value will need to be asserted to the
// correct type.
func Copy(src any) any {
	if src == nil {
		return nil
	}

	// make the interface a reflect.Value
	original := reflect.ValueOf(src)

	// make a copy of the same type as the original.
	cpy := reflect.New(original.Type()).Elem()

	// recursively copy the original.
	copyRecursive(original, cpy)

	// return the copy as an interface.
	return cpy.Interface()
}

// copyRecursive does the actual copying of the interface. It currently has
// limited support for what it can handle. Add as needed.
func copyRecursive(original, cpy reflect.Value) {
	// check for implement deepcopy.Interface
	if original.CanInterface() {
		if copier, ok := original.Interface().(Interface); ok {
			cpy.Set(reflect.ValueOf(copier.DeepCopy()))
			return
		}
	}

	// handle according to original's Kind
	switch original.Kind() {
	case reflect.Ptr:
		// get the actual value being pointed to.
		originalValue := original.Elem()

		// if  it isn't valid, return.
		if !originalValue.IsValid() {
			return
		}
		cpy.Set(reflect.New(originalValue.Type()))
		copyRecursive(originalValue, cpy.Elem())

	case reflect.Interface:
		// if this is a nil, don't do anything
		if original.IsNil() {
			return
		}
		// get the value for the interface, not the pointer.
		originalValue := original.Elem()

		// get the value by calling Elem().
		copyValue := reflect.New(originalValue.Type()).Elem()
		copyRecursive(originalValue, copyValue)
		cpy.Set(copyValue)

	case reflect.Struct:
		t, ok := original.Interface().(time.Time)
		if ok {
			cpy.Set(reflect.ValueOf(t))
			return
		}
		// go through each field of the struct and copy it.
		for i := 0; i < original.NumField(); i++ {
			// the Type's StructField for a given field is checked to see if StructField.PkgPath
			// is set to determine if the field is exported or not because CanSet() returns false
			// for settable fields.  I'm not sure why.  -mohae
			if original.Type().Field(i).PkgPath != "" {
				continue
			}
			copyRecursive(original.Field(i), cpy.Field(i))
		}

	case reflect.Slice:
		if original.IsNil() {
			return
		}
		// make a new slice and copy each element.
		cpy.Set(reflect.MakeSlice(original.Type(), original.Len(), original.Cap()))
		for i := 0; i < original.Len(); i++ {
			copyRecursive(original.Index(i), cpy.Index(i))
		}

	case reflect.Map:
		if original.IsNil() {
			return
		}
		cpy.Set(reflect.MakeMap(original.Type()))
		for _, key := range original.MapKeys() {
			originalValue := original.MapIndex(key)
			copyValue := reflect.New(originalValue.Type()).Elem()
			copyRecursive(originalValue, copyValue)
			copyKey := Copy(key.Interface())
			cpy.SetMapIndex(reflect.ValueOf(copyKey), copyValue)
		}

	default:
		cpy.Set(original)
	}
}
