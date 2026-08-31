//go:build !unix

package agent

func withFileLock(_ string, fn func() error) error {
	return fn()
}
