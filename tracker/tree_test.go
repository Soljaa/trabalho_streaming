package tracker

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
)

// checkTree confere as regras que a árvore nunca pode quebrar: todo nó do mapa
// é alcançável a partir da raiz, nenhum nó aparece duas vezes (sem ciclos), os
// ponteiros Parent batem com as listas Children e ninguém passa do fanout.
func checkTree(t *testing.T, tr *Tree) {
	t.Helper()
	if tr.root == nil {
		if len(tr.nodes) != 0 {
			t.Fatalf("sem raiz, mas %d nós no mapa", len(tr.nodes))
		}
		return
	}
	if tr.root.Parent != nil {
		t.Fatalf("raiz %s tem pai %s", tr.root.ID, tr.root.Parent.ID)
	}
	seen := map[*Node]bool{}
	queue := []*Node{tr.root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] {
			t.Fatalf("nó %s visitado duas vezes (ciclo)", n.ID)
		}
		seen[n] = true
		if tr.nodes[n.ID] != n {
			t.Fatalf("nó %s está na árvore mas não no mapa", n.ID)
		}
		if len(n.Children) > tr.fanout {
			t.Fatalf("nó %s tem %d filhos (fanout %d)", n.ID, len(n.Children), tr.fanout)
		}
		for _, c := range n.Children {
			if c.Parent != n {
				t.Fatalf("filho %s de %s aponta para outro pai", c.ID, n.ID)
			}
		}
		queue = append(queue, n.Children...)
	}
	if len(seen) != len(tr.nodes) {
		t.Fatalf("%d nós alcançáveis, %d no mapa", len(seen), len(tr.nodes))
	}
}

// build cria uma árvore com a fonte n1 e os peers n2..nN, nessa ordem.
func build(t *testing.T, fanout, n int) *Tree {
	t.Helper()
	tr := NewTree(fanout)
	if _, err := tr.SetRoot("n1", "addr1"); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= n; i++ {
		if _, err := tr.Add(fmt.Sprintf("n%d", i), fmt.Sprintf("addr%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	checkTree(t, tr)
	return tr
}

func parentOf(tr *Tree, id string) string {
	if p := tr.Get(id).Parent; p != nil {
		return p.ID
	}
	return ""
}

func TestAddFillsLevelByLevel(t *testing.T) {
	tr := build(t, 2, 7)
	//         n1
	//      /      \
	//    n2        n3
	//   /  \      /  \
	//  n4  n5    n6  n7
	want := map[string]string{"n2": "n1", "n3": "n1", "n4": "n2", "n5": "n2", "n6": "n3", "n7": "n3"}
	for id, parent := range want {
		if got := parentOf(tr, id); got != parent {
			t.Errorf("pai de %s = %s, esperado %s", id, got, parent)
		}
	}
	if d := tr.Get("n7").Depth(); d != 2 {
		t.Errorf("profundidade de n7 = %d, esperado 2", d)
	}
}

func TestFanoutThree(t *testing.T) {
	tr := build(t, 3, 5)
	for _, id := range []string{"n2", "n3", "n4"} {
		if got := parentOf(tr, id); got != "n1" {
			t.Errorf("pai de %s = %s, esperado n1", id, got)
		}
	}
	if got := parentOf(tr, "n5"); got != "n2" {
		t.Errorf("pai de n5 = %s, esperado n2", got)
	}
}

func TestJoinErrors(t *testing.T) {
	tr := NewTree(2)
	if _, err := tr.Add("n1", "a"); !errors.Is(err, ErrNoSource) {
		t.Errorf("peer sem fonte: erro %v, esperado ErrNoSource", err)
	}
	tr.SetRoot("n1", "a")
	if _, err := tr.SetRoot("n2", "b"); !errors.Is(err, ErrHasSource) {
		t.Errorf("segunda fonte: erro %v, esperado ErrHasSource", err)
	}
	if _, err := tr.Add("n1", "c"); !errors.Is(err, ErrDuplicateID) {
		t.Errorf("id repetido: erro %v, esperado ErrDuplicateID", err)
	}
}

func TestRemoveLeaf(t *testing.T) {
	tr := build(t, 2, 7)
	r, err := tr.Remove("n5")
	if err != nil {
		t.Fatal(err)
	}
	checkTree(t, tr)
	if r.OldParent.ID != "n2" || len(r.Moved) != 0 {
		t.Errorf("OldParent=%s Moved=%d, esperado n2 e nenhum órfão", r.OldParent.ID, len(r.Moved))
	}
	// A vaga liberada em n2 é a mais rasa, então o próximo nó entra ali.
	tr.Add("n8", "addr8")
	if got := parentOf(tr, "n8"); got != "n2" {
		t.Errorf("pai de n8 = %s, esperado n2", got)
	}
}

func TestRemoveInnerNodeKeepsSubtrees(t *testing.T) {
	tr := build(t, 2, 11)
	// n2 tem os filhos n4 e n5; n4 tem n8 e n9; n5 tem n10 e n11.
	r, err := tr.Remove("n2")
	if err != nil {
		t.Fatal(err)
	}
	checkTree(t, tr)
	if len(r.Moved) != 2 {
		t.Fatalf("%d órfãos religados, esperado 2", len(r.Moved))
	}
	// n4 ocupa a vaga que n2 deixou em n1; n5 vai para a vaga mais rasa que
	// sobrar: debaixo de n6, a primeira folha do nível 2.
	if got := parentOf(tr, "n4"); got != "n1" {
		t.Errorf("pai de n4 = %s, esperado n1", got)
	}
	if got := parentOf(tr, "n5"); got != "n6" {
		t.Errorf("pai de n5 = %s, esperado n6", got)
	}
	// Os netos não mudam de pai.
	for id, parent := range map[string]string{"n8": "n4", "n9": "n4", "n10": "n5", "n11": "n5"} {
		if got := parentOf(tr, id); got != parent {
			t.Errorf("pai de %s = %s, esperado %s", id, got, parent)
		}
	}
}

func TestRemoveRootEmptiesTree(t *testing.T) {
	tr := build(t, 2, 5)
	r, err := tr.Remove("n1")
	if err != nil {
		t.Fatal(err)
	}
	if r.OldParent != nil || tr.Len() != 0 || tr.Root() != nil {
		t.Errorf("depois de remover a raiz: OldParent=%v Len=%d", r.OldParent, tr.Len())
	}
	checkTree(t, tr)
	if _, err := tr.Remove("n1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("remover de novo: erro %v, esperado ErrNotFound", err)
	}
}

// TestRandomChurn faz milhares de entradas e saídas aleatórias e confere as
// regras da árvore depois de cada operação.
func TestRandomChurn(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) // semente fixa: o teste é reproduzível
	for _, fanout := range []int{1, 2, 3} {
		tr := build(t, fanout, 1)
		var alive []string
		next := 2
		for range 3000 {
			if len(alive) > 0 && rng.IntN(3) == 0 {
				i := rng.IntN(len(alive))
				if _, err := tr.Remove(alive[i]); err != nil {
					t.Fatal(err)
				}
				alive = append(alive[:i], alive[i+1:]...)
			} else {
				id := fmt.Sprintf("n%d", next)
				next++
				if _, err := tr.Add(id, id); err != nil {
					t.Fatal(err)
				}
				alive = append(alive, id)
			}
			checkTree(t, tr)
		}
	}
}
