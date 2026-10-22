// Package protocol define as mensagens trocadas entre os nós e o tracker.
//
// Cada nó abre uma conexão TCP com o tracker e a mantém aberta enquanto
// participa da transmissão. Pela conexão trafegam objetos JSON, um por linha
// (o json.Encoder do Go já coloca um "\n" depois de cada objeto).
//
// Fluxo normal:
//
//	nó -> tracker:  {"type":"join","role":"peer","listen_port":9002}
//	tracker -> nó:  {"type":"welcome","id":"n2","depth":1,"parent":{"id":"n1","addr":"127.0.0.1:9001"}}
//	nó -> tracker:  {"type":"ping"}            (a cada poucos segundos)
//	tracker -> nó:  {"type":"pong"}
//	tracker -> nó:  {"type":"child_added","child":{"id":"n4","addr":"127.0.0.1:9004"}}
//	nó -> tracker:  {"type":"leave"}
package protocol

// Mensagens que o nó envia ao tracker.
const (
	TypeJoin  = "join"  // entrar na árvore (sempre a primeira mensagem)
	TypePing  = "ping"  // heartbeat: "continuo vivo"
	TypeLeave = "leave" // saída voluntária
)

// Mensagens que o tracker envia ao nó.
const (
	TypeWelcome       = "welcome"        // join aceito: seu ID e seu pai
	TypeError         = "error"          // join recusado; o tracker fecha a conexão em seguida
	TypePong          = "pong"           // resposta ao ping
	TypeChildAdded    = "child_added"    // você ganhou um filho: comece a transmitir para ele
	TypeChildRemoved  = "child_removed"  // seu filho saiu: pare de transmitir para ele
	TypeParentChanged = "parent_changed" // seu pai saiu: passe a receber do novo pai
	TypeStreamEnded   = "stream_ended"   // a fonte saiu: a transmissão acabou
)

// Papéis possíveis no join.
const (
	RoleSource = "source" // tem o vídeo; vira a raiz da árvore
	RolePeer   = "peer"   // recebe o vídeo e repassa para os filhos
)

// NodeInfo identifica um nó: o ID que o tracker deu a ele e o endereço
// (ip:porta) onde ele aceita conexões de outros nós.
type NodeInfo struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

// Message é o formato único de todas as mensagens. Cada tipo usa só alguns
// campos; os outros ficam vazios e o "omitempty" os tira do JSON.
type Message struct {
	Type string `json:"type"`

	// join
	Role       string `json:"role,omitempty"`
	ListenPort int    `json:"listen_port,omitempty"`

	// welcome
	ID    string `json:"id,omitempty"`
	Depth int    `json:"depth,omitempty"`

	// welcome e parent_changed (nil para a fonte, que não tem pai)
	Parent *NodeInfo `json:"parent,omitempty"`

	// child_added e child_removed
	Child *NodeInfo `json:"child,omitempty"`

	// error
	Error string `json:"error,omitempty"`
}
