//go:build !windows

package main

import (
	"os"
	"sync"
	"syscall"
)

func tryCacheLock(path string) (func(), bool, error) {
	if err := cacheRegularOrMissing(path); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, false, nil
		}
		return nil, false, err
	}
	var once sync.Once
	return func() { once.Do(func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }) }, true, nil
}

func cacheVersionCanDelete(target string) bool { return true }
