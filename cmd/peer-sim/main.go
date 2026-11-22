// Comando peer-sim: simula um nó da rede para testar o tracker. Faz o join,
// manda heartbeats e mostra tudo o que o tracker avisa. Não transmite vídeo;
// a parte de controle do cliente de verdade pode seguir o mesmo esqueleto.
//
//	go run ./cmd/peer-sim -role source -port 9001
//	go run ./cmd/peer-sim -role peer -port 9002
//
// Como um nó de verdade, ele abre a porta de escuta (aceita os filhos e fecha
// a conexão logo em seguida, já que não há vídeo) e testa se consegue se
// conectar ao pai que o tracker indicou. Se não conseguir em 3 tentativas,
// manda parent_unreachable e o tracker o coloca debaixo de outro pai.
//
// Ctrl+C sai de forma educada (manda leave). Para simular uma queda, feche a
// janela do terminal ou mate o processo. Para simular um nó travado, use
// -heartbeat 0: ele continua conectado mas para de mandar ping. Para simular
// um firewall bloqueando a porta, use -firewall: o nó continua vivo para o
// tracker, mas nenhum filho consegue se conectar a ele.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"time"

	"streaming/protocol"
)

// Regra do cliente para conectar no pai: até 3 tentativas, 1 s entre elas,
// cada uma com prazo de 2 s (sem prazo, o Dial pode esperar uns 20 s quando a
// máquina do pai some da rede). Depois de avisar o tracker, se ele não indicar
// outro pai, o nó volta a testar o mesmo pai 5 s depois.
const (
	dialAttempts = 3
	dialTimeout  = 2 * time.Second
	dialRetry    = 1 * time.Second
	retestAfter  = 5 * time.Second
)

func main() {
	trackerAddr := flag.String("tracker", "127.0.0.1:8000", "endereço do tracker")
	role := flag.String("role", protocol.RolePeer, "source ou peer")
	port := flag.Int("port", 9001, "porta onde este nó aceita conexões dos filhos")
	interval := flag.Duration("heartbeat", 5*time.Second, "intervalo entre pings (0 = não manda ping)")
	firewall := flag.Bool("firewall", false, "simula um firewall: não abre a porta, então os filhos não conseguem se conectar")
	flag.Parse()

	conn, err := net.Dial("tcp", *trackerAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// A porta de escuta abre antes do join, porque um filho pode chegar logo
	// depois. O IP é o desta conexão com o tracker: é o mesmo que o tracker
	// vê e passa para os filhos.
	if *firewall {
		log.Printf("-firewall: a porta %d fica fechada", *port)
	} else {
		host, _, _ := net.SplitHostPort(conn.LocalAddr().String())
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(*port)))
		if err != nil {
			log.Fatal(err)
		}
		defer ln.Close()
		go acceptChildren(ln)
	}

	// Só a goroutine principal (esta) escreve na conexão; a leitura fica em outra.
	enc := json.NewEncoder(conn)
	if err := enc.Encode(protocol.Message{Type: protocol.TypeJoin, Role: *role, ListenPort: *port}); err != nil {
		log.Fatal(err)
	}

	// A goroutine de leitura só repassa as mensagens pelo channel; quem imprime
	// é sempre a goroutine principal, assim os logs saem na ordem de chegada.
	// readErr é escrito antes do close(incoming) e lido depois que o main vê o
	// channel fechado, então as duas goroutines nunca o acessam ao mesmo tempo.
	incoming := make(chan protocol.Message)
	var readErr error
	go func() {
		defer close(incoming)
		dec := json.NewDecoder(conn)
		for {
			var msg protocol.Message
			if err := dec.Decode(&msg); err != nil {
				readErr = err
				return
			}
			incoming <- msg
		}
	}()

	// Um channel nil nunca fica pronto no select: com -heartbeat 0 o case do
	// ping simplesmente nunca acontece.
	var tick <-chan time.Time
	if *interval > 0 {
		t := time.NewTicker(*interval)
		defer t.Stop()
		tick = t.C
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	// parent é o último pai que o tracker indicou. Os testes de conexão rodam
	// em outra goroutine e devolvem o resultado por checks.
	var parent *protocol.NodeInfo
	checks := make(chan parentCheck)

	for {
		select {
		case msg, ok := <-incoming:
			if !ok {
				log.Printf("conexão com o tracker encerrada: %v", readErr)
				return
			}
			show(msg)
			if msg.Type == protocol.TypeParentChanged || (msg.Type == protocol.TypeWelcome && msg.Parent != nil) {
				parent = msg.Parent
				go checkParent(*parent, 0, checks)
			}
		case r := <-checks:
			if parent == nil || r.parent.ID != parent.ID {
				continue // o pai mudou durante o teste; o novo já está sendo testado
			}
			if r.err == nil {
				log.Printf("   conectou no pai %s", r.parent.ID)
				continue
			}
			log.Printf("   não conectou no pai %s em %d tentativas (%v): avisando o tracker", r.parent.ID, dialAttempts, r.err)
			send(enc, protocol.Message{Type: protocol.TypeParentUnreachable, Parent: &r.parent})
			// Se o tracker não tiver outro pai, o parent_changed não vem: testa
			// o mesmo pai de novo mais tarde (e avisa de novo se ainda falhar).
			go checkParent(r.parent, retestAfter, checks)
		case <-tick:
			enc.Encode(protocol.Message{Type: protocol.TypePing})
		case <-stop:
			log.Print("Ctrl+C: mandando leave")
			enc.Encode(protocol.Message{Type: protocol.TypeLeave})
			return
		}
	}
}

