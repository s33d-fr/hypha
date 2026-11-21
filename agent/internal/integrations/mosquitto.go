package integrations

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Mosquitto : sujets $SYS du courtier, en MQTT 3.1.1 ──────────────────────────────────────
//
// Client minimal en lecture seule : CONNECT (session propre, identifiant hypha-<aléa>), SUBSCRIBE
// à $SYS/broker/# en QoS 0, lecture des PUBLISH (Mosquitto garde les valeurs de $SYS en messages
// retenus : elles arrivent aussitôt), puis DISCONNECT. Jamais de PUBLISH. Le compte n'a besoin que
// de « topic read $SYS/# » dans l'ACL. Compteurs cumulés (messages, octets) → écarts par intervalle.

var mosquittoGauges = map[string]string{
	"$SYS/broker/clients/connected": "mosquitto.clients.connected", "$SYS/broker/clients/disconnected": "mosquitto.clients.disconnected",
	"$SYS/broker/clients/total": "mosquitto.clients.total", "$SYS/broker/clients/maximum": "mosquitto.clients.maximum",
	"$SYS/broker/clients/expired": "mosquitto.clients.expired", "$SYS/broker/messages/stored": "mosquitto.messages.stored",
	"$SYS/broker/store/messages/count": "mosquitto.store.messages.count", "$SYS/broker/store/messages/bytes": "mosquitto.store.messages.bytes",
	"$SYS/broker/subscriptions/count": "mosquitto.subscriptions", "$SYS/broker/shared_subscriptions/count": "mosquitto.shared_subscriptions",
	"$SYS/broker/retained messages/count": "mosquitto.retained_messages", "$SYS/broker/heap/current": "mosquitto.heap.current",
	"$SYS/broker/heap/maximum": "mosquitto.heap.maximum", "$SYS/broker/connections/socket/count": "mosquitto.connections.sockets",
	"$SYS/broker/load/connections/1min": "mosquitto.load.connections.1min", "$SYS/broker/load/messages/received/1min": "mosquitto.load.messages.received.1min",
	"$SYS/broker/load/messages/sent/1min": "mosquitto.load.messages.sent.1min",
}

var mosquittoCounters = map[string]string{
	"$SYS/broker/messages/received": "mosquitto.messages.received", "$SYS/broker/messages/sent": "mosquitto.messages.sent",
	"$SYS/broker/publish/messages/received": "mosquitto.publish.messages.received", "$SYS/broker/publish/messages/sent": "mosquitto.publish.messages.sent",
	"$SYS/broker/publish/messages/dropped": "mosquitto.publish.messages.dropped", "$SYS/broker/publish/bytes/received": "mosquitto.publish.bytes.received",
	"$SYS/broker/publish/bytes/sent": "mosquitto.publish.bytes.sent", "$SYS/broker/bytes/received": "mosquitto.bytes.received",
	"$SYS/broker/bytes/sent": "mosquitto.bytes.sent",
}

var mosquittoUnits = map[string]string{
	"$SYS/broker/store/messages/bytes": "byte", "$SYS/broker/heap/current": "byte", "$SYS/broker/heap/maximum": "byte",
	"$SYS/broker/publish/bytes/received": "byte", "$SYS/broker/publish/bytes/sent": "byte",
	"$SYS/broker/bytes/received": "byte", "$SYS/broker/bytes/sent": "byte",
}

const (
	mqttMaxPacket   = 64 << 10 // un sujet $SYS tient en quelques octets
	mqttMaxMessages = 1000
)

type Mosquitto struct {
	probe
	cfg config.MosquittoConfig
}

