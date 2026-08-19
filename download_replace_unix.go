//go:build !windows

package main

import "os"

func replaceDownloadedFile(source, destination string) error {
	return os.Rename(source, destination)
}
