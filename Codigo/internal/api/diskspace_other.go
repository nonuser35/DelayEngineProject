//go:build !windows

package api

func diskSpace(path string) (uint64, uint64, bool) {
	return 0, 0, false
}
