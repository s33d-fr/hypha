//go:build linux

package integrations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sys/unix"

	"s33d.fr/hypha/agent/internal/hostfs"
)

// DialChildCommand est la sous-commande cachée du binaire qui ouvre la socket sous une
// autre identité : « hypha-agent __pg-dial <socket> ».
const DialChildCommand = "__pg-dial"

// executable rend le binaire relancé pour le sous-processus (remplacé dans les tests).
var executable = os.Executable

// peerDialer rend la fonction de connexion utilisée par pgx : la socket Unix est ouverte
// par un sous-processus lancé sous l'uid et le gid de user, qui renvoie le descripteur
// ouvert à l'agent par la socket de contrôle (SCM_RIGHTS). PostgreSQL relève l'identité
// du processus au moment de la connexion (SO_PEERCRED) : il voit user, l'agent garde ses
// droits pour tout le reste, et aucun mot de passe n'est nécessaire.
func peerDialer(user string) (pgconn.DialFunc, error) {
	uid, gid, ok := hostfs.LookupUser(user)
	if !ok {
		return nil, fmt.Errorf("utilisateur système %q introuvable dans %s", user, hostfs.Etc("passwd"))
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "unix" {
			return nil, fmt.Errorf("connexion %s inattendue (socket Unix attendue)", network)
		}
		if os.Geteuid() == uid {
			var d net.Dialer
			return d.DialContext(ctx, "unix", addr)
		}
		return dialAs(ctx, uid, gid, addr)
	}, nil
}

func dialAs(ctx context.Context, uid, gid int, path string) (net.Conn, error) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(pair[0]), "pg-dial")
	defer parent.Close()
	child := os.NewFile(uintptr(pair[1]), "pg-dial-child")

	exe, err := executable()
	if err != nil {
		child.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, DialChildCommand, path)
	cmd.ExtraFiles = []*os.File{child} // descripteur 3 dans le sous-processus
	// Environnement vide : le sous-processus ne doit hériter ni de la clé d'API ni du reste.
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), NoSetGroups: true}}
	err = cmd.Run()
	child.Close()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("ouverture de %s sous l'uid %d : %s", path, uid, msg)
	}
	// Le sous-processus a écrit avant de sortir : la lecture ne bloque pas. S'il n'a rien
	// envoyé, sa sortie a fermé la socket et la lecture rend 0 octet.
	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := unix.Recvmsg(int(parent.Fd()), buf, oob, 0)
	if err != nil {
		return nil, fmt.Errorf("réception de la connexion : %w", err)
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) == 0 {
		return nil, errors.New("aucune connexion reçue du sous-processus")
	}
	fds, err := unix.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) == 0 {
		return nil, errors.New("aucune connexion reçue du sous-processus")
	}
	f := os.NewFile(uintptr(fds[0]), path)
	defer f.Close()
	return net.FileConn(f) // duplique le descripteur ; f peut être fermé
}

// RunDialChild est le corps du sous-processus : ouvrir la socket, renvoyer le descripteur
// à l'agent par le descripteur 3, sortir.
func RunDialChild(path string) error {
	c, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return err
	}
	f, err := c.(*net.UnixConn).File()
	c.Close()
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.Sendmsg(3, []byte{1}, unix.UnixRights(int(f.Fd())), nil, 0)
}
