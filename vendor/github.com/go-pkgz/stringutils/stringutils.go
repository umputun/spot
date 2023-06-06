package stringutils

import (
	"fmt"
	"reflect"
	"strings"
	"unsafe"
)

// Contains string in slice
func Contains(src string, inSlice []string) bool {
	for _, a := range inSlice {
		if a == src {
			return true
		}
	}
	return false
}

// ContainsAnySubstring checks if string contains any of provided substring
func ContainsAnySubstring(s string, subStrings []string) bool {
	for _, mx := range subStrings {
		if strings.Contains(s, mx) {
			return true
		}
	}
	return false
}

// DeDup remove duplicates from slice. optimized for performance, good for short slices only!
func DeDup(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	l := len(keys) - 1
	for i := 0; i < l; i++ {
		for j := i + 1; j <= l; j++ {
			if keys[i] == keys[j] {
				keys[j] = keys[l]
				keys = keys[0:l]
				l--
				j--
			}
		}
	}
	return keys
}

// DeDupBig remove duplicates from slice. Should be used instead of DeDup for large slices
func DeDupBig(keys []string) (result []string) {
	if len(keys) == 0 {
		return nil
	}
	result = make([]string, 0, len(keys))
	visited := map[string]bool{}
	for _, k := range keys {
		if _, found := visited[k]; !found {
			visited[k] = found
			result = append(result, k)
		}
	}
	return result
}

// SliceToString converts slice of any to slice of string
func SliceToString(s []any) []string {
	if len(s) == 0 {
		return nil
	}
	strSlice := make([]string, len(s))
	for i, v := range s {
		if vb, ok := v.([]byte); ok {
			strSlice[i] = bytesToString(vb)
			continue
		}
		strSlice[i] = fmt.Sprintf("%v", v)
	}
	return strSlice
}

// nolint
func bytesToString(bytes []byte) string {
	sliceHeader := (*reflect.SliceHeader)(unsafe.Pointer(&bytes))
	return *(*string)(unsafe.Pointer(&reflect.StringHeader{
		Data: sliceHeader.Data,
		Len:  sliceHeader.Len,
	}))
}

// HasCommonElement checks if any element of the second slice is in the first slice
func HasCommonElement(a, b []string) bool {
	for _, second := range b {
		for _, first := range a {
			if first == second {
				return true
			}
		}
	}
	return false
}

// HasPrefixSlice checks if any string in the slice starts with the given prefix
func HasPrefixSlice(prefix string, slice []string) bool {
	for _, v := range slice {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}

// HasSuffixSlice checks if any string in the slice ends with the given suffix
func HasSuffixSlice(suffix string, slice []string) bool {
	for _, v := range slice {
		if strings.HasSuffix(v, suffix) {
			return true
		}
	}
	return false
}
