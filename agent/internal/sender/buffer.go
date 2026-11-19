package sender

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Buffer est le tampon disque des envois en échec : un fichier par corps (déjà
// compressé), nommé par date pour garder l'ordre. Borné à max octets : au-delà, les plus
// anciens partent en premier (les données récentes valent plus pour la supervision).
//
// Format d'un fichier : la route sur la première ligne, puis le corps gzip.
type Buffer struct {
	dir   string
	max   int64
	mu    sync.Mutex
	files []bufFile
	total int64
	seq   atomic.Uint64
}

type bufFile struct {
	name string
	size int64
}

// OpenBuffer ouvre le répertoire et recense les fichiers laissés par une exécution précédente.
func OpenBuffer(dir string, max int64) (*Buffer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	b := &Buffer{dir: dir, max: max}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".gz") {
			if strings.HasSuffix(name, ".tmp") {
				os.Remove(filepath.Join(dir, name)) // écriture interrompue par un arrêt brutal
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		b.files = append(b.files, bufFile{name: name, size: info.Size()})
		b.total += info.Size()
	}
	sort.Slice(b.files, func(i, j int) bool { return b.files[i].name < b.files[j].name })
	return b, nil
}

// Put range un corps ; rend le nombre de corps anciens jetés pour tenir la borne.
func (b *Buffer) Put(route string, gz []byte) (evicted int, err error) {
	size := int64(len(route) + 1 + len(gz))
	if size > b.max {
		return 1, errors.New("corps plus gros que le tampon entier, jeté")
	}
	name := fmt.Sprintf("%020d-%06d.gz", time.Now().UnixNano(), b.seq.Add(1)%1000000)
	path := filepath.Join(b.dir, name)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	w := bufio.NewWriter(f)
	w.WriteString(route)
	w.WriteByte('\n')
	w.Write(gz)
	werr := w.Flush()
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return 0, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.files = append(b.files, bufFile{name: name, size: size})
	b.total += size
	for b.total > b.max && len(b.files) > 1 {
		old := b.files[0]
		b.files = b.files[1:]
		b.total -= old.size
		os.Remove(filepath.Join(b.dir, old.name))
		evicted++
	}
	return evicted, nil
}

// Oldest rend le plus ancien corps tamponné.
func (b *Buffer) Oldest() (name, route string, gz []byte, ok bool) {
	b.mu.Lock()
	if len(b.files) == 0 {
		b.mu.Unlock()
		return "", "", nil, false
	}
	name = b.files[0].name
	b.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(b.dir, name))
	if err != nil {
		b.Remove(name) // fichier disparu ou illisible : on passe au suivant
		return "", "", nil, false
	}
	i := bytes.IndexByte(data, '\n')
	if i <= 0 {
		b.Remove(name)
		return "", "", nil, false
	}
	return name, string(data[:i]), data[i+1:], true
}

// Remove supprime un corps envoyé (ou définitivement refusé).
func (b *Buffer) Remove(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, f := range b.files {
		if f.name == name {
			b.files = append(b.files[:i], b.files[i+1:]...)
			b.total -= f.size
			break
		}
	}
	os.Remove(filepath.Join(b.dir, name))
}

// Stats rend le nombre de corps et d'octets en attente.
func (b *Buffer) Stats() (count int, bytes int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.files), b.total
}
