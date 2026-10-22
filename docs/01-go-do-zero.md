# Go do zero

Este guia cobre o que é preciso saber de Go para entender e mexer no projeto.
Os exemplos vêm do próprio código. Quando ajuda, há uma comparação com Python.

## 1. Instalação

### Windows

O jeito mais rápido é o `winget`, que já vem no Windows 11 e baixa o instalador
oficial do site go.dev:

```bash
winget install --id GoLang.Go -e
```

Outra opção é baixar o `.msi` em https://go.dev/dl e seguir o instalador. Os dois
caminhos fazem a mesma coisa: instalam o Go em `C:\Program Files\Go` e colocam
`C:\Program Files\Go\bin` no PATH.

**Feche e abra o terminal** (o PATH só é lido quando o terminal abre) e confira:

```bash
go version
```

A saída deve ser algo como `go version go1.27.0 windows/amd64`.

### Linux e macOS

No Linux, baixe o `.tar.gz` em https://go.dev/dl e siga as instruções da página
(é extrair em `/usr/local/go` e pôr `/usr/local/go/bin` no PATH). No macOS,
`brew install go` resolve.

### Onde as coisas ficam

```bash
go env GOROOT GOPATH
```

- **GOROOT** (`C:\Program Files\Go`): o próprio Go, ou seja, o compilador e a biblioteca padrão.
- **GOPATH** (`C:\Users\<você>\go`): cache das bibliotecas de terceiros que o Go baixa.
  Em versões antigas o código tinha que ficar dentro do GOPATH; hoje não precisa,
  o projeto pode estar em qualquer pasta.

### Editor

No VS Code, instale a extensão **Go** (publicada pelo Go Team at Google, id
`golang.go`). Na primeira vez ela oferece instalar o `gopls`, o servidor de
linguagem que dá autocompletar, "ir para definição", erros enquanto você digita
e formatação automática ao salvar. Aceite.

## 2. Módulos, pacotes e executáveis

### Módulo

Um **módulo** é um projeto. Ele é definido pelo arquivo `go.mod` na raiz, criado com:

```bash
go mod init streaming
```

```
module streaming

go 1.27.0
```

- `module streaming` é o nome do módulo e o prefixo de todos os imports internos.
  Projetos publicados costumam usar o endereço do repositório
  (`github.com/usuario/projeto`); para um projeto local, um nome curto basta.
- `go 1.27.0` é a versão mínima. Quem tiver um Go mais antigo (a partir do 1.21)
  verá o próprio `go` baixar a versão certa automaticamente.

Se o projeto usasse bibliotecas de terceiros, elas apareceriam no `go.mod` (como
um `requirements.txt`) e as somas de verificação num `go.sum`. Este projeto usa
só a biblioteca padrão, então não tem dependências.

### Pacote

Um **pacote** é uma pasta. Todos os `.go` de uma pasta começam com o mesmo
`package nome` e enxergam tudo uns dos outros, como se fossem um arquivo só.

| Pasta | Pacote | Import |
|---|---|---|
| `protocol/` | `package protocol` | `"streaming/protocol"` |
| `tracker/` | `package tracker` | `"streaming/tracker"` |
| `cmd/tracker/` | `package main` | não se importa: é um executável |

O pacote `main` com uma função `main()` vira um programa executável. A convenção
é colocar cada executável em `cmd/<nome>/`.

### Visibilidade: maiúscula ou minúscula

Go não tem `public`/`private`. A regra é a primeira letra do nome:

- **Maiúscula** é exportado, visível fora do pacote: `Tree`, `NewTree`, `Node.Depth`.
- **Minúscula** é interno ao pacote: `findParent`, `client`, `sendLocked`.

Por isso o `cmd/tracker/main.go` consegue chamar `tracker.NewServer(...)` mas não
`tracker.findParent()`.

## 3. Comandos do dia a dia

| Comando | O que faz |
|---|---|
| `go run ./cmd/tracker` | compila e roda na hora (o binário vai para uma pasta temporária) |
| `go build -o bin/ ./cmd/...` | compila todos os executáveis de `cmd/` para `bin/` |
| `go test ./...` | roda os testes de todos os pacotes (`./...` = esta pasta e as de baixo) |
| `go test -v -run TestHeartbeat ./tracker` | só os testes cujo nome casa com o padrão, mostrando detalhes |
| `go vet ./...` | procura erros comuns que compilam mas estão errados |
| `gofmt -l .` | lista arquivos fora da formatação padrão (`gofmt -w .` corrige) |
| `go mod tidy` | acerta o `go.mod` de acordo com os imports |

O Go gera **um único executável** por programa, sem precisar do Go instalado na
máquina que vai rodá-lo. Dá para compilar para outro sistema mudando duas
variáveis de ambiente, por exemplo para gerar um binário Linux no Windows:

```bash
GOOS=linux GOARCH=amd64 go build -o bin/ ./cmd/...
```

(Esse é o formato do Git Bash; no PowerShell, defina `$env:GOOS="linux"` e
`$env:GOARCH="amd64"` antes do `go build`.)

## 4. Sintaxe essencial

### Variáveis e tipos

Go é estaticamente tipado: o tipo de cada variável é fixo e verificado na
compilação. Na maioria das vezes o compilador deduz o tipo sozinho.

```go
d := 0                          // declara e atribui; o tipo (int) é deduzido
var node *Node                  // declara sem valor: começa com o "valor zero"
id := fmt.Sprintf("n%d", 4)     // string
```

Toda variável começa com um **valor zero**, nunca com lixo: `0` para números,
`""` para string, `false` para bool e `nil` para ponteiros, slices, maps e
channels.

### Funções com vários retornos e erros

Go não tem exceções. Uma função que pode falhar devolve o erro como último
valor, e quem chama confere:

```go
func (t *Tree) Add(id, addr string) (*Node, error) {
	if t.root == nil {
		return nil, ErrNoSource
	}
	...
	return n, nil
}

node, err := s.tree.Add(id, addr)
if err != nil {
	return nil, err
}
```

Em Python isso seria um `try/except`. Em Go o erro é um valor comum, e o
`if err != nil` aparece o tempo todo. É verboso, mas deixa explícito onde cada
coisa pode falhar.

Para criar e comparar erros:

```go
var ErrNoSource = errors.New("ainda não há fonte")      // erro fixo, comparável
fmt.Errorf("listen_port inválida: %d", msg.ListenPort)  // erro com valores formatados
errors.Is(err, io.EOF)                                  // "esse erro é (ou embrulha) io.EOF?"
```

`panic` existe, mas é para erro de programação, quando o programa não tem como
continuar (como `NewTree(0)`). Não é usado para fluxo normal.

### Structs e métodos

Não há classes. Há **structs** (só dados) e **métodos** ligados a um tipo:

```go
type Node struct {
	ID       string
	Addr     string
	Parent   *Node
	Children []*Node
}

// (n *Node) é o "receiver", o equivalente ao self do Python.
func (n *Node) Depth() int {
	d := 0
	for p := n.Parent; p != nil; p = p.Parent {
		d++
	}
	return d
}
```

Não existe construtor. A convenção é uma função `NewXxx`:

```go
func NewTree(fanout int) *Tree {
	return &Tree{fanout: fanout, nodes: make(map[string]*Node)}
}
```

### Ponteiros

`*Node` é um ponteiro para um `Node`; `&Node{...}` cria um `Node` e devolve o
endereço dele. A árvore usa ponteiros porque o mesmo nó é referenciado em vários
lugares (no mapa, na lista de filhos do pai, no `Parent` dos filhos), e todos
precisam enxergar o mesmo objeto. Sem ponteiro, cada atribuição faria uma cópia.

O receiver também é um ponteiro (`func (t *Tree) Add`) para que o método altere
a árvore original, e não uma cópia. Não existe aritmética de ponteiros como em
C, e o coletor de lixo libera a memória sozinho.

### Slices (listas)

```go
queue := []*Node{t.root}            // cria com um elemento
queue = append(queue, n.Children...) // adiciona vários ("..." espalha o slice)
n := queue[0]                        // primeiro elemento
queue = queue[1:]                    // tudo menos o primeiro (fatia, sem copiar)
len(queue)                           // tamanho

i := slices.Index(p.Children, n)     // posição de n, ou -1
p.Children = slices.Delete(p.Children, i, i+1)
```

O `append` devolve o slice novo, por isso é sempre `x = append(x, ...)`.

### Maps (dicionários)

```go
nodes := make(map[string]*Node)
nodes["n1"] = node
n, ok := nodes["n1"]   // ok é false se a chave não existe (n vira nil)
delete(nodes, "n1")
clear(nodes)           // esvazia
```

### Controle de fluxo

Só existe `for`, em três formas:

