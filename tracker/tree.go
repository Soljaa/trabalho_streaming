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
	ErrNotParent   = errors.New("o nó informado não é o pai atual")
	ErrNoOtherSlot = errors.New("não há outro pai com vaga")
)

// Node é um nó da árvore de distribuição.
type Node struct {
	ID       string
	Addr     string // ip:porta onde o nó aceita conexões dos filhos
	Parent   *Node  // nil só na raiz
	Children []*Node

	// avoid guarda os IDs dos pais que este nó avisou não conseguir alcançar
	// (ver Relocate). A busca de pai pula esses nós, para o tracker não o
	// colocar de novo debaixo de um deles num reparo futuro.
	avoid map[string]bool
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

// Add coloca um novo nó no nível mais raso que ainda tem vaga, como filho do
// nó desse nível que tem menos filhos.
func (t *Tree) Add(id, addr string) (*Node, error) {
	if t.root == nil {
		return nil, ErrNoSource
	}
	if t.has(id) {
		return nil, ErrDuplicateID
	}
	n := t.register(&Node{ID: id, Addr: addr})
	t.attach(n, t.findParent(n))
	return n, nil
}

// findParent escolhe o pai do nó n, que ainda está fora da árvore. Pula os
// pais que n avisou não alcançar; se só eles tiverem vaga, ignora a restrição,
// porque um pai problemático ainda é melhor que deixar n fora da árvore.
func (t *Tree) findParent(n *Node) *Node {
	if p := t.search(n.avoid); p != nil {
		return p
	}
	return t.search(nil)
}

// search faz uma busca em largura (BFS) a partir da raiz, um nível por vez, e
// para no primeiro nível que tem vaga. Assim o escolhido é sempre um nó com
// vaga mais perto da fonte, o que mantém a árvore baixa (cada nível a mais é
// mais um salto de atraso).
//
// Dentro desse nível, escolhe o nó com menos filhos (no empate, o primeiro).
// Um nó só ganha o segundo filho depois que todos os nós do nível ganharam o
// primeiro: o upload fica dividido e a queda de um nó corta menos gente.
// Pegar o primeiro nó com vaga daria a mesma profundidade, mas encheria n2
// (n4 e n5) enquanto n3 ficava sem filhos.
//
// Os nós em avoid não são candidatos, mas a busca continua pelos filhos deles.
// Com avoid vazio, nunca devolve nil: uma folha tem 0 filhos e fanout >= 1,
// então sempre há vaga em algum lugar. Com avoid, devolve nil se todos os nós
// com vaga estiverem em avoid.
func (t *Tree) search(avoid map[string]bool) *Node {
	level := []*Node{t.root}
	for len(level) > 0 {
		var best *Node
		var next []*Node
		for _, n := range level {
			next = append(next, n.Children...)
			if avoid[n.ID] {
				continue
			}
			if best == nil || len(n.Children) < len(best.Children) {
				best = n
			}
		}
		if best != nil && len(best.Children) < t.fanout {
			return best
		}
		level = next
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
// religado no nó mais raso com vaga (a mesma BFS do Add), evitando os pais
// que ele já avisou não alcançar. Como a BFS parte da raiz e o órfão já está
// desligado dela, a busca nunca passa pela subárvore do próprio órfão, então
// não há risco de criar um ciclo.
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
		t.attach(c, t.findParent(c))
		r.Moved = append(r.Moved, c)
	}
	return r, nil
}

// Relocation descreve a troca de pai pedida pelo próprio nó.
type Relocation struct {
	Node      *Node // quem trocou de pai; Node.Parent já é o novo pai
	OldParent *Node // o pai que ele não alcançava
}

// Relocate troca o pai do nó id, que avisou (parent_unreachable) não
// conseguir se conectar ao pai parentID. Para o tracker esse pai está vivo
// (manda ping), então ele continua onde está: quem muda de lugar é o filho,
// levando a subárvore junto. O novo pai sai da mesma BFS do Add, sem contar
// parentID nem os outros pais que o nó já avisou não alcançar.
//
// O aviso pode estar atrasado: se parentID já não é o pai do nó (um
// parent_changed cruzou com o aviso), devolve ErrNotParent e nada muda. Se
// não houver outro pai com vaga, devolve ErrNoOtherSlot e o nó fica onde
// estava; parentID continua anotado e será evitado nos próximos reparos.
func (t *Tree) Relocate(id, parentID string) (Relocation, error) {
	n, ok := t.nodes[id]
	if !ok {
		return Relocation{}, ErrNotFound
	}
	old := n.Parent
	if old == nil || old.ID != parentID {
		return Relocation{}, ErrNotParent
	}
	if n.avoid == nil {
		n.avoid = make(map[string]bool)
	}
	n.avoid[parentID] = true

	// n sai da árvore antes da busca, para que nem ele nem a subárvore dele
	// sejam candidatos (um nó debaixo de um descendente seria um ciclo).
	i := slices.Index(old.Children, n)
	t.detach(n)
	p := t.search(n.avoid)
	if p == nil {
		n.Parent = old
		old.Children = slices.Insert(old.Children, i, n) // volta para a mesma posição
		return Relocation{}, ErrNoOtherSlot
	}
	t.attach(n, p)
	return Relocation{Node: n, OldParent: old}, nil
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
