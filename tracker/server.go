package tracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"streaming/protocol"
)

const (
	sendQueueSize = 32              // mensagens pendentes por nó antes de considerá-lo travado
	writeTimeout  = 5 * time.Second // tempo máximo para conseguir escrever uma mensagem
)

type Config struct {
	Fanout           int           // máximo de filhos por nó
	HeartbeatTimeout time.Duration // silêncio máximo de um nó antes de ser dado como morto
}

// Server é o tracker: aceita conexões TCP dos nós, mantém a árvore e avisa
// cada nó quando o pai ou os filhos dele mudam.
//
// Cada conexão é atendida por duas goroutines:
//   - handleConn lê as mensagens do nó (join, ping, leave, parent_unreachable);
//   - writeLoop escreve as mensagens que o tracker manda para o nó, tiradas
//     de uma fila (o channel `send`).
//
// Assim, quem altera a árvore só coloca mensagens nas filas e nunca fica
// bloqueado esperando a rede enquanto segura o mutex.
type Server struct {
	cfg Config

	mu      sync.Mutex // protege os três campos abaixo
	tree    *Tree
	clients map[string]*client
	nextID  int
}

// client é a ponta do tracker na conexão com um nó.
type client struct {
	id   string
	conn net.Conn
	send chan protocol.Message
}

func NewServer(cfg Config) *Server {
	return &Server{
		cfg:     cfg,
		tree:    NewTree(cfg.Fanout),
		clients: make(map[string]*client),
	}
}

// Serve aceita conexões até o listener ser fechado. Cada conexão ganha a sua
// própria goroutine, então um nó lento não atrasa os outros.
func (s *Server) Serve(ln net.Listener) error {
	log.Printf("tracker ouvindo em %s (fanout=%d, heartbeat-timeout=%s)",
		ln.Addr(), s.cfg.Fanout, s.cfg.HeartbeatTimeout)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)

	// 1) A primeira mensagem tem que ser um join.
	conn.SetReadDeadline(time.Now().Add(s.cfg.HeartbeatTimeout))
	var msg protocol.Message
	if err := dec.Decode(&msg); err != nil {
		log.Printf("[%s] conexão encerrada antes do join: %s", conn.RemoteAddr(), describe(err))
		return
	}
	c, err := s.join(conn, msg)
	if err != nil {
		log.Printf("[%s] join recusado: %v", conn.RemoteAddr(), err)
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		json.NewEncoder(conn).Encode(protocol.Message{Type: protocol.TypeError, Error: err.Error()})
		return
	}
	go c.writeLoop()

	// 2) Depois do join, esperamos pings, o leave e avisos de pai inalcançável.
	// Cada leitura tem um prazo: se o nó ficar calado por mais de
	// HeartbeatTimeout, o Decode devolve erro de timeout e o nó é tratado
	// como morto.
	for {
		conn.SetReadDeadline(time.Now().Add(s.cfg.HeartbeatTimeout))
		var msg protocol.Message
		if err := dec.Decode(&msg); err != nil {
			s.remove(c.id, describe(err))
			return
		}
		switch msg.Type {
		case protocol.TypePing:
			s.send(c.id, protocol.Message{Type: protocol.TypePong})
		case protocol.TypeLeave:
			s.remove(c.id, "pediu para sair (leave)")
			return
		case protocol.TypeParentUnreachable:
			s.relocate(c.id, msg.Parent)
		default:
			log.Printf("[%s] mensagem desconhecida %q ignorada", c.id, msg.Type)
		}
	}
}

// describe traduz o erro de leitura no motivo da saída do nó.
func describe(err error) string {
	switch {
	case errors.Is(err, io.EOF):
		return "conexão fechada pelo nó"
	case errors.Is(err, os.ErrDeadlineExceeded):
		return "sem heartbeat (timeout)"
	case errors.Is(err, net.ErrClosed):
		return "conexão encerrada pelo tracker"
	default:
		return "falha na conexão: " + err.Error()
	}
}

// join valida o pedido, coloca o nó na árvore e avisa o pai dele.
func (s *Server) join(conn net.Conn, msg protocol.Message) (*client, error) {
	if msg.Type != protocol.TypeJoin {
		return nil, fmt.Errorf("a primeira mensagem deve ser %q, veio %q", protocol.TypeJoin, msg.Type)
	}
	if msg.ListenPort < 1 || msg.ListenPort > 65535 {
		return nil, fmt.Errorf("listen_port inválida: %d", msg.ListenPort)
	}
	// O nó informa só a porta; o IP é o mesmo de onde veio a conexão.
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(host, strconv.Itoa(msg.ListenPort))

	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("n%d", s.nextID+1)
	var node *Node
	switch msg.Role {
	case protocol.RoleSource:
		node, err = s.tree.SetRoot(id, addr)
	case protocol.RolePeer:
		node, err = s.tree.Add(id, addr)
	default:
		err = fmt.Errorf("role inválido: %q (use %q ou %q)", msg.Role, protocol.RoleSource, protocol.RolePeer)
	}
	if err != nil {
		return nil, err
	}
	s.nextID++

	c := &client{id: id, conn: conn, send: make(chan protocol.Message, sendQueueSize)}
	s.clients[id] = c

	welcome := protocol.Message{Type: protocol.TypeWelcome, ID: id, Depth: node.Depth()}
	if node.Parent != nil {
		welcome.Parent = node.Parent.Info()
		s.sendLocked(node.Parent.ID, protocol.Message{Type: protocol.TypeChildAdded, Child: node.Info()})
	}
	s.sendLocked(id, welcome)

	log.Printf("[%s] entrou como %s em %s", id, msg.Role, addr)
	s.logTreeLocked()
	return c, nil
}

