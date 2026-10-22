package tracker

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"streaming/protocol"
)

// TestMain roda antes de todos os testes do pacote. Os logs do tracker só
// aparecem com `go test -v`; sem o -v eles poluiriam a saída.
func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

// startServer sobe um tracker numa porta livre de 127.0.0.1 e devolve o endereço.
func startServer(t *testing.T, heartbeat time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0") // porta 0: o sistema escolhe uma livre
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go NewServer(Config{Fanout: 2, HeartbeatTimeout: heartbeat}).Serve(ln)
	return ln.Addr().String()
}

// testPeer é um nó falso que fala o protocolo direto pelo socket.
type testPeer struct {
	t    *testing.T
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
	id   string
}

func join(t *testing.T, addr, role string, port int) *testPeer {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	p := &testPeer{t: t, conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}
	p.write(protocol.Message{Type: protocol.TypeJoin, Role: role, ListenPort: port})
	return p
}

func (p *testPeer) write(msg protocol.Message) {
	p.t.Helper()
	if err := p.enc.Encode(msg); err != nil {
		p.t.Fatal(err)
	}
}

// keepAlive manda pings em segundo plano até a conexão fechar.
func (p *testPeer) keepAlive(every time.Duration) {
	go func() {
		for {
			time.Sleep(every)
			if err := p.enc.Encode(protocol.Message{Type: protocol.TypePing}); err != nil {
				return
			}
		}
	}()
}

// expect lê a próxima mensagem (pulando pongs) e confere o tipo.
func (p *testPeer) expect(typ string) protocol.Message {
	p.t.Helper()
	for {
		p.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var msg protocol.Message
		if err := p.dec.Decode(&msg); err != nil {
			p.t.Fatalf("%s: esperava %q, mas a leitura falhou: %v", p.id, typ, err)
		}
		if msg.Type == protocol.TypePong && typ != protocol.TypePong {
			continue
		}
		if msg.Type != typ {
			p.t.Fatalf("%s: esperava %q, veio %+v", p.id, typ, msg)
		}
		return msg
	}
}

func (p *testPeer) welcome(wantParent string) protocol.Message {
	p.t.Helper()
	msg := p.expect(protocol.TypeWelcome)
	p.id = msg.ID
	if got := idOf(msg.Parent); got != wantParent {
		p.t.Fatalf("%s: pai = %q, esperado %q", p.id, got, wantParent)
	}
	return msg
}

func (p *testPeer) expectChild(typ, childID string) {
	p.t.Helper()
	if got := idOf(p.expect(typ).Child); got != childID {
		p.t.Fatalf("%s: %s com filho %q, esperado %q", p.id, typ, got, childID)
	}
}

func (p *testPeer) expectParentChanged(parentID string) {
	p.t.Helper()
	if got := idOf(p.expect(protocol.TypeParentChanged).Parent); got != parentID {
		p.t.Fatalf("%s: novo pai %q, esperado %q", p.id, got, parentID)
	}
}

// expectClosed confere que o tracker fechou a conexão.
func (p *testPeer) expectClosed() {
	p.t.Helper()
	p.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg protocol.Message
	err := p.dec.Decode(&msg)
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		p.t.Fatalf("%s: esperava a conexão fechada, veio %+v (erro %v)", p.id, msg, err)
	}
}

func idOf(n *protocol.NodeInfo) string {
	if n == nil {
		return ""
	}
	return n.ID
}

func TestJoinLeaveAndRepair(t *testing.T) {
	addr := startServer(t, 5*time.Second)

	src := join(t, addr, protocol.RoleSource, 9001)
	src.welcome("") // n1

	a := join(t, addr, protocol.RolePeer, 9002)
	a.welcome("n1") // n2
	src.expectChild(protocol.TypeChildAdded, "n2")

	b := join(t, addr, protocol.RolePeer, 9003)
	b.welcome("n1") // n3
	src.expectChild(protocol.TypeChildAdded, "n3")

	c := join(t, addr, protocol.RolePeer, 9004)
	w := c.welcome("n2") // n4
	if w.Parent.Addr != "127.0.0.1:9002" || w.Depth != 2 {
		t.Errorf("welcome de n4: pai em %s, profundidade %d", w.Parent.Addr, w.Depth)
	}
	a.expectChild(protocol.TypeChildAdded, "n4")

	d := join(t, addr, protocol.RolePeer, 9005)
	d.welcome("n2") // n5
	a.expectChild(protocol.TypeChildAdded, "n5")

	// Árvore agora:  n1 -> (n2 -> (n4, n5), n3)
	a.write(protocol.Message{Type: protocol.TypePing})
	a.expect(protocol.TypePong)

	// n2 sai com leave: os filhos dele (n4, n5) precisam de pai novo.
	// n4 pega a vaga que n2 deixou em n1; n5 vai para debaixo de n3.
	a.write(protocol.Message{Type: protocol.TypeLeave})
	a.expectClosed()
	src.expectChild(protocol.TypeChildRemoved, "n2")
	c.expectParentChanged("n1")
	src.expectChild(protocol.TypeChildAdded, "n4")
	d.expectParentChanged("n3")
	b.expectChild(protocol.TypeChildAdded, "n5")

	// n3 cai sem avisar (conexão fechada): n5 volta para n1.
	b.conn.Close()
	src.expectChild(protocol.TypeChildRemoved, "n3")
	d.expectParentChanged("n1")
	src.expectChild(protocol.TypeChildAdded, "n5")

	// A fonte sai: todo mundo recebe stream_ended e é desconectado.
	src.conn.Close()
	for _, p := range []*testPeer{c, d} {
		p.expect(protocol.TypeStreamEnded)
		p.expectClosed()
	}
}

func TestHeartbeatTimeout(t *testing.T) {
	addr := startServer(t, 300*time.Millisecond)

	src := join(t, addr, protocol.RoleSource, 9001)
	src.welcome("")
	src.keepAlive(100 * time.Millisecond)

	// Este nó entra e fica mudo: sem ping, o tracker o derruba depois de 300ms.
	silent := join(t, addr, protocol.RolePeer, 9002)
	silent.welcome("n1")
	src.expectChild(protocol.TypeChildAdded, "n2")
	src.expectChild(protocol.TypeChildRemoved, "n2")
	silent.expectClosed()
}

func TestJoinRejected(t *testing.T) {
	addr := startServer(t, 5*time.Second)

	cases := []struct {
		name string
		msg  protocol.Message
	}{
		{"peer sem fonte", protocol.Message{Type: protocol.TypeJoin, Role: protocol.RolePeer, ListenPort: 9002}},
		{"role inválido", protocol.Message{Type: protocol.TypeJoin, Role: "chefe", ListenPort: 9002}},
		{"porta inválida", protocol.Message{Type: protocol.TypeJoin, Role: protocol.RoleSource, ListenPort: 0}},
		{"não começou com join", protocol.Message{Type: protocol.TypePing}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			p := &testPeer{t: t, conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}
			t.Cleanup(func() { conn.Close() })
			p.write(tc.msg)
			if p.expect(protocol.TypeError).Error == "" {
				t.Error("mensagem de erro vazia")
			}
			p.expectClosed()
		})
	}

	// Com uma fonte já conectada, uma segunda fonte é recusada.
	join(t, addr, protocol.RoleSource, 9001).welcome("")
	second := join(t, addr, protocol.RoleSource, 9009)
	second.expect(protocol.TypeError)
	second.expectClosed()
}
