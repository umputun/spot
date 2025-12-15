// Package stringutils provides utilities for working with strings.
package stringutils

import (
	"crypto/rand"
	"fmt"
	"math/big"
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
		if mx == "" {
			continue // skip empty substrings
		}
		if strings.Contains(s, mx) {
			return true
		}
	}
	return false
}

// DeDup remove duplicates from slice.
// This function is stable - it preserves the order of first occurrences.
func DeDup(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	result := make([]string, 0, len(keys))
	visited := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if _, found := visited[k]; !found {
			visited[k] = struct{}{}
			result = append(result, k)
		}
	}
	return result
}

// DeDupBig remove duplicates from slice.
// Deprecated: Use DeDup instead. This function now just calls DeDup for backwards compatibility.
func DeDupBig(keys []string) []string {
	return DeDup(keys)
}

// SliceToString converts slice of any to slice of string
func SliceToString(s []any) []string {
	if len(s) == 0 {
		return nil
	}
	strSlice := make([]string, len(s))
	for i, v := range s {
		if vb, ok := v.([]byte); ok {
			strSlice[i] = string(vb) // safe conversion
			continue
		}
		strSlice[i] = fmt.Sprintf("%v", v)
	}
	return strSlice
}

// HasCommonElement checks if any element of the second slice is in the first slice
func HasCommonElement(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	// build set from smaller slice for better performance
	if len(a) > len(b) {
		a, b = b, a
	}
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, y := range b {
		if _, ok := set[y]; ok {
			return true
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

// Truncate cuts string to the given length (in runes) and adds ellipsis if it was truncated
// if maxLen is less than 4 (3 chars for ellipsis + 1 rune from string), returns empty string
func Truncate(s string, maxLen int) string {
	if maxLen < 4 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	return string(runes[:maxLen-3]) + "..."
}

// TruncateWords cuts string to the given number of words and adds ellipsis if it was truncated
// returns empty string if maxWords is 0
func TruncateWords(s string, maxWords int) string {
	if maxWords == 0 {
		return ""
	}

	words := strings.Fields(s)
	if len(words) <= maxWords {
		return s
	}

	return strings.Join(words[:maxWords], " ") + "..."
}

// RandomWord generates pronounceable random word with length between minLen and maxLen
func RandomWord(minLen, maxLen int) string {
	if minLen < 2 {
		minLen = 2
	}
	if maxLen < minLen {
		maxLen = minLen
	}

	vowels := []rune("aeiou")
	consonants := []rune("bcdfghjklmnpqrstvwxyz")

	// make a random length between min and max
	n, err := rand.Int(rand.Reader, big.NewInt(int64(maxLen-minLen+1)))
	length := minLen
	if err == nil {
		length += int(n.Int64())
	}

	var result strings.Builder
	// decide to start with vowel or consonant
	n, _ = rand.Int(rand.Reader, big.NewInt(2))
	startWithVowel := n.Int64() == 0

	for i := 0; i < length; i++ {
		isVowel := (i%2 == 0) == startWithVowel
		if isVowel {
			n, _ = rand.Int(rand.Reader, big.NewInt(int64(len(vowels))))
			result.WriteRune(vowels[n.Int64()])
		} else {
			n, _ = rand.Int(rand.Reader, big.NewInt(int64(len(consonants))))
			result.WriteRune(consonants[n.Int64()])
		}
	}

	return result.String()
}

// Filter returns a new slice containing only elements that match the predicate
func Filter(slice []string, predicate func(string) bool) []string {
	if len(slice) == 0 || predicate == nil {
		return nil
	}
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if predicate(s) {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// Map applies transform function to each element and returns new slice
func Map(slice []string, transform func(string) string) []string {
	if len(slice) == 0 || transform == nil {
		return nil
	}
	result := make([]string, len(slice))
	for i, s := range slice {
		result[i] = transform(s)
	}
	return result
}

// Reverse returns a new slice with elements in reversed order
func Reverse(slice []string) []string {
	if len(slice) == 0 {
		return nil
	}
	result := make([]string, len(slice))
	for i, j := 0, len(slice)-1; i <= j; i, j = i+1, j-1 {
		result[i], result[j] = slice[j], slice[i]
	}
	return result
}

// IndexOf returns the index of the first occurrence of element in slice, or -1 if not found
func IndexOf(slice []string, element string) int {
	for i, s := range slice {
		if s == element {
			return i
		}
	}
	return -1
}

// LastIndexOf returns the index of the last occurrence of element in slice, or -1 if not found
func LastIndexOf(slice []string, element string) int {
	for i := len(slice) - 1; i >= 0; i-- {
		if slice[i] == element {
			return i
		}
	}
	return -1
}

// Difference returns elements that are in the first slice but not in the second
func Difference(a, b []string) []string {
	if len(a) == 0 {
		return nil
	}
	if len(b) == 0 {
		return a
	}

	// build set from b for O(1) lookups
	bSet := make(map[string]struct{}, len(b))
	for _, s := range b {
		bSet[s] = struct{}{}
	}

	result := make([]string, 0, len(a))
	for _, s := range a {
		if _, found := bSet[s]; !found {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// Union combines multiple slices and removes duplicates, preserving order
func Union(slices ...[]string) []string {
	if len(slices) == 0 {
		return nil
	}

	// estimate capacity
	totalLen := 0
	for _, slice := range slices {
		totalLen += len(slice)
	}
	if totalLen == 0 {
		return nil
	}

	seen := make(map[string]struct{}, totalLen)
	result := make([]string, 0, totalLen)

	for _, slice := range slices {
		for _, s := range slice {
			if _, found := seen[s]; !found {
				seen[s] = struct{}{}
				result = append(result, s)
			}
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// Intersection returns elements that are present in both slices, preserving order from first slice
func Intersection(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	// build set from b for O(1) lookups
	bSet := make(map[string]struct{}, len(b))
	for _, s := range b {
		bSet[s] = struct{}{}
	}

	result := make([]string, 0, len(a))
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		if _, inB := bSet[s]; inB {
			if _, alreadyAdded := seen[s]; !alreadyAdded {
				seen[s] = struct{}{}
				result = append(result, s)
			}
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// NormalizeWhitespace replaces multiple whitespace characters with single space and trims
func NormalizeWhitespace(s string) string {
	if s == "" {
		return ""
	}

	// use Fields to split on any whitespace, then rejoin with single space
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

// IsBlank returns true if string is empty or contains only whitespace
func IsBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// RemovePrefix removes the prefix from s if present, otherwise returns s unchanged
func RemovePrefix(s, prefix string) string {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// RemoveSuffix removes the suffix from s if present, otherwise returns s unchanged
func RemoveSuffix(s, suffix string) string {
	if strings.HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}
