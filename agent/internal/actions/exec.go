package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// limitedBuffer garde le début de la sortie (place laissée pour la mention de coupure,
// le tout sous MaxOutput) et compte le reste.
type limitedBuffer struct {
	mu      sync.Mutex
	b       bytes.Buffer
	dropped int
}

const keepOutput = MaxOutput - 96

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := min(max(keepOutput-l.b.Len(), 0), len(p))
	l.b.Write(p[:n])
	l.dropped += len(p) - n
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := strings.ToValidUTF8(l.b.String(), "")
	if l.dropped > 0 {
		s += fmt.Sprintf("\n[sortie coupée : %d octets de plus]", l.dropped)
	}
	return s
}

// cleanEnv : environnement de l'agent sans ses variables (la clé d'API ne passe jamais à
// un script ni à une commande lancée par une action).
func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "HYPHA_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runCommand lance une commande avec délai, sortie bornée et environnement nettoyé ; rend le
// code de sortie (-1 si la commande n'a pas pu tourner jusqu'au bout) et la sortie.
func runCommand(ctx context.Context, name string, args ...string) (int, string) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = cleanEnv()
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	prepareCmd(cmd)
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	text := out.String()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, text
	case ctx.Err() != nil:
		return -1, strings.TrimRight(text, "\n") + "\n[délai dépassé : commande arrêtée]"
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return ee.ExitCode(), text
	}
	return -1, strings.TrimRight(text+"\n"+err.Error(), "\n")
}
