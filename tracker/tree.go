package tracker

import (
	"errors"
	"slices"
	"strings"

	"streaming/protocol"
)

// Erros que o Tree pode devolver. Declarar como variáveis permite que quem
// chama compare com errors.Is(err, ErrNoSource).
var (
	ErrHasSource   = errors.New("já existe uma fonte transmitindo")
	ErrNoSource    = errors.New("ainda não há fonte; tente de novo mais tarde")
	ErrDuplicateID = errors.New("id já está na árvore")
	ErrNotFound    = errors.New("nó não está na árvore")
)

// Node é um nó da árvore de distribuição.
type Node struct {
	ID       string
	Addr     string // ip:porta onde o nó aceita conexões dos filhos
	Parent   *Node  // nil só na raiz
	Children []*Node
}

// Info converte o nó para o formato usado nas mensagens.
func (n *Node) Info() *protocol.NodeInfo {
	return &protocol.NodeInfo{ID: n.ID, Addr: n.Addr}
}

// Depth é a distância até a raiz (a raiz tem profundidade 0).
func (n *Node) Depth() int {
	d := 0
	for p := n.Parent; p != nil; p = p.Parent {
		d++
	}
	return d
}

// Tree guarda a topologia da transmissão: a raiz é a fonte do vídeo e cada
// nó repassa o que recebe para no máximo `fanout` filhos (fanout 2 = árvore
// binária).
//
// Tree não é segura para uso concorrente: o Server a protege com um mutex.
type Tree struct {
	fanout int
	root   *Node
	nodes  map[string]*Node // acesso direto por ID
}

func NewTree(fanout int) *Tree {
	if fanout < 1 {
		panic("fanout precisa ser >= 1")
	}
	return &Tree{fanout: fanout, nodes: make(map[string]*Node)}
}

func (t *Tree) Root() *Node            { return t.root }
func (t *Tree) Get(id string) *Node    { return t.nodes[id] }
func (t *Tree) Len() int               { return len(t.nodes) }
func (t *Tree) has(id string) bool     { _, ok := t.nodes[id]; return ok }
func (t *Tree) register(n *Node) *Node { t.nodes[n.ID] = n; return n }

// SetRoot coloca a fonte na raiz. Só pode haver uma fonte por vez.
func (t *Tree) SetRoot(id, addr string) (*Node, error) {
	if t.root != nil {
		return nil, ErrHasSource
	}
	if t.has(id) {
		return nil, ErrDuplicateID
	}
	t.root = t.register(&Node{ID: id, Addr: addr})
	return t.root, nil
}

// Add coloca um novo nó como filho do nó mais raso que ainda tem vaga.
func (t *Tree) Add(id, addr string) (*Node, error) {
	if t.root == nil {
		return nil, ErrNoSource
	}
	if t.has(id) {
		return nil, ErrDuplicateID
	}
	n := t.register(&Node{ID: id, Addr: addr})
	t.attach(n, t.findParent())
	return n, nil
}

// findParent faz uma busca em largura (BFS) a partir da raiz e devolve o
// primeiro nó com menos de `fanout` filhos. Como a BFS visita a árvore nível
// por nível, o escolhido é sempre o nó com vaga mais perto da fonte, o que
// mantém a árvore baixa (cada nível a mais é mais um salto de atraso).
//
// Nunca devolve nil: uma folha tem 0 filhos e fanout >= 1, então sempre há
// vaga em algum lugar.
func (t *Tree) findParent() *Node {
	queue := []*Node{t.root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if len(n.Children) < t.fanout {
			return n
		}
		queue = append(queue, n.Children...)
	}
	return nil
}

func (t *Tree) attach(child, parent *Node) {
	child.Parent = parent
	parent.Children = append(parent.Children, child)
}

func (t *Tree) detach(n *Node) {
	if p := n.Parent; p != nil {
		if i := slices.Index(p.Children, n); i >= 0 {
			p.Children = slices.Delete(p.Children, i, i+1)
		}
	}
	n.Parent = nil
}

// Removal descreve o que mudou na árvore quando um nó saiu.
type Removal struct {
	Node      *Node   // o nó que saiu
	OldParent *Node   // quem perdeu o filho; nil quando quem saiu foi a raiz
	Moved     []*Node // órfãos religados; cada um já aponta (Parent) para o novo pai
}

// Remove tira um nó da árvore e religa os filhos dele.
//
// Cada filho leva junto a sua subárvore inteira: só os filhos diretos trocam
// de pai, os netos continuam recebendo dos mesmos pais de antes. Cada órfão é
// religado no nó mais raso com vaga (a mesma BFS do Add). Como a BFS parte da
// raiz e o órfão já está desligado dela, a busca nunca passa pela subárvore
// do próprio órfão, então não há risco de criar um ciclo.
//
// Remover a raiz (a fonte) esvazia a árvore: sem fonte não há o que transmitir.
func (t *Tree) Remove(id string) (Removal, error) {
	n, ok := t.nodes[id]
	if !ok {
		return Removal{}, ErrNotFound
	}

	if n == t.root {
		t.root = nil
		clear(t.nodes)
		return Removal{Node: n}, nil
	}

	r := Removal{Node: n, OldParent: n.Parent}
	t.detach(n)
	delete(t.nodes, id)

	orphans := n.Children
	n.Children = nil
	for _, c := range orphans {
		c.Parent = nil
		t.attach(c, t.findParent())
		r.Moved = append(r.Moved, c)
	}
	return r, nil
}

// String desenha a árvore, por exemplo:
//
//	n1 127.0.0.1:9001
//	├── n2 127.0.0.1:9002
//	│   └── n4 127.0.0.1:9004
//	└── n3 127.0.0.1:9003
func (t *Tree) String() string {
	if t.root == nil {
		return "(árvore vazia)"
	}
	var b strings.Builder
	b.WriteString(t.root.ID + " " + t.root.Addr + "\n")
	writeChildren(&b, t.root, "")
	return b.String()
}

func writeChildren(b *strings.Builder, n *Node, prefix string) {
	for i, c := range n.Children {
		branch, next := "├── ", "│   "
		if i == len(n.Children)-1 {
			branch, next = "└── ", "    "
		}
		b.WriteString(prefix + branch + c.ID + " " + c.Addr + "\n")
		writeChildren(b, c, prefix+next)
	}
}
