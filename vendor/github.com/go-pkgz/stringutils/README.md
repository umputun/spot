# stringutils [![Build Status](https://github.com/go-pkgz/stringutils/workflows/build/badge.svg)](https://github.com/go-pkgz/stringutils/actions) [![Go Report Card](https://goreportcard.com/badge/github.com/go-pkgz/stringutils)](https://goreportcard.com/report/github.com/go-pkgz/stringutils) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/stringutils/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/stringutils?branch=master)

Package `stringutils` provides useful string operations.

## Details

### Slice Operations

- **Contains**: checks if slice contains a string.
- **DeDup**: removes duplicates from slice of strings while preserving order (stable).
- **DeDupBig**: deprecated alias for `DeDup`, kept for backwards compatibility.
- **SliceToString**: converts slice of `any` to a slice of strings.
- **Filter**: returns a new slice containing only elements that match the predicate function.
- **Map**: applies a transform function to each element and returns a new slice.
- **Reverse**: returns a new slice with elements in reversed order.
- **IndexOf**: returns the index of the first occurrence of element in slice, or -1 if not found.
- **LastIndexOf**: returns the index of the last occurrence of element in slice, or -1 if not found.

### Set Operations

- **HasCommonElement**: checks if any element of the second slice is in the first slice.
- **Difference**: returns elements that are in the first slice but not in the second.
- **Union**: combines multiple slices and removes duplicates, preserving order.
- **Intersection**: returns elements that are present in both slices, preserving order from first slice.

### String Checking

- **ContainsAnySubstring**: checks if string contains any of provided substrings.
- **HasPrefixSlice**: checks if any string in the slice starts with the given prefix.
- **HasSuffixSlice**: checks if any string in the slice ends with the given suffix.
- **IsBlank**: returns true if string is empty or contains only whitespace.

### String Manipulation

- **Truncate**: cuts string to the given length (in runes) and adds ellipsis if it was truncated.
- **TruncateWords**: cuts string to the given number of words and adds ellipsis if it was truncated.
- **NormalizeWhitespace**: replaces multiple whitespace characters with single space and trims.
- **RemovePrefix**: removes the prefix from string if present, otherwise returns unchanged.
- **RemoveSuffix**: removes the suffix from string if present, otherwise returns unchanged.

### String Generation

- **RandomWord**: generates pronounceable random word with given min/max length.

## Install and update

`go get -u github.com/go-pkgz/stringutils`

## Usage examples