# Streaming P2P (Lina e Daniel)

Trabalho de Redes 2 (UFF): um sistema de streaming P2P em Go. Os nós formam uma
árvore. A fonte, que tem o vídeo, fica na raiz, e cada nó repassa os segmentos
para no máximo 2 filhos. Um servidor central, o **tracker**, monta essa árvore,
avisa cada nó de quem é o seu pai e quem são os seus filhos, e conserta a árvore
quando alguém sai ou cai.

## Estrutura

```
trabalho_streaming/
├── go.mod                   módulo Go do projeto (nome: streaming)
├── protocol/protocol.go     mensagens entre nós e tracker (o cliente importa o mesmo pacote)
├── tracker/
│   ├── tree.go              a árvore: entrada por BFS, saída com religação dos órfãos, troca de pai
│   ├── server.go            servidor TCP: conexões, heartbeat, avisos aos nós
│   ├── tree_test.go         testes da árvore
│   └── server_test.go       testes do servidor por TCP de verdade
├── cmd/
│   ├── tracker/main.go      executável do tracker
│   └── peer-sim/main.go     nó simulado para testar o tracker (não transmite vídeo)
├── scripts/
│   ├── demo.ps1             demonstração com vários processos e falhas
│   └── demo-firewall.ps1    demonstração de um pai vivo que os filhos não alcançam
└── docs/                    explicações para o relatório
```

## Como rodar

Precisa do Go instalado (ver [docs/01-go-do-zero.md](docs/01-go-do-zero.md)). Abra um
terminal por processo, todos na pasta do projeto.

Terminal 1, o tracker:

```bash
go run ./cmd/tracker
```

Terminal 2, a fonte:

```bash
go run ./cmd/peer-sim -role source -port 9001
```

Terminais 3, 4, 5… um peer em cada, com portas diferentes:

```bash
go run ./cmd/peer-sim -port 9002
```

O tracker imprime a árvore a cada mudança. Para testar falhas:

- **Ctrl+C num peer** é a saída educada: ele manda `leave` antes de fechar.
- **Fechar a janela do terminal** de um peer simula uma queda.
- **`-heartbeat 0`** cria um nó travado: fica conectado mas nunca manda `ping`, e o tracker o derruba depois de 15 s.
- **`-firewall`** cria um nó com a porta bloqueada: ele continua vivo para o tracker, mas os filhos não conseguem se conectar a ele, avisam com `parent_unreachable` e são mudados de lugar.

O tracker escuta em `:8000` (todas as interfaces) para aceitar nós de outras
máquinas. Na primeira vez o Firewall do Windows pede permissão. Para testar só na
sua máquina, use `go run ./cmd/tracker -addr 127.0.0.1:8000` e o aviso não aparece.

## Testes e demonstração

```bash
go test ./...
```

```bash
go test -v ./tracker
```

```bash
powershell -ExecutionPolicy Bypass -File scripts\demo.ps1
```

```bash
powershell -ExecutionPolicy Bypass -File scripts\demo-firewall.ps1
```

O `-v` mostra os logs do tracker com a árvore mudando. O `demo.ps1` sobe o tracker
e 7 nós como processos separados, provoca as três falhas (timeout, queda, saída da
fonte) e deixa os logs de cada processo em `demo-logs\`. O `demo-firewall.ps1`
sobe um nó com a porta bloqueada e mostra o tracker mudando de lugar os filhos
que não o alcançam.

## Documentação

1. [Go do zero](docs/01-go-do-zero.md): instalação, ferramentas, sintaxe e concorrência, com exemplos tirados deste código.
2. [Arquitetura do tracker](docs/02-arquitetura-do-tracker.md): protocolo, algoritmos, tolerância a falhas e o que o cliente precisa fazer.
3. [Passo a passo do desenvolvimento](docs/03-passo-a-passo.md): o que foi feito, em ordem, com os resultados.
4. [Arquitetura final](docs/04-arquitetura-final.md): o sistema completo, com casos de uso, fluxogramas, estados, cenários e a proposta de protocolo entre peers.