func newMosquitto(cfg config.MosquittoConfig) *Mosquitto {
	explicit := cfg.Username != "" || cfg.URL != "mqtt://127.0.0.1:1883"
	return &Mosquitto{cfg: cfg, probe: newProbe("mosquitto", "mosquitto.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL)}
}

func (m *Mosquitto) period() time.Duration { return 60 * time.Second }

func (m *Mosquitto) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := m.det.skip(now); skip {
		return nil, st
	}
	if m.det.mode == "auto" && !m.det.seen && !fileExists(hostfs.Etc("mosquitto")) {
		return nil, "absent"
	}
	ts := model.Ms(now)
	sys, err := m.readSys(ctx)
	if err != nil {
		return m.fail(ts, tags, err)
	}
	m.det.ok()
	kv := m.kvs()
	m.begin()
	for topic, name := range mosquittoGauges {
		if v, ok := sysNumber(sys[topic]); ok {
			b.Gauge(name, mosquittoUnits[topic], v, kv...)
		}
	}
	for topic, name := range mosquittoCounters {
		if v, ok := sysNumber(sys[topic]); ok {
			unit := mosquittoUnits[topic]
			if unit == "" {
				unit = "message"
			}
			m.count(b, name, unit, topic, v, kv...)
		}
	}
	m.end()
	up, _ := sysNumber(sys["$SYS/broker/uptime"])
	b.Gauge("mosquitto.uptime", "second", up, kv...)
	ver := strings.TrimPrefix(sys["$SYS/broker/version"], "mosquitto version ")
	clients, _ := sysNumber(sys["$SYS/broker/clients/connected"])
	msg := fmt.Sprintf("Mosquitto %s : %.0f client(s) connecté(s), en service depuis %.0f s", firstNonEmpty(ver, "?"), clients, up)
	return []model.Check{m.okCheck(ts, tags, msg)}, "ok"
}

// sysNumber : « 133 seconds », « 0.92 », « 55 » → nombre.
func sysNumber(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	return v, err == nil
}

// readSys se connecte, s'abonne à $SYS/broker/# et rend les valeurs reçues (sujet → texte).
func (m *Mosquitto) readSys(ctx context.Context) (map[string]string, error) {
	u, err := url.Parse(m.cfg.URL)
	if err != nil {
		return nil, err
	}
	timeout := m.cfg.Timeout.D()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), map[string]string{"mqtt": "1883", "mqtts": "8883"}[u.Scheme])
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, cleanDialErr(err)
	}
	if u.Scheme == "mqtts" {
		tc := &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: m.cfg.Insecure} //nolint:gosec // choisi dans la conf
		if m.cfg.CAFile != "" {
			pem, err := os.ReadFile(m.cfg.CAFile)
			pool := x509.NewCertPool()
			if err != nil || !pool.AppendCertsFromPEM(pem) {
				conn.Close()
				return nil, fmt.Errorf("autorité de certification illisible : %s", m.cfg.CAFile)
			}
			tc.RootCAs = pool
		}
		tconn := tls.Client(conn, tc)
		if err := tconn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("TLS : %v", err)
		}
		conn = tconn
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	return mqttReadSys(conn, m.cfg.Username, m.cfg.Password, timeout)
}

func cleanDialErr(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return fmt.Errorf("connexion impossible : %v", op.Err)
	}
	return err
}

// mqttReadSys : dialogue MQTT 3.1.1 sur une connexion ouverte.
func mqttReadSys(conn net.Conn, user, pass string, timeout time.Duration) (map[string]string, error) {
	rd := bufio.NewReader(conn)
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	if _, err := conn.Write(mqttConnect("hypha-"+hex.EncodeToString(id), user, pass)); err != nil {
		return nil, err
	}
	typ, body, err := mqttRead(rd)
	if err != nil {
		return nil, fmt.Errorf("pas de réponse MQTT (%v)", err)
	}
	if typ != 2 || len(body) < 2 {
		return nil, fmt.Errorf("réponse MQTT inattendue (paquet %d au lieu de CONNACK)", typ)
	}
	switch body[1] {
	case 0:
	case 1:
		return nil, fmt.Errorf("courtier refuse MQTT 3.1.1 (CONNACK 1)")
	case 4:
		return nil, fmt.Errorf("identifiants refusés (CONNACK 4)")
	case 5:
		return nil, fmt.Errorf("connexion non autorisée (CONNACK 5 : compte attendu ?)")
	default:
		return nil, fmt.Errorf("connexion refusée (CONNACK %d)", body[1])
	}
	if _, err := conn.Write(mqttSubscribe(1, "$SYS/broker/#")); err != nil {
		return nil, err
	}
	out := map[string]string{}
	deadline := time.Now().Add(timeout)
	quiet := 600 * time.Millisecond // fin des messages retenus : plus rien depuis 0,6 s
	subacked := false
	for len(out) < mqttMaxMessages {
		wait := quiet
		if !subacked || len(out) == 0 {
			wait = time.Until(deadline)
		}
		if until := time.Until(deadline); until < wait {
			wait = until
		}
		if wait <= 0 {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(wait))
		typ, body, err := mqttRead(rd)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			if len(out) > 0 {
				break
			}
			return nil, fmt.Errorf("lecture MQTT : %v", err)
		}
		switch typ {
		case 9: // SUBACK
			subacked = true
			if len(body) >= 3 && body[2] == 0x80 {
				return nil, fmt.Errorf("abonnement à $SYS/# refusé (ACL : topic read $SYS/# attendu)")
			}
		case 3: // PUBLISH
			if topic, payload, ok := mqttPublish(body); ok && strings.HasPrefix(topic, "$SYS/") {
				out[topic] = model.Truncate(strings.TrimSpace(payload), 200)
			}
		}
	}
	_, _ = conn.Write([]byte{0xE0, 0x00}) // DISCONNECT
	if len(out) == 0 {
		if !subacked {
			return nil, fmt.Errorf("abonnement sans réponse")
		}
		return nil, fmt.Errorf("aucun sujet $SYS reçu (sys_interval à 0, ou ACL sans « topic read $SYS/# »)")
	}
	if _, ok := out["$SYS/broker/version"]; !ok {
		if _, ok := out["$SYS/broker/uptime"]; !ok {
			return nil, fmt.Errorf("sujets $SYS reçus sans version ni durée de service (courtier autre que Mosquitto ?)")
		}
	}
	return out, nil
}

