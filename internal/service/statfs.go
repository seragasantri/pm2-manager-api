package service

import "syscall"

// statfs mengembalikan total & terpakai (byte) untuk satu mount point.
func statfs(path string) (total, used uint64, ok bool) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, false
	}
	total = fs.Blocks * uint64(fs.Bsize)
	free := fs.Bavail * uint64(fs.Bsize)
	return total, total - free, true
}
