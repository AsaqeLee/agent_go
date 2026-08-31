//go:build !unix

package session

func withFileLock(_ string, fn func() error) error {
	return fn()
}
