//go:build !windows

package main

import "errors"

func regSetString(hklm bool, path, name, value string) error {
	return errors.New("registry: Windows only")
}

func portOwner(port int) string { return "" }
