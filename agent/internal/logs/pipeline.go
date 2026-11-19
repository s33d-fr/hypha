// Package logs collecte les journaux (contrat § 3.3) : journald (Linux), fichiers suivis
// par motifs glob, journaux d'événements Windows.
//
// Garantie visée : ni perte ni doublon en marche normale. Chaque source note sa position
// (curseur journald, décalage dans le fichier, numéro d'enregistrement Windows) dans une
// fonction Commit portée par l'entrée ; ces fonctions ne sont appelées qu'une fois le lot
// accepté par le serveur ou rangé dans le tampon disque. Après un arrêt brutal, la reprise
// se fait à la dernière position sûre : au pire quelques lignes en double, jamais de trou.
package logs

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// Limites d'un lot (contrat : 500 lignes ou 5 s). La borne en octets garde chaque envoi
// sous la limite de 10 Mo décompressés du serveur, même avec des messages de 32 Kio.
const (
	batchLines    = 500
	batchInterval = 5 * time.Second
	batchBytes    = 4 << 20
	// MaxMessage : longueur maximale d'un message (contrat : 32 Kio).
	MaxMessage = 32 << 10
)

// Entry est une ligne en route vers le serveur.
type Entry struct {
	Log    model.Log
	Commit func() // position à noter quand la ligne est en sécurité ; peut être nil
	Skip   bool   // ligne à ne pas envoyer (vide) dont la position doit quand même avancer
}

// SendFunc envoie un lot ; onDurable est appelé quand le lot est en sécurité.
type SendFunc func(logs []model.Log, onDurable func())

// Pipeline reçoit les lignes de toutes les sources et les envoie par lots.
type Pipeline struct {
	in       chan Entry
	excludes []*regexp.Regexp
	tags     model.TagFunc
	send     SendFunc
	log      *slog.Logger
	observe  func(*model.Log)
}

// SetObserver fait voir chaque ligne lue, exclue ou non, à fn (métriques tirées des
// journaux par les intégrations) ; à poser avant Run.
func (p *Pipeline) SetObserver(fn func(*model.Log)) { p.observe = fn }

// NewPipeline crée la file ; excludes sont les motifs logs.exclude_patterns.
func NewPipeline(excludes []string, tags model.TagFunc, send SendFunc, log *slog.Logger) *Pipeline {
	p := &Pipeline{in: make(chan Entry, 2000), tags: tags, send: send, log: log}
	for _, e := range excludes {
		if re, err := regexp.Compile(e); err == nil {
			p.excludes = append(p.excludes, re)
		}
	}
	return p
}

// Submit confie une ligne à la file ; bloque si la file est pleine (les sources ralentissent
// alors d'elles-mêmes au lieu de perdre des lignes). Rend faux à l'arrêt.
func (p *Pipeline) Submit(ctx context.Context, e Entry) bool {
	select {
	case p.in <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// Run regroupe et envoie jusqu'à l'arrêt, puis envoie le dernier lot.
func (p *Pipeline) Run(ctx context.Context) {
	var (
		batch   []model.Log
		commits []func()
		size    int
		timer   *time.Timer
		timerC  <-chan time.Time
	)
	flush := func() {
		if timer != nil {
			timer.Stop()
			timer, timerC = nil, nil
		}
		if len(batch) == 0 && len(commits) == 0 {
			return
		}
		cs := commits
		done := func() {
			for _, c := range cs {
				c()
			}
		}
		if len(batch) == 0 {
			done() // lot entièrement exclu : seules les positions avancent
		} else {
			p.send(batch, done)
		}
		batch, commits, size = nil, nil, 0
	}
	add := func(e Entry) {
		if e.Commit != nil {
			commits = append(commits, e.Commit)
		}
		if p.observe != nil && !e.Skip {
			p.observe(&e.Log)
		}
		if e.Skip || !p.keep(&e.Log) {
			// Ligne exclue : elle ne part pas, mais sa position doit avancer avec le lot
			// (dans l'ordre, sinon un arrêt pourrait sauter une ligne non encore envoyée).
			return
		}
		p.finish(&e.Log)
		batch = append(batch, e.Log)
		size += len(e.Log.Message) + 256
		if timer == nil {
			timer = time.NewTimer(batchInterval)
			timerC = timer.C
		}
		if len(batch) >= batchLines || size >= batchBytes {
			flush()
		}
	}
	for {
		select {
		case e := <-p.in:
			add(e)
		case <-timerC:
			timer, timerC = nil, nil
			flush()
		case <-ctx.Done():
			// Ce qui est déjà dans la file part avec le dernier lot.
			for {
				select {
				case e := <-p.in:
					add(e)
					continue
				default:
				}
				break
			}
			flush()
			return
		}
	}
}

func (p *Pipeline) keep(l *model.Log) bool {
	for _, re := range p.excludes {
		if re.MatchString(l.Message) {
			return false
		}
	}
	return true
}

// finish complète une ligne : troncature à 32 Kio, statut par défaut, étiquettes communes.
func (p *Pipeline) finish(l *model.Log) {
	if len(l.Message) > MaxMessage {
		l.Message = model.Truncate(l.Message, MaxMessage)
	}
	if l.Status == "" {
		l.Status = "info"
	}
	if l.Attributes == nil {
		l.Attributes = map[string]any{}
	}
	tags := p.tags()
	for k, v := range l.Tags {
		tags[k] = v
	}
	l.Tags = tags
}
