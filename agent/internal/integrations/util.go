package integrations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// findTool cherche un outil dans le PATH, puis dans les dossiers d'administration (le PATH
// d'un service ne contient pas toujours /usr/sbin).
func findTool(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if runtime.GOOS == "windows" {
		return "", false
	}
	for _, d := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin", "/usr/bin", "/usr/local/bin"} {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// runTool lance un outil (délai borné, sortie bornée à 32 Mio, langue C) ; une sortie non
// nulle est rendue avec l'erreur (certains outils écrivent leur réponse puis sortent en 1).
func runTool(ctx context.Context, timeout time.Duration, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var out, stderr bytes.Buffer
	cmd.Stdout = &limited{w: &out, n: 32 << 20}
	cmd.Stderr = &limited{w: &stderr, n: 64 << 10}
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			msg = "délai dépassé"
		}
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		if msg != "" {
			err = fmt.Errorf("%s : %s", filepath.Base(path), msg)
		} else {
			err = fmt.Errorf("%s : %w", filepath.Base(path), err)
		}
	}
	return out.Bytes(), err
}

type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	k := min(len(p), l.n)
	l.n -= k
	l.w.Write(p[:k])
	return len(p), nil
}

// httpGet lit une page locale (délai 5 s, 16 Mio au plus).
func httpGet(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hypha-agent")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s : HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

var errNoData = errors.New("aucune donnée")

// cache garde le dernier relevé d'une intégration chère (lu toutes les refresh, rendu à
// chaque passage).
type cache[T any] struct {
	at    time.Time
	value T
	err   error
}

func (c *cache[T]) get(now time.Time, refresh time.Duration, read func() (T, error)) (T, error) {
	if c.at.IsZero() || now.Sub(c.at) >= refresh {
		c.value, c.err = read()
		c.at = now
	}
	return c.value, c.err
}
