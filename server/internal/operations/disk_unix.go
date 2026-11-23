//go:build !windows

package operations

import "syscall"

// diskSpace 查询目录所在文件系统的总容量与非特权可用字节数。
func diskSpace(path string) (total uint64, available uint64, ok bool) {
	var info syscall.Statfs_t
	if syscall.Statfs(path, &info) != nil || info.Blocks == 0 || info.Bsize <= 0 {
		return 0, 0, false
	}
	blockSize := uint64(info.Bsize)
	return info.Blocks * blockSize, info.Bavail * blockSize, true
}
