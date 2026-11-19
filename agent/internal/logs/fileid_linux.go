//go:build linux

package logs

import (
	"fmt"
	"os"
	"syscall"
)

// openShared ouvre un journal en lecture. Sous Linux, garder le fichier ouvert n'empêche
// ni sa rotation ni sa suppression.
func openShared(path string) (*os.File, error) { return os.Open(path) }

// fileIdentity : périphérique et inode ; un fichier renommé garde son inode, un fichier
// recréé sous le même nom en change.
func fileIdentity(f *os.File) (string, error) {
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	return statID(fi)
}

func pathIdentity(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return statID(fi)
}

func statID(fi os.FileInfo) (string, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("identité de fichier indisponible")
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
