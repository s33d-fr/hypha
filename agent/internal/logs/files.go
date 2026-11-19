package logs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/state"
)

const (
	filesStateName = "logs_files.json"
	pollEvery      = time.Second
	globEvery      = 10 * time.Second
	readPerPoll    = 1 << 20 // au plus 1 Mio lu par fichier et par seconde : un fichier
	// énorme découvert d'un coup ne monopolise ni la mémoire ni le CPU
	partialFlushAfter = 10 * time.Second // ligne sans fin de ligne envoyée telle quelle après ce délai
	onceTailBytes     = 16 << 10
	onceTailLines     = 20
)

// filePos est la position durable d'un fichier : son identité (inode, ou index de fichier
// NTFS) et le décalage après la dernière ligne en sécurité.
type filePos struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
}

type tailedFile struct {
	path     string
	src      config.FileSource
	f        *os.File
	id       string
	offset   int64 // décalage après la dernière ligne complète lue
	partial  []byte
	partSeen time.Time
	skipping bool // ligne trop longue : on saute jusqu'à la prochaine fin de ligne
}

// Files suit les fichiers désignés par logs.files.
type Files struct {
	sources []config.FileSource
	pipe    *Pipeline
	store   *state.Store
	saver   *state.Saver
	log     *slog.Logger
	setStat func(string)

	mu       sync.Mutex
	registry map[string]filePos // positions en sécurité (sous mu)
	files    map[string]*tailedFile
}

// NewFiles prépare le suivi.
func NewFiles(sources []config.FileSource, pipe *Pipeline, store *state.Store, log *slog.Logger, setStat func(string)) *Files {
	f := &Files{sources: sources, pipe: pipe, store: store, log: log, setStat: setStat,
		registry: map[string]filePos{}, files: map[string]*tailedFile{}}
	f.saver = state.NewSaver(store, filesStateName, f.snapshot, func(err error) {
		log.Warn("positions des fichiers suivis non écrites", "err", err)
	})
	return f
}

func (f *Files) snapshot() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]filePos, len(f.registry))
	for k, v := range f.registry {
		out[k] = v
	}
	return out
}

func (f *Files) commit(path, id string, offset int64) {
	f.mu.Lock()
	f.registry[path] = filePos{ID: id, Offset: offset}
	f.mu.Unlock()
	f.saver.MarkDirty()
}

// Run suit les fichiers jusqu'à l'arrêt.
func (f *Files) Run(ctx context.Context) {
	if err := f.store.ReadJSON(filesStateName, &f.registry); err != nil && !errors.Is(err, os.ErrNotExist) {
		f.log.Warn("positions des fichiers illisibles : reprise à la fin des fichiers", "err", err)
		f.registry = map[string]filePos{}
	}
	go f.saver.Run(ctx)
	f.glob(true)
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	lastGlob := time.Now()
	for {
		select {
		case <-ctx.Done():
			for _, tf := range f.files {
				tf.f.Close()
			}
			return
		case now := <-poll.C:
			if now.Sub(lastGlob) >= globEvery {
				f.glob(false)
				lastGlob = now
			}
			for path, tf := range f.files {
				if !f.poll(ctx, tf, now) {
					tf.f.Close()
					delete(f.files, path)
				}
			}
			f.setStat(statusFiles(len(f.files)))
		}
	}
}

func statusFiles(n int) string {
	if n == 0 {
		return "ok (aucun fichier ne correspond pour l'instant)"
	}
	return "ok"
}

// glob ouvre les fichiers nouvellement désignés. Au démarrage, un fichier inconnu du
// registre est pris à la fin (on n'envoie pas l'historique d'un journal existant) ; un
// fichier apparu ensuite est lu depuis le début (il vient d'être créé).
func (f *Files) glob(startup bool) {
	for _, src := range f.sources {
		matches, err := filepath.Glob(src.Path)
		if err != nil {
			continue
		}
		for _, path := range matches {
			if _, open := f.files[path]; open {
				continue
			}
			if fi, err := os.Stat(path); err != nil || fi.IsDir() {
				continue
			}
			tf, err := f.open(path, src, startup)
			if err != nil {
				f.log.Warn("fichier journal illisible", "fichier", path, "err", err)
				continue
			}
			f.files[path] = tf
		}
	}
}

func (f *Files) open(path string, src config.FileSource, startup bool) (*tailedFile, error) {
	fh, err := openShared(path)
	if err != nil {
		return nil, err
	}
	id, err := fileIdentity(fh)
	if err != nil {
		fh.Close()
		return nil, err
	}
	fi, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, err
	}
	f.mu.Lock()
	pos, known := f.registry[path]
	f.mu.Unlock()
	var offset int64
	switch {
	case known && pos.ID == id && pos.Offset <= fi.Size():
		offset = pos.Offset // reprise là où on s'était arrêté
	case known:
		offset = 0 // tourné ou tronqué pendant l'arrêt de l'agent : nouveau fichier
	case startup:
		offset = fi.Size()
	default:
		offset = 0
	}
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		fh.Close()
		return nil, err
	}
	return &tailedFile{path: path, src: src, f: fh, id: id, offset: offset}, nil
}

