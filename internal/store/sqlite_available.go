//go:build sqlite && cgo

package store

func sqliteAvailable() bool {
	return true
}
