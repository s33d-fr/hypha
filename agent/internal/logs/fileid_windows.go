//go:build windows

package logs

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// openShared ouvre un journal en partageant aussi la suppression (FILE_SHARE_DELETE) :
// sans cela, l'application qui écrit le journal ne pourrait plus le renommer ni le
// supprimer tant que l'agent le lit. os.Open ne demande pas ce partage.
func openShared(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// fileIdentity : numéro de série du volume et index de fichier NTFS (l'équivalent de l'inode).
func fileIdentity(f *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x:%x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func pathIdentity(path string) (string, error) {
	f, err := openShared(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return fileIdentity(f)
}
