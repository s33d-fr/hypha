package synthetics

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// fakeServer : serveur en clair d'une seule conversation, réponses dictées par handle.
func fakeServer(t *testing.T, greet string, handle func(cmd string) (string, bool)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.Write([]byte(greet))
				rd := bufio.NewReader(c)
				for {
					l, err := rd.ReadString('\n')
					if err != nil {
						return
					}
					out, done := handle(strings.TrimSpace(l))
					c.Write([]byte(out))
					if done {
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestProbeSMTP(t *testing.T) {
	addr := fakeServer(t, "220 mx.test ESMTP Postfix\r\n", func(cmd string) (string, bool) {
		switch {
		case strings.HasPrefix(strings.ToUpper(cmd), "EHLO"):
			return "250-mx.test\r\n250-PIPELINING\r\n250-AUTH PLAIN LOGIN\r\n250 SIZE 52428800\r\n", false
		case strings.EqualFold(cmd, "QUIT"):
			return "221 bye\r\n", true
		}
		return "502 non\r\n", false
	})
	r := probeMail(context.Background(), "smtp", addr, map[string]any{"tls": "off", "expect_banner": "postfix"})
	if !r.OK {
		t.Fatalf("en clair : %s", r.Error)
	}
	caps, _ := r.Details["capabilities"].([]string)
	if strings.Join(caps, " ") != "PIPELINING AUTH SIZE AUTH=PLAIN AUTH=LOGIN" {
		t.Fatalf("capacités : %v", caps)
	}
	r = probeMail(context.Background(), "smtp", addr, map[string]any{"tls": "starttls"})
	if r.OK || !strings.Contains(r.Error, "STARTTLS non proposé") {
		t.Fatalf("STARTTLS absent non signalé : %+v", r)
	}
	r = probeSSL(context.Background(), addr, map[string]any{"starttls": "smtp"})
	if r.OK || !strings.Contains(r.Error, "STARTTLS non proposé") {
		t.Fatalf("ssl + starttls : %+v", r)
	}
}

func TestProbeIMAP(t *testing.T) {
	addr := fakeServer(t, "* OK IMAP4rev1 pret\r\n", func(cmd string) (string, bool) {
		tag, rest, _ := strings.Cut(cmd, " ")
		switch strings.ToUpper(rest) {
		case "CAPABILITY":
			return "* CAPABILITY IMAP4rev1 IDLE AUTH=PLAIN\r\n" + tag + " OK fini\r\n", false
		case "LOGOUT":
			return "* BYE\r\n" + tag + " OK\r\n", true
		}
		return tag + " BAD\r\n", false
	})
	r := probeMail(context.Background(), "imap", addr, map[string]any{"tls": "off"})
	caps, _ := r.Details["capabilities"].([]string)
	if !r.OK || strings.Join(caps, " ") != "IMAP4REV1 IDLE AUTH=PLAIN" {
		t.Fatalf("%+v", r)
	}
}
