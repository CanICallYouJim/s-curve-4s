//go:build !darwin

package main

// Windows probes the candidate executable before deleting its directory.
func cacheRunningExecutables(root string) ([]string, error) { return nil, nil }
