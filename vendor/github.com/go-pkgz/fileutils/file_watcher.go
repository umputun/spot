package fileutils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"

	"github.com/go-pkgz/fileutils/enum"
)

//go:generate enum -type=eventType -path=enum

// eventType represents the type of file system event
//
//nolint:unused // This type is used by the enum generator
type eventType int

// Event types
//
//nolint:unused // These constants are used by the enum generator
const (
	eventTypeCreate eventType = iota + 1
	eventTypeWrite
	eventTypeRemove
	eventTypeRename
	eventTypeChmod
)

// FileEvent represents a file system event
type FileEvent struct {
	Path string         // path to the file or directory
	Type enum.EventType // type of event
}

// FileWatcher watches for file system events
type FileWatcher struct {
	watcher  *fsnotify.Watcher
	callback func(FileEvent)
	done     chan struct{}
}

// NewFileWatcher creates a new file watcher for the specified path
func NewFileWatcher(path string, callback func(FileEvent)) (*FileWatcher, error) {
	if path == "" {
		return nil, errors.New("empty path")
	}

	if callback == nil {
		return nil, errors.New("callback function is required")
	}

	// check if path exists
	if !IsFile(path) && !IsDir(path) {
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	// create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	// add path to watcher
	if err := watcher.Add(path); err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("failed to watch path %s: %w", path, err)
	}

	fw := &FileWatcher{
		watcher:  watcher,
		callback: callback,
		done:     make(chan struct{}),
	}

	// start watching in a goroutine
	go fw.watch()

	return fw, nil
}

// watch processes events from the fsnotify watcher
func (fw *FileWatcher) watch() {
	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			// convert fsnotify event to our event type
			var eventType enum.EventType
			switch {
			case event.Has(fsnotify.Create):
				eventType = enum.EventTypeCreate
			case event.Has(fsnotify.Write):
				eventType = enum.EventTypeWrite
			case event.Has(fsnotify.Remove):
				eventType = enum.EventTypeRemove
			case event.Has(fsnotify.Rename):
				eventType = enum.EventTypeRename
			case event.Has(fsnotify.Chmod):
				eventType = enum.EventTypeChmod
			default:
				continue // unknown event type
			}

			// call the callback with the event
			fw.callback(FileEvent{
				Path: event.Name,
				Type: eventType,
			})

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			// log error or call error callback
			fmt.Printf("error: %v\n", err)

		case <-fw.done:
			return
		}
	}
}

// Close stops watching and releases resources
func (fw *FileWatcher) Close() error {
	close(fw.done)
	return fw.watcher.Close()
}

// AddPath adds a path to the watcher
func (fw *FileWatcher) AddPath(path string) error {
	if path == "" {
		return errors.New("empty path")
	}

	// check if path exists
	if !IsFile(path) && !IsDir(path) {
		return fmt.Errorf("path does not exist: %s", path)
	}

	return fw.watcher.Add(path)
}

// RemovePath removes a path from the watcher
func (fw *FileWatcher) RemovePath(path string) error {
	if path == "" {
		return errors.New("empty path")
	}

	return fw.watcher.Remove(path)
}

// WatchRecursive watches a directory recursively
func WatchRecursive(dir string, callback func(FileEvent)) (*FileWatcher, error) {
	if dir == "" {
		return nil, errors.New("empty directory path")
	}

	if callback == nil {
		return nil, errors.New("callback function is required")
	}

	// check if directory exists
	if !IsDir(dir) {
		return nil, fmt.Errorf("directory does not exist: %s", dir)
	}

	// create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	// create file watcher
	fw := &FileWatcher{
		watcher:  watcher,
		callback: callback,
		done:     make(chan struct{}),
	}

	// add all subdirectories
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := watcher.Add(path); err != nil {
				return fmt.Errorf("failed to watch directory %s: %w", path, err)
			}
		}
		return nil
	})

	if err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("failed to set up recursive watching: %w", err)
	}

	// start watching in a goroutine
	go fw.watch()

	return fw, nil
}
