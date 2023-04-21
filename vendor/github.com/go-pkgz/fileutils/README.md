# fileutils [![Build Status](https://github.com/go-pkgz/fileutils/workflows/build/badge.svg)](https://github.com/go-pkgz/fileutils/actions) [![Go Report Card](https://goreportcard.com/badge/github.com/go-pkgz/fileutils)](https://goreportcard.com/report/github.com/go-pkgz/fileutils) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/fileutils/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/fileutils?branch=master)

Package `fileutils` provides useful, high-level file operations.

## Details

- `IsFile` & `IsDir` checks if file/directory exits
- `CopyFile` copies a file from source to destination
- `CopyDir` copies all files recursively from the source to destination directory
- `ListFiles` returns sorted slice of file paths in directory
- `TempFileName` returns a new temporary file name
- `SanitizePath` cleans file path

## Install and update

`go get -u github.com/go-pkgz/fileutils`
