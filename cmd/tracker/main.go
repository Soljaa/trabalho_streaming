// Comando tracker: o servidor central que monta e mantém a árvore de
// distribuição do streaming.
//
//	go run ./cmd/tracker -addr :8000 -fanout 2
package main

import (
	"flag"
	"log"
	"net"
	"time"

	"streaming/tracker"
)

func main() {
	addr := flag.String("addr", ":8000", "endereço onde o tracker escuta (ip:porta; sem ip = todas as interfaces)")
	fanout := flag.Int("fanout", 2, "máximo de filhos por nó (2 = árvore binária)")
	heartbeat := flag.Duration("heartbeat-timeout", 15*time.Second, "silêncio máximo de um nó antes de ser dado como morto")
	flag.Parse()

	if *fanout < 1 {
		log.Fatal("fanout precisa ser >= 1")
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	srv := tracker.NewServer(tracker.Config{Fanout: *fanout, HeartbeatTimeout: *heartbeat})
	log.Fatal(srv.Serve(ln))
}
