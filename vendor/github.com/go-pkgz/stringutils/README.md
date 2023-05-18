# stringutils [![Build Status](https://github.com/go-pkgz/stringutils/workflows/build/badge.svg)](https://github.com/go-pkgz/stringutils/actions) [![Go Report Card](https://goreportcard.com/badge/github.com/go-pkgz/stringutils)](https://goreportcard.com/report/github.com/go-pkgz/stringutils) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/stringutils/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/stringutils?branch=master)

Package `stringutils` provides useful string operations.

## Details

- **Contains**: checks if slice contains a string.
- **ContainsAnySubstring**: checks if string contains any of provided substring.
- **DeDup**: removes duplicates from slice of strings, optimized for performance, good for short slices only.
- **DeDupBig**: removes duplicates from slice. Should be used instead of `DeDup` for large slices.
- **SliceToString**: converts slice of `any` to a slice of strings.
- **HasCommonElement**: checks if any element of the second slice is in the first slice.
- 
## Install and update

`go get -u github.com/go-pkgz/stringutils`
