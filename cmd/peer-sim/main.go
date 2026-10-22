// Comando peer-sim: simula um nó da rede para testar o tracker. Faz o join,
// manda heartbeats e mostra tudo o que o tracker avisa. Não transmite vídeo;
// a parte de controle do cliente de verdade pode seguir o mesmo esqueleto.
//
//	go run ./cmd/peer-sim -role source -port 9001
//	go run ./cmd/peer-sim -role peer -port 9002
//
// Ctrl+C sai de forma educada (manda leave). Para simular uma queda, feche a
// janela do terminal ou mate o processo. Para simular um nó travado, use
// -heartbeat 0: ele continua conectado mas para de mandar ping.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"streaming/protocol"
)

func main() {
	trackerAddr := flag.String("tracker", "127.0.0.1:8000", "endereço do tracker")
	role := flag.String("role", protocol.RolePeer, "source ou peer")
	port := flag.Int("port", 9001, "porta onde este nó aceitaria conexões dos filhos")
	interval := flag.Duration("heartbeat", 5*time.Second, "intervalo entre pings (0 = não manda ping)")
	flag.Parse()

	conn, err := net.Dial("tcp", *trackerAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

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

	for {
		select {
		case msg, ok := <-incoming:
			if !ok {
				log.Printf("conexão com o tracker encerrada: %v", readErr)
				return
			}
			show(msg)
		case <-tick:
			enc.Encode(protocol.Message{Type: protocol.TypePing})
		case <-stop:
			log.Print("Ctrl+C: mandando leave")
			enc.Encode(protocol.Message{Type: protocol.TypeLeave})
			return
		}
	}
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
		log.Printf("   filho %s saiu: parar de transmitir para ele", msg.Child.ID)
	case protocol.TypeParentChanged:
		log.Printf("   pai mudou para %s em %s: reconectar", msg.Parent.ID, msg.Parent.Addr)
	case protocol.TypeStreamEnded:
		log.Printf("   a fonte saiu: transmissão encerrada")
	case protocol.TypeError:
		log.Printf("   tracker recusou: %s", msg.Error)
	}
}
