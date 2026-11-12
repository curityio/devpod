package helper

// setUmask does nothing on Windows, which has no umask.
func setUmask(int) {}
