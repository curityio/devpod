//go:build !windows

package helper

import "syscall"

func setUmask(mask int) {
	syscall.Umask(mask)
}