func mqttString(s string) []byte {
	b := make([]byte, 2, 2+len(s))
	binary.BigEndian.PutUint16(b, uint16(len(s)))
	return append(b, s...)
}

func mqttPacket(header byte, body []byte) []byte {
	out := []byte{header}
	n := len(body)
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		out = append(out, d)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}

func mqttConnect(clientID, user, pass string) []byte {
	flags := byte(0x02) // session propre
	if user != "" {
		flags |= 0x80
		if pass != "" {
			flags |= 0x40
		}
	}
	body := append(mqttString("MQTT"), 4, flags, 0, 30) // niveau 4 = 3.1.1, maintien 30 s
	body = append(body, mqttString(clientID)...)
	if user != "" {
		body = append(body, mqttString(user)...)
		if pass != "" {
			body = append(body, mqttString(pass)...)
		}
	}
	return mqttPacket(0x10, body)
}

func mqttSubscribe(packetID uint16, topic string) []byte {
	body := []byte{byte(packetID >> 8), byte(packetID)}
	body = append(body, mqttString(topic)...)
	return mqttPacket(0x82, append(body, 0)) // QoS 0
}

// mqttRead lit un paquet : type (4 bits de poids fort) et corps.
func mqttRead(rd *bufio.Reader) (byte, []byte, error) {
	h, err := rd.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, mult := 0, 1
	for i := 0; ; i++ {
		c, err := rd.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(c&0x7F) * mult
		if c&0x80 == 0 {
			break
		}
		if i >= 3 {
			return 0, nil, fmt.Errorf("longueur de paquet invalide")
		}
		mult *= 128
	}
	if n > mqttMaxPacket {
		return 0, nil, fmt.Errorf("paquet de %d octets refusé", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(rd, body); err != nil {
		return 0, nil, err
	}
	typ := h >> 4
	if typ == 3 {
		body = append([]byte{h & 0x0F}, body...) // drapeaux du PUBLISH (QoS) en tête
	}
	return typ, body, nil
}

// mqttPublish : sujet et contenu d'un PUBLISH (premier octet : drapeaux de l'en-tête).
func mqttPublish(body []byte) (string, string, bool) {
	if len(body) < 3 {
		return "", "", false
	}
	flags, body := body[0], body[1:]
	tl := int(binary.BigEndian.Uint16(body))
	if 2+tl > len(body) {
		return "", "", false
	}
	topic, rest := string(body[2:2+tl]), body[2+tl:]
	if (flags>>1)&0x03 > 0 { // QoS 1 ou 2 : identifiant de paquet
		if len(rest) < 2 {
			return "", "", false
		}
		rest = rest[2:]
	}
	return topic, string(rest), true
}
