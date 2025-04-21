# fileutils [![Build Status](https://github.com/go-pkgz/fileutils/workflows/build/badge.svg)](https://github.com/go-pkgz/fileutils/actions) [![Go Report Card](https://goreportcard.com/badge/github.com/go-pkgz/fileutils)](https://goreportcard.com/report/github.com/go-pkgz/fileutils) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/fileutils/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/fileutils?branch=master)

Package `fileutils` provides useful, high-level file operations.

## Details

- `IsFile` & `IsDir` checks if file/directory exists
- `CopyFile` copies a file from source to destination, preserving mode
- `CopyDir` copies all files recursively from the source to destination directory
- `MoveFile` moves a file, using atomic rename when possible with copy+delete fallback
- `ListFiles` returns sorted slice of file paths in directory
- `TempFileName` returns a new temporary file name using secure random generation
- `SanitizePath` cleans file path
- `TouchFile` creates an empty file or updates timestamps of existing one
- `Checksum` calculates file checksum using various hash algorithms (MD5, SHA1, SHA256, etc.)
- `FileWatcher` watches files or directories for changes
- `WatchRecursive` watches a directory recursively for changes

## Usage Examples

### File Operations

```go
// Copy a file
err := fileutils.CopyFile("source.txt", "destination.txt")
if err != nil {
    log.Fatalf("Failed to copy file: %v", err)
}

// Move a file
err = fileutils.MoveFile("source.txt", "destination.txt")
if err != nil {
    log.Fatalf("Failed to move file: %v", err)
}

// Check if a file or directory exists
if fileutils.IsFile("file.txt") {
    fmt.Println("File exists")
}
if fileutils.IsDir("directory") {
    fmt.Println("Directory exists")
}

// Generate a temporary file name
tempName, err := fileutils.TempFileName("/tmp", "prefix-*.ext")
if err != nil {
    log.Fatalf("Failed to generate temp file name: %v", err)
}
fmt.Println("Temp file:", tempName)
```

### File Checksum

```go
// Calculate MD5 checksum
md5sum, err := fileutils.Checksum("path/to/file", enum.HashAlgMD5)
if err != nil {
    log.Fatalf("Failed to calculate MD5: %v", err)
}
fmt.Printf("MD5: %s\n", md5sum)

// Calculate SHA256 checksum
sha256sum, err := fileutils.Checksum("path/to/file", enum.HashAlgSHA256)
if err != nil {
    log.Fatalf("Failed to calculate SHA256: %v", err)
}
fmt.Printf("SHA256: %s\n", sha256sum)
```

### File Watcher

```go
// Create a simple file watcher
watcher, err := fileutils.NewFileWatcher("/path/to/file", func(event FileEvent) {
    fmt.Printf("Event: %s, Path: %s\n", event.Type, event.Path)
})
if err != nil {
    log.Fatalf("Failed to create watcher: %v", err)
}
defer watcher.Close()

// Watch a directory recursively
watcher, err := fileutils.WatchRecursive("/path/to/dir", func(event FileEvent) {
    fmt.Printf("Event: %s, Path: %s\n", event.Type, event.Path)
})
if err != nil {
    log.Fatalf("Failed to create watcher: %v", err)
}
defer watcher.Close()

// Add another path to an existing watcher
err = watcher.AddPath("/path/to/another/file")

// Remove a path from the watcher
err = watcher.RemovePath("/path/to/file")
```

## Install and update

`go get -u github.com/go-pkgz/fileutils`