```go
for i := 2; i <= n; i++ { ... }      // clássico
for len(queue) > 0 { ... }           // "while"
for i, c := range n.Children { ... } // percorre slice/map/channel (índice e valor)
for range 3000 { ... }               // repete 3000 vezes
```

O `if` aceita uma instrução antes da condição, com a variável valendo só dentro
do `if`:

```go
if err := dec.Decode(&msg); err != nil {
	return
}
```

O `switch` não precisa de `break`; cada `case` para sozinho:

```go
switch msg.Type {
case protocol.TypePing:
	s.send(c.id, protocol.Message{Type: protocol.TypePong})
case protocol.TypeLeave:
	s.remove(c.id, "pediu para sair (leave)")
	return
default:
	log.Printf("mensagem desconhecida %q", msg.Type)
}
```

### defer

`defer` agenda uma chamada para quando a função terminar, seja pelo `return`
normal ou por erro. Serve para garantir a limpeza (como o `with` do Python):

```go
func (s *Server) remove(id, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock() // destrava na saída, qualquer que seja o caminho
	...
}
```

### Interfaces

Uma interface é uma lista de métodos. Qualquer tipo que tenha esses métodos
satisfaz a interface automaticamente, sem precisar declarar nada (é o "duck
typing" do Python, só que verificado pelo compilador). O projeto usa várias da
biblioteca padrão:

- `error`: qualquer tipo com `Error() string`.
- `net.Conn`: uma conexão de rede (`Read`, `Write`, `Close`, `SetReadDeadline`…).
  O tracker não sabe se é TCP de verdade ou outra coisa; só usa os métodos.
- `io.Reader` / `io.Writer`: qualquer coisa de onde se lê bytes ou para onde se
  escreve. Uma `net.Conn` é as duas coisas, por isso dá para fazer
  `json.NewDecoder(conn)`.

### JSON e struct tags

O texto entre crases depois de um campo é uma **tag**, que o pacote
`encoding/json` lê para saber o nome do campo no JSON:

```go
type Message struct {
	Type       string `json:"type"`
	ListenPort int    `json:"listen_port,omitempty"` // omitempty: some do JSON se for 0
	Parent     *NodeInfo `json:"parent,omitempty"`   // some se for nil
}

enc := json.NewEncoder(conn)
enc.Encode(protocol.Message{Type: "ping"}) // escreve {"type":"ping"}\n na conexão

dec := json.NewDecoder(conn)
var msg protocol.Message
dec.Decode(&msg) // lê o próximo objeto JSON da conexão e preenche msg
```

Só campos exportados (maiúscula) entram no JSON.

## 5. Concorrência

É o ponto forte do Go e o que mais aparece no servidor.

### Goroutines

Uma goroutine é uma função rodando em paralelo com o resto. Basta um `go` na
frente da chamada:

```go
for {
	conn, err := ln.Accept()
	...
	go s.handleConn(conn) // cada conexão é atendida em paralelo
}
```

Goroutines são muito mais leves que threads do sistema (começam com poucos KB
de memória), então ter milhares é normal. E, ao contrário do Python com o GIL,
elas rodam de fato em paralelo nos vários núcleos do processador.

### Channels

Um channel é um "cano" tipado por onde goroutines trocam valores com
segurança:

```go
send := make(chan protocol.Message, 32) // channel com buffer de 32 mensagens
send <- msg                             // envia (bloqueia se o buffer estiver cheio)
msg := <-send                           // recebe (bloqueia se estiver vazio)
close(send)                             // fecha: ninguém mais envia
for msg := range send { ... }           // recebe até o channel ser fechado
```

No tracker, cada nó conectado tem um channel `send` que funciona como a fila de
mensagens a enviar. Uma goroutine (`writeLoop`) fica em `for msg := range
c.send` escrevendo cada mensagem na rede.

### select

O `select` espera vários channels ao mesmo tempo e executa o primeiro que
ficar pronto. No `peer-sim`:

```go
for {
	select {
	case msg := <-incoming: // chegou mensagem do tracker
		show(msg)
	case <-tick:            // passou o intervalo do heartbeat
		enc.Encode(protocol.Message{Type: protocol.TypePing})
	case <-stop:            // usuário apertou Ctrl+C
		enc.Encode(protocol.Message{Type: protocol.TypeLeave})
		return
	}
}
```

Dois truques usados no projeto:

- **`default` torna o envio não bloqueante.** Se o buffer estiver cheio, cai no
  `default` em vez de travar (ver `sendLocked` em `tracker/server.go`).
- **Um channel `nil` nunca fica pronto.** Com `-heartbeat 0` o `tick` fica `nil`,
  e o `case <-tick` simplesmente nunca acontece.

### Mutex

Quando várias goroutines mexem na mesma estrutura (no tracker, a árvore e o mapa
de clientes), é preciso garantir que só uma mexa de cada vez. O `sync.Mutex`
faz isso:

```go
s.mu.Lock()
defer s.mu.Unlock()
// daqui até o fim da função, só esta goroutine mexe na árvore
```

Sem o mutex, duas entradas simultâneas poderiam escolher a mesma vaga na árvore
e corromper a estrutura. Esse tipo de bug se chama **data race** e é difícil de
reproduzir. O `go test -race` detecta essas situações, mas no Windows precisa de
um compilador C (gcc) instalado; no Linux e no macOS funciona direto.

## 6. Rede com a biblioteca padrão

O pacote `net` cobre tudo o que o projeto precisa, sem bibliotecas externas:

```go
ln, err := net.Listen("tcp", ":8000") // servidor: escuta na porta 8000
conn, err := ln.Accept()              // espera e aceita uma conexão

conn, err := net.Dial("tcp", "127.0.0.1:8000") // cliente: conecta

conn.SetReadDeadline(time.Now().Add(15 * time.Second)) // a próxima leitura falha se demorar mais de 15 s
conn.RemoteAddr()                                       // endereço do outro lado (ip:porta)
net.SplitHostPort("127.0.0.1:52247")                    // separa "127.0.0.1" e "52247"
net.JoinHostPort("127.0.0.1", "9002")                   // junta em "127.0.0.1:9002"
```

O `SetReadDeadline` é o que implementa o timeout do heartbeat: antes de cada
leitura o tracker define um prazo e, se o nó ficar calado até lá, a leitura
devolve `os.ErrDeadlineExceeded`.

## 7. Testes

Testes ficam em arquivos `_test.go` no mesmo pacote e são funções
`TestXxx(t *testing.T)`:

```go
func TestFanoutThree(t *testing.T) {
	tr := build(t, 3, 5)
	if got := parentOf(tr, "n5"); got != "n2" {
		t.Errorf("pai de n5 = %s, esperado n2", got) // marca falha e continua
	}
}
```

| Recurso | Para quê |
|---|---|
| `t.Errorf` / `t.Fatalf` | marca falha; o `Fatal` também interrompe o teste |
| `t.Helper()` | numa função auxiliar, faz o erro apontar a linha de quem a chamou |
| `t.Cleanup(f)` | roda `f` no fim do teste (fechar conexões, por exemplo) |
| `t.Run(nome, f)` | subteste, muito usado com uma tabela de casos (ver `TestJoinRejected`) |
| `TestMain(m)` | roda antes de todos os testes do pacote |
| `-count=20` | roda 20 vezes, útil para pegar testes instáveis |

Não precisa de framework: o pacote `testing` vem na biblioteca padrão.

## 8. Pacotes da biblioteca padrão usados

| Pacote | Uso no projeto |
|---|---|
| `net` | TCP: `Listen`, `Accept`, `Dial`, deadlines, endereços |
| `encoding/json` | codificar e decodificar as mensagens |
| `sync` | `Mutex` que protege a árvore |
| `time` | durações, deadlines, `Ticker` do heartbeat |
| `log` | logs com data e hora (vão para o stderr) |
| `flag` | opções de linha de comando (`-addr`, `-fanout`, …) |
| `errors`, `fmt` | criar, embrulhar e comparar erros; formatar texto |
| `strconv`, `strings` | número ↔ texto; montar o desenho da árvore |
| `slices` | `Index` e `Delete` em slices |
| `io`, `os` | `io.EOF`, `os.ErrDeadlineExceeded`, `os.Exit` |
| `os/signal` | capturar o Ctrl+C no `peer-sim` |
| `math/rand/v2` | números aleatórios no teste de estresse |
| `testing` | testes automatizados |

## 9. Para ir além

- **Tour of Go** (https://go.dev/tour): tutorial interativo no navegador, cerca de 2 horas.
- **Effective Go** (https://go.dev/doc/effective_go): como escrever Go do jeito idiomático.
- **Documentação da biblioteca padrão** (https://pkg.go.dev/std): por exemplo, https://pkg.go.dev/net.
- `go doc net.Conn` no terminal mostra a documentação sem abrir o navegador.