// acceptChildren aceita as conexões dos filhos e fecha cada uma na hora: o
// peer-sim não tem vídeo para mandar, só mostra que a porta está aberta.
func acceptChildren(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		c.Close()
	}
}

type parentCheck struct {
	parent protocol.NodeInfo
	err    error // nil se conectou
}

// checkParent espera `wait` e tenta abrir uma conexão TCP com o pai, como o
// cliente de verdade faria antes de mandar o hello. O cliente de verdade
// também desconfiaria de um pai que aceita a conexão mas não manda nenhum
// frame (processo travado).
func checkParent(parent protocol.NodeInfo, wait time.Duration, out chan<- parentCheck) {
	time.Sleep(wait)
	var err error
	for i := range dialAttempts {
		if i > 0 {
			time.Sleep(dialRetry)
		}
		var c net.Conn
		if c, err = net.DialTimeout("tcp", parent.Addr, dialTimeout); err == nil {
			c.Close()
			break
		}
	}
	out <- parentCheck{parent: parent, err: err}
}

// send manda uma mensagem ao tracker e a mostra no log.
func send(enc *json.Encoder, msg protocol.Message) {
	raw, _ := json.Marshal(msg)
	log.Printf("-> %s", raw)
	enc.Encode(msg)
}

// show imprime a mensagem crua e o que o cliente de verdade faria com ela.
func show(msg protocol.Message) {
	if msg.Type == protocol.TypePong {
		return // aparece a cada heartbeat; só polui a tela
	}
	raw, _ := json.Marshal(msg)
	log.Printf("<- %s", raw)

	switch msg.Type {
	case protocol.TypeWelcome:
		if msg.Parent == nil {
			log.Printf("   sou %s, a raiz: transmito o vídeo para os meus filhos", msg.ID)
		} else {
			log.Printf("   sou %s (profundidade %d): conectar no pai %s em %s",
				msg.ID, msg.Depth, msg.Parent.ID, msg.Parent.Addr)
		}
	case protocol.TypeChildAdded:
		log.Printf("   novo filho %s em %s: começar a transmitir para ele", msg.Child.ID, msg.Child.Addr)
	case protocol.TypeChildRemoved:
		log.Printf("   filho %s saiu ou mudou de pai: parar de transmitir para ele", msg.Child.ID)
	case protocol.TypeParentChanged:
		log.Printf("   pai mudou para %s em %s: reconectar", msg.Parent.ID, msg.Parent.Addr)
	case protocol.TypeStreamEnded:
		log.Printf("   a fonte saiu: transmissão encerrada")
	case protocol.TypeError:
		log.Printf("   tracker recusou: %s", msg.Error)
	}
}
