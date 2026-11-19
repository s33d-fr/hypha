//go:build linux

package integrations

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"s33d.fr/hypha/agent/internal/hostfs"
)

// Le binaire de test joue aussi le sous-processus : « <binaire> __pg-dial <socket> ».
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == DialChildCommand {
		if err := RunDialChild(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// La connexion ouverte par le sous-processus porte l'identité demandée (ce que relève
// PostgreSQL pour l'authentification peer), et l'agent peut s'en servir.
func TestPeerDialPassesCredentials(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changement d'identité : root requis")
	}
	uid, gid, ok := hostfs.LookupUser("nobody")
	if !ok {
		t.Skip("compte nobody absent")
	}
	// Répertoire et binaire accessibles à nobody (les répertoires temporaires sont en 700).
	dir := t.TempDir()
	os.Chmod(filepath.Dir(dir), 0o755)
	os.Chmod(dir, 0o755)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "child.test")
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	executable = func() (string, error) { return bin, nil }
	defer func() { executable = os.Executable }()

	sock := filepath.Join(dir, ".s.PGSQL.5432")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	os.Chmod(sock, 0o777)

	type peer struct {
		uid  uint32
		data string
		err  error
	}
	got := make(chan peer, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- peer{err: err}
			return
		}
		defer c.Close()
		raw, _ := c.(*net.UnixConn).SyscallConn()
		var cred *unix.Ucred
		raw.Control(func(fd uintptr) { cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
		if err != nil {
			got <- peer{err: err}
			return
		}
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 4)
		_, err = io.ReadFull(c, buf)
		got <- peer{uid: cred.Uid, data: string(buf), err: err}
	}()

	dial, err := peerDialer("nobody")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dial(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	p := <-got
	if p.err != nil {
		t.Fatal(p.err)
	}
	if int(p.uid) != uid || p.data != "ping" {
		t.Fatalf("pair vu par le serveur : uid %d (attendu %d, gid %d), données %q", p.uid, uid, gid, p.data)
	}
}
