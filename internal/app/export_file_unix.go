//go:build !windows

package app

import "os"

func installFileNoReplace(staging, target string) error {
	if err := os.Link(staging, target); err != nil {
		return err
	}
	_ = os.Remove(staging)
	return nil
}

func replaceFile(staging, target string) error {
	return os.Rename(staging, target)
}