// poll lit ce qui a été ajouté et détecte rotation et troncature ; faux si le fichier
// a disparu et doit être oublié.
func (f *Files) poll(ctx context.Context, tf *tailedFile, now time.Time) bool {
	atEOF := f.read(ctx, tf, now)
	if !atEOF {
		return true // reste à lire : la rotation sera regardée une fois l'ancien fichier vidé
	}
	id, err := pathIdentity(tf.path)
	switch {
	case err != nil:
		// Supprimé (ou renommé sans remplaçant pour l'instant) : on a tout lu, on le lâche ;
		// le prochain glob reprendra le nouveau fichier depuis le début.
		f.flushPartial(ctx, tf, now, true)
		f.mu.Lock()
		delete(f.registry, tf.path)
		f.mu.Unlock()
		return false
	case id != tf.id:
		// Rotation par renommage (logrotate, nginx -s reopen) : l'ancien fichier est vidé,
		// on passe au nouveau, lu depuis le début.
		f.flushPartial(ctx, tf, now, true)
		tf.f.Close()
		nf, err := f.open(tf.path, tf.src, false)
		if err != nil {
			return false
		}
		*tf = *nf
		f.read(ctx, tf, now)
		return true
	}
	if fi, err := tf.f.Stat(); err == nil && fi.Size() < tf.offset {
		// Troncature sur place (copytruncate) : on repart du début.
		tf.f.Seek(0, io.SeekStart)
		tf.offset, tf.partial, tf.skipping = 0, nil, false
		f.read(ctx, tf, now)
	}
	f.flushPartial(ctx, tf, now, false)
	return true
}

// read lit jusqu'à readPerPoll octets ; vrai si la fin du fichier est atteinte.
func (f *Files) read(ctx context.Context, tf *tailedFile, now time.Time) bool {
	buf := make([]byte, 64<<10)
	total := 0
	for total < readPerPoll {
		n, err := tf.f.Read(buf)
		if n > 0 {
			total += n
			f.consume(ctx, tf, buf[:n], now)
		}
		if err != nil || n == 0 {
			return true
		}
	}
	return false
}

// consume découpe les octets lus en lignes.
func (f *Files) consume(ctx context.Context, tf *tailedFile, data []byte, now time.Time) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if tf.skipping {
				tf.offset += int64(len(data))
				return
			}
			tf.partial = append(tf.partial, data...)
			tf.partSeen = now
			if len(tf.partial) > MaxMessage {
				// Ligne démesurée : on envoie le début (tronqué à 32 Kio) et on saute le reste.
				line := tf.partial
				tf.offset += int64(len(line))
				tf.partial, tf.skipping = nil, true
				f.emit(ctx, tf, line, now)
			}
			return
		}
		chunk := data[:i]
		data = data[i+1:]
		if tf.skipping {
			tf.offset += int64(len(chunk)) + 1
			tf.skipping = false
			continue
		}
		line := chunk
		if len(tf.partial) > 0 {
			line = append(tf.partial, chunk...)
			tf.partial = nil
		}
		tf.offset += int64(len(chunk)) + 1
		f.emit(ctx, tf, line, now)
	}
}

// flushPartial envoie une dernière ligne sans fin de ligne, quand le fichier se ferme ou
// qu'elle n'a pas bougé depuis partialFlushAfter.
func (f *Files) flushPartial(ctx context.Context, tf *tailedFile, now time.Time, force bool) {
	if len(tf.partial) == 0 || (!force && now.Sub(tf.partSeen) < partialFlushAfter) {
		return
	}
	line := tf.partial
	tf.partial = nil
	tf.offset += int64(len(line))
	f.emit(ctx, tf, line, now)
}

func (f *Files) emit(ctx context.Context, tf *tailedFile, line []byte, now time.Time) {
	line = bytes.TrimRight(line, "\r")
	if len(bytes.TrimSpace(line)) == 0 {
		// Ligne vide : rien à envoyer, mais la position avance.
		path, id, off := tf.path, tf.id, tf.offset
		f.pipe.Submit(ctx, Entry{Skip: true, Commit: func() { f.commit(path, id, off) }})
		return
	}
	l := model.Log{
		TS:         now.UnixMilli(),
		Service:    tf.src.Service,
		Source:     tf.src.Source,
		Attributes: map[string]any{"file": tf.path},
	}
	if l.Source == "" {
		l.Source = "file"
	}
	ApplyLine(&l, string(line), now)
	path, id, off := tf.path, tf.id, tf.offset
	f.pipe.Submit(ctx, Entry{Log: l, Commit: func() { f.commit(path, id, off) }})
}

// Once lit les dernières lignes de chaque fichier (--once : un aperçu, sans rien noter).
func (f *Files) Once(ctx context.Context) {
	now := time.Now()
	for _, src := range f.sources {
		matches, _ := filepath.Glob(src.Path)
		for _, path := range matches {
			fh, err := openShared(path)
			if err != nil {
				continue
			}
			fi, err := fh.Stat()
			if err != nil || fi.IsDir() {
				fh.Close()
				continue
			}
			start := max(fi.Size()-onceTailBytes, 0)
			buf := make([]byte, fi.Size()-start)
			n, _ := fh.ReadAt(buf, start)
			fh.Close()
			lines := bytes.Split(bytes.TrimRight(buf[:n], "\n"), []byte("\n"))
			if start > 0 && len(lines) > 0 {
				lines = lines[1:] // première ligne coupée par le début de la fenêtre
			}
			if len(lines) > onceTailLines {
				lines = lines[len(lines)-onceTailLines:]
			}
			tf := &tailedFile{path: path, src: src}
			for _, line := range lines {
				f.emit(ctx, tf, line, now)
			}
		}
	}
}

// FlushState écrit les positions maintenant (arrêt : après la confirmation du dernier lot).
func (f *Files) FlushState() { f.saver.Flush() }