// remove tira o nó da árvore e avisa quem foi afetado. Pode ser chamado mais
// de uma vez para o mesmo nó (por exemplo, leave seguido de conexão fechada);
// só a primeira chamada tem efeito.
func (s *Server) remove(id, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clients[id]
	if !ok {
		return
	}
	s.dropLocked(c)
	r, err := s.tree.Remove(id)
	if err != nil {
		log.Printf("[%s] erro ao remover da árvore: %v", id, err)
		return
	}
	log.Printf("[%s] saiu: %s", id, reason)

	// Só a raiz não tem pai. Sem a fonte não há transmissão: avisamos todo
	// mundo e encerramos as conexões. Os nós podem fazer join de novo quando
	// uma nova fonte entrar.
	if r.OldParent == nil {
		for _, other := range s.clients {
			s.sendLocked(other.id, protocol.Message{Type: protocol.TypeStreamEnded})
			s.dropLocked(other)
		}
		log.Printf("a fonte saiu; transmissão encerrada")
		s.logTreeLocked()
		return
	}

	s.sendLocked(r.OldParent.ID, protocol.Message{Type: protocol.TypeChildRemoved, Child: r.Node.Info()})
	for _, orphan := range r.Moved {
		s.sendLocked(orphan.ID, protocol.Message{Type: protocol.TypeParentChanged, Parent: orphan.Parent.Info()})
		s.sendLocked(orphan.Parent.ID, protocol.Message{Type: protocol.TypeChildAdded, Child: orphan.Info()})
		log.Printf("[%s] órfão religado em %s", orphan.ID, orphan.Parent.ID)
	}
	s.logTreeLocked()
}

// relocate atende o parent_unreachable: o nó não consegue se conectar ao pai,
// embora o pai continue vivo para o tracker (o caso típico é o firewall da
// máquina do pai bloqueando a porta). Só o tracker conhece a árvore, então é
// ele quem escolhe o novo pai; o pai antigo continua na árvore.
func (s *Server) relocate(id string, parent *protocol.NodeInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if parent == nil {
		log.Printf("[%s] parent_unreachable sem o campo parent; ignorado", id)
		return
	}
	r, err := s.tree.Relocate(id, parent.ID)
	switch {
	case errors.Is(err, ErrNotParent):
		// O aviso cruzou com um parent_changed: o nó já tem outro pai e vai
		// tentar se conectar a ele.
		log.Printf("[%s] avisou que não alcança %s, que já não é o pai dele; ignorado", id, parent.ID)
		return
	case errors.Is(err, ErrNoOtherSlot):
		// O nó continua tentando e pode avisar de novo quando entrar mais gente.
		log.Printf("[%s] não alcança o pai %s, mas não há outro pai com vaga; fica onde está", id, parent.ID)
		return
	case err != nil:
		log.Printf("[%s] parent_unreachable: %v", id, err)
		return
	}

	log.Printf("[%s] não alcança o pai %s; mudou para %s", id, r.OldParent.ID, r.Node.Parent.ID)
	s.sendLocked(r.OldParent.ID, protocol.Message{Type: protocol.TypeChildRemoved, Child: r.Node.Info()})
	s.sendLocked(id, protocol.Message{Type: protocol.TypeParentChanged, Parent: r.Node.Parent.Info()})
	s.sendLocked(r.Node.Parent.ID, protocol.Message{Type: protocol.TypeChildAdded, Child: r.Node.Info()})
	s.logTreeLocked()
}

// Os métodos com sufixo "Locked" esperam que s.mu já esteja travado.

func (s *Server) send(id string, msg protocol.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendLocked(id, msg)
}

// sendLocked coloca a mensagem na fila do nó sem bloquear. Se a fila estiver
// cheia, o nó parou de ler: fechamos a conexão e a goroutine de leitura dele
// cuida da remoção.
func (s *Server) sendLocked(id string, msg protocol.Message) {
	c, ok := s.clients[id]
	if !ok {
		return
	}
	select {
	case c.send <- msg:
	default:
		log.Printf("[%s] fila de envio cheia; derrubando a conexão", id)
		c.conn.Close()
	}
}

// dropLocked tira o nó do mapa e fecha a fila dele. O writeLoop ainda envia o
// que estiver na fila e depois fecha a conexão. Como só se envia para nós que
// estão no mapa, ninguém escreve num channel já fechado.
func (s *Server) dropLocked(c *client) {
	delete(s.clients, c.id)
	close(c.send)
}

func (s *Server) logTreeLocked() {
	log.Printf("árvore com %d nó(s):\n%s", s.tree.Len(), s.tree)
}

// writeLoop é a única goroutine que escreve na conexão do nó, então as
// mensagens nunca se misturam. Termina quando a fila é fechada.
func (c *client) writeLoop() {
	defer c.conn.Close()
	enc := json.NewEncoder(c.conn)
	for msg := range c.send {
		c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := enc.Encode(msg); err != nil {
			return
		}
	}
}
