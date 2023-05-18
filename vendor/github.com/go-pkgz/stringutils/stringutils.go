package stringutils

import (
	"fmt"
	"strings"
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
	strSlice := make([]string, len(s))
	for i, v := range s {
		strSlice[i] = fmt.Sprintf("%v", v)
	}
	return strSlice
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
