//go:build windows

package operations

import (
	"syscall"
	"unsafe"
)

var (
	kernel32DLL       = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeEx = kernel32DLL.NewProc("GetDiskFreeSpaceExW")
)

// diskSpace 在 Windows 上通过 GetDiskFreeSpaceExW 查询目录所在卷的总容量与可用字节数。
func diskSpace(path string) (total uint64, available uint64, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64
	ret, _, _ := procGetDiskFreeEx.Call(
		uintptr(unsafe.Pointer(pointer)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if ret == 0 || totalNumberOfBytes == 0 {
		return 0, 0, false
	}
	return totalNumberOfBytes, freeBytesAvailable, true
}
