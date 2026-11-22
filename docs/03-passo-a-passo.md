# Passo a passo do desenvolvimento do tracker

Registro do que foi feito, na ordem, com os comandos e os resultados. Serve de
base para a seção de desenvolvimento do relatório.

Objetivo da etapa (22/10, Daniel): um servidor que recebe o contato dos nós e
gerencia as relações entre eles, ou seja, quem é pai de quem na árvore de
distribuição.

## Passo 1: instalar o Go

```bash
winget install --id GoLang.Go -e
```

```
Encontrado Go Programming Language [GoLang.Go] Versão 1.27.0
Baixando https://go.dev/dl/go1.27.0.windows-amd64.msi
Hash do instalador verificado com êxito
Instalado com êxito
```

Num terminal novo:

```bash
go version
```

```
go version go1.27.0 windows/amd64
```

## Passo 2: criar o módulo

Na pasta do projeto:

```bash
go mod init streaming
```

Isso cria o `go.mod`, que define o nome do módulo (`streaming`) e a versão
mínima do Go. Todos os imports internos começam por esse nome, por exemplo
`streaming/protocol`.

## Passo 3: definir o protocolo primeiro

Antes de qualquer lógica, foi definido **o que** o tracker e os nós conversam:
o arquivo [`protocol/protocol.go`](../protocol/protocol.go).

Isso vem primeiro porque o protocolo é o contrato entre as duas partes do
trabalho. Com ele fechado, o tracker (Daniel) e o cliente (Lina) podem ser
desenvolvidos em paralelo, e como os dois são em Go, o cliente importa o mesmo
pacote e não há risco de divergirem no nome de um campo.

Decisões tomadas (detalhes em [02-arquitetura-do-tracker.md](02-arquitetura-do-tracker.md)):

- TCP com uma conexão persistente por nó.
- Mensagens em JSON, uma por linha.
- 3 mensagens do nó para o tracker (`join`, `ping`, `leave`) e 7 do tracker para o
  nó (`welcome`, `error`, `pong`, `child_added`, `child_removed`,
  `parent_changed`, `stream_ended`).
- Uma única struct `Message` para todos os tipos; campos não usados somem do JSON
  com `omitempty`.

## Passo 4: a árvore como estrutura de dados pura

[`tracker/tree.go`](../tracker/tree.go) guarda a topologia e não sabe nada de
rede: só recebe "entrou o nó X" ou "saiu o nó Y" e devolve o que mudou.

Separar assim permite testar o algoritmo isoladamente, sem abrir sockets, e
deixa o código de rede livre de detalhes da árvore.

- `SetRoot`: a fonte vira a raiz.
- `Add`: busca em largura até o primeiro nó com vaga (regra trocada no passo 11).
- `Remove`: tira o nó e religa cada filho, com a subárvore junto, na vaga mais
  rasa. Devolve um `Removal` dizendo quem perdeu um filho e quem foi religado
  onde, que é exatamente o que o servidor precisa para avisar os nós.

## Passo 5: o servidor TCP

[`tracker/server.go`](../tracker/server.go) aceita conexões e usa a árvore.

1. `Serve`: laço de `Accept`, com uma goroutine `handleConn` por conexão.
2. `handleConn`: lê o `join`, registra o nó, inicia a goroutine de escrita e
   depois fica lendo `ping`/`leave`, com um prazo de leitura renovado a cada
   mensagem (é o heartbeat).
3. `join`: valida, coloca na árvore, manda `welcome` ao nó e `child_added` ao pai.
4. `remove`: tira da árvore e manda `child_removed`, `parent_changed` e
   `child_added` para quem foi afetado, ou `stream_ended` para todos, se quem
   saiu foi a fonte.
5. `writeLoop`: uma goroutine por nó que esvazia a fila de mensagens no socket.

Toda mudança na árvore acontece com o mutex travado, e ninguém escreve no socket
segurando o mutex: os avisos vão para as filas.

## Passo 6: os executáveis

- [`cmd/tracker/main.go`](../cmd/tracker/main.go): lê as opções (`-addr`,
  `-fanout`, `-heartbeat-timeout`), abre a porta e chama `Serve`.
- [`cmd/peer-sim/main.go`](../cmd/peer-sim/main.go): um nó simulado que faz join,
  manda ping a cada 5 s, imprime cada mensagem recebida (e o que um cliente de
  verdade faria com ela) e manda `leave` no Ctrl+C. Com ele dá para testar o
  tracker sem depender do cliente de vídeo.

## Passo 7: verificações estáticas

```bash
gofmt -l .
```

```bash
go vet ./...
```

```bash
go build ./...
```

Os três terminaram sem nenhuma saída, o que significa: código no formato padrão,
nenhum problema encontrado pelo `vet` e compilação sem erros.

## Passo 8: testes automatizados

Dois arquivos de teste:

- [`tracker/tree_test.go`](../tracker/tree_test.go) testa a árvore sozinha:
  preenchimento por níveis, fanout 3, erros de entrada, remoção de folha, remoção
  de nó interno preservando as subárvores, remoção da raiz e um **teste de
  estresse** (`TestRandomChurn`) com 3000 entradas e saídas aleatórias para
  fanout 1, 2 e 3, conferindo depois de cada operação que não há ciclos, que todo
  nó é alcançável a partir da raiz e que ninguém passa do fanout.
- [`tracker/server_test.go`](../tracker/server_test.go) sobe um tracker de
  verdade numa porta livre e conecta nós falsos por TCP:
  - `TestJoinLeaveAndRepair`: monta a árvore, testa ping/pong, a saída com
    `leave`, a queda de conexão (com a religação dos órfãos) e a saída da fonte;
  - `TestHeartbeatTimeout`: um nó que não manda ping é derrubado;
  - `TestJoinRejected`: peer sem fonte, role inválido, porta inválida, primeira
    mensagem que não é join, segunda fonte.

```bash
go test ./...
```

```
ok  	streaming/tracker	1.683s
```

Para garantir que os testes de rede não passam só por sorte de timing, eles
foram rodados 20 vezes seguidas:

```bash
go test -count=20 ./tracker
```

```
ok  	streaming/tracker	17.704s
```

Com `-v` aparecem os logs do tracker, e dá para ver a árvore mudando (trecho do
`TestJoinLeaveAndRepair`):

```
[n2] saiu: pediu para sair (leave)
[n4] órfão religado em n1
[n5] órfão religado em n3
árvore com 4 nó(s):
n1 127.0.0.1:9001
├── n3 127.0.0.1:9003
│   └── n5 127.0.0.1:9005
└── n4 127.0.0.1:9004
```

## Passo 9: demonstração com processos reais

O script [`scripts/demo.ps1`](../scripts/demo.ps1) compila os executáveis e sobe
o tracker e 6 nós, cada um num processo separado, como seria em máquinas
diferentes (esta é a primeira versão; o passo 11 mostra a demo depois da troca
da regra de entrada):

```bash
powershell -ExecutionPolicy Bypass -File scripts\demo.ps1
```

Roteiro: fonte n1 e peers n2 a n6 entram; o n6 entra com `-heartbeat 0` (mudo);
depois o processo do n2 é morto; por fim o processo da fonte é morto. O tracker
roda com `-heartbeat-timeout 6s` para a demonstração ser rápida.

Log do tracker (horários removidos):

```
tracker ouvindo em 127.0.0.1:8000 (fanout=2, heartbeat-timeout=6s)
[n1] entrou como source em 127.0.0.1:9001
...
[n6] entrou como peer em 127.0.0.1:9006
árvore com 6 nó(s):
n1 127.0.0.1:9001
├── n2 127.0.0.1:9002
│   ├── n4 127.0.0.1:9004
│   └── n5 127.0.0.1:9005
└── n3 127.0.0.1:9003
    └── n6 127.0.0.1:9006

[n6] saiu: sem heartbeat (timeout)                      <- 6 s depois de entrar, mudo
árvore com 5 nó(s):
n1 127.0.0.1:9001
├── n2 127.0.0.1:9002
│   ├── n4 127.0.0.1:9004
│   └── n5 127.0.0.1:9005
└── n3 127.0.0.1:9003

[n2] saiu: falha na conexão: read tcp 127.0.0.1:8000->127.0.0.1:50618:
     wsarecv: An existing connection was forcibly closed by the remote host.
[n4] órfão religado em n1
[n5] órfão religado em n3
árvore com 4 nó(s):
n1 127.0.0.1:9001
├── n3 127.0.0.1:9003
│   └── n5 127.0.0.1:9005
└── n4 127.0.0.1:9004

[n1] saiu: falha na conexão: ... forcibly closed by the remote host.
a fonte saiu; transmissão encerrada
árvore com 0 nó(s):
(árvore vazia)
```

O mesmo episódio visto pelo n4:

```
<- {"type":"welcome","id":"n4","depth":2,"parent":{"id":"n2","addr":"127.0.0.1:9002"}}
   sou n4 (profundidade 2): conectar no pai n2 em 127.0.0.1:9002
<- {"type":"parent_changed","parent":{"id":"n1","addr":"127.0.0.1:9001"}}
   pai mudou para n1 em 127.0.0.1:9001: reconectar
<- {"type":"stream_ended"}
   a fonte saiu: transmissão encerrada
conexão com o tracker encerrada: EOF
```

E pela fonte n1, que perdeu o filho n2 e ganhou o n4 no mesmo instante:

```
<- {"type":"child_added","child":{"id":"n2","addr":"127.0.0.1:9002"}}
<- {"type":"child_added","child":{"id":"n3","addr":"127.0.0.1:9003"}}
<- {"type":"child_removed","child":{"id":"n2","addr":"127.0.0.1:9002"}}
<- {"type":"child_added","child":{"id":"n4","addr":"127.0.0.1:9004"}}
```

Observações para o relatório:

- **As três formas de detecção funcionaram:** timeout de heartbeat (n6), queda
  abrupta (n2) e queda da fonte (n1).
- **O Windows envia RST ao matar um processo.** Por isso a queda aparece como
  "forcibly closed" e não como EOF. Um `Close()` normal envia FIN, e aí a leitura
  devolve EOF (é o que aparece no teste automatizado). O tracker trata os dois do
  mesmo jeito.
- **O reparo foi local:** só n4 e n5, os filhos diretos do nó que caiu, mudaram
  de pai.

## Passo 10: um bug encontrado na demonstração

Na primeira execução da demo, o log do n4 mostrou "conexão com o tracker
encerrada: EOF" **antes** do `stream_ended`, embora a mensagem tenha chegado
primeiro.

Causa: no `peer-sim`, uma goroutine lia a conexão e passava as mensagens pelo
channel `incoming` para a goroutine principal, que as imprimia. A goroutine de
leitura entregava o `stream_ended`, voltava a ler, recebia o EOF e imprimia o
erro ela mesma, às vezes antes de a principal terminar de imprimir a mensagem.
Duas goroutines escrevendo no log não têm ordem garantida entre si.

Correção: a goroutine de leitura não imprime mais nada; ela guarda o erro e
fecha o channel, e a goroutine principal imprime o erro ao ver o channel
fechado. Assim tudo sai na ordem de chegada. É um exemplo pequeno de um problema
clássico de concorrência: a ordem dos eventos em goroutines diferentes não é
garantida sem sincronização.

## Passo 11: distribuir os filhos dentro do nível (02/11)

Revendo a árvore do passo 9 apareceu um desequilíbrio: com 5 nós, a regra
"primeiro nó com vaga" punha o n4 e o n5 debaixo do n2 e deixava o n3 sem
filhos. O n2 mandava o vídeo duas vezes, o n3 nenhuma, e a queda do n2 cortava
dois nós.

Mudança: o `findParent` agora anda um nível por vez e, no primeiro nível com
vaga, escolhe o nó com menos filhos. A profundidade de cada nó continua a mesma;
a comparação completa está na seção 4 de
[02-arquitetura-do-tracker.md](02-arquitetura-do-tracker.md).

Testes ajustados:

- `TestAddFillsLevelByLevel`: o n5 agora vai para o n3 e o n6 para o n2.
- `TestRemoveLeaf` e `TestRemoveInnerNodeKeepsSubtrees`: os pais esperados
  mudaram junto com a árvore.
- `TestJoinLeaveAndRepair`: ganhou um sexto nó, para que a saída do n2 continue
  deixando dois órfãos.
- `TestAddSpreadsChildren` (novo): para árvores de 1 a 40 nós com fanout 2 e 3,
  os nós de um mesmo nível nunca diferem em mais de um filho.

Para conferir que o teste novo pega o problema, ele foi rodado contra a regra
antiga:

```
--- FAIL: TestAddSpreadsChildren (0.00s)
    tree_test.go:110: fanout 2, 5 nós: na profundidade 1 há nós com 0 e com 2 filhos
```

Com a regra nova:

```bash
go test -count=20 ./tracker
```

```
ok  	streaming/tracker	19.588s
```

A demo também mudou. Com a regra nova, o n6 mudo do roteiro antigo ia para o n2
e, depois do timeout, o n2 caía com um filho só. Agora são 6 peers e o mudo é o
n7, que fica no n3; assim o n2 cai com dois filhos. Log do tracker (horários
removidos):

```
árvore com 7 nó(s):
n1 127.0.0.1:9001
├── n2 127.0.0.1:9002
│   ├── n4 127.0.0.1:9004
│   └── n6 127.0.0.1:9006
└── n3 127.0.0.1:9003
    ├── n5 127.0.0.1:9005
    └── n7 127.0.0.1:9007

[n7] saiu: sem heartbeat (timeout)
...
[n2] saiu: falha na conexão: ... forcibly closed by the remote host.
[n4] órfão religado em n1
[n6] órfão religado em n4
árvore com 5 nó(s):
n1 127.0.0.1:9001
├── n3 127.0.0.1:9003
│   └── n5 127.0.0.1:9005
└── n4 127.0.0.1:9004
    └── n6 127.0.0.1:9006
```

O n6 foi para debaixo do n4, que era seu irmão: depois de ocupar a vaga do n2,
o n4 é o nó do nível 1 com menos filhos. Nenhum nó ficou mais longe da fonte,
mas o n6 só volta a receber vídeo novo depois que o n4 se reconectar no n1. É um
caso para o cliente tratar: um nó pode receber `parent_changed` e `child_added`
no mesmo reparo.

## Passo 12: pai vivo, mas inalcançável (22/11)

Revendo o cenário de entrada (seção 9.1 da arquitetura final) surgiu a
pergunta: o tracker não deveria confirmar que o pai está vivo antes de mandar o
`welcome` ao filho? A conclusão foi que não: a confirmação só prova que o pai
estava vivo no momento da resposta, e o filho já está registrado como filho
dele, então, se o pai morrer, o tracker manda `parent_changed` de qualquer
jeito. A discussão completa e as medições estão na seção 9.1 de
[04-arquitetura-final.md](04-arquitetura-final.md).

A análise mostrou um caso que nem a confirmação nem o heartbeat cobrem: o pai
vivo para o tracker, mas com a porta bloqueada (Firewall do Windows). O filho
tentaria para sempre. Mudanças:

- **Protocolo:** mensagem nova `parent_unreachable`, do nó para o tracker, com
  o pai que ele não alcança.
- **Árvore:** `Tree.Relocate` muda o nó de pai sem tirar o pai antigo da
  árvore. O nó guarda os pais que avisou não alcançar (`avoid`), e a BFS
  (agora em `search`, chamada por `findParent`) pula esses pais, também nos
  reparos futuros. Sem outra vaga, o nó fica onde está.
- **Servidor:** `Server.relocate` ignora avisos atrasados (o pai citado já não
  é o atual) e manda `child_removed`, `parent_changed` e `child_added`.
- **peer-sim:** agora abre a porta de escuta e testa a conexão com cada pai
  que recebe: 3 tentativas, 1 s entre elas, prazo de 2 s cada. Se todas
  falharem, manda `parent_unreachable` e volta a testar 5 s depois. A opção
  nova `-firewall` simula a porta bloqueada.

Testes novos:

- `TestRelocate`: o nó vai para o nível mais raso com vaga sem passar pelo pai
  avisado, leva a subárvore junto, ignora avisos atrasados e, num reparo
  posterior, evita o pai avisado.
- `TestRelocateWithoutOtherSlot`: sem outra vaga o nó fica; quando entra outro
  nó, o aviso seguinte funciona; se a única vaga for a do pai evitado, o reparo
  usa esse pai mesmo assim.
- `TestRandomChurn`: passou a misturar trocas de pai com as entradas e saídas.
- `TestParentUnreachable`: o mesmo fluxo por TCP, conferindo as mensagens de
  cada nó.

A demo nova `scripts/demo-firewall.ps1` sobe um n2 com `-firewall`; cada nó
que cai debaixo dele é mudado de lugar cerca de 2 s depois (log na seção 9.5 da
arquitetura final). A demo antiga continua com o mesmo log do tracker; os logs
dos peers ganharam as linhas "conectou no pai".

## Próximos passos

- Integrar com o cliente da Lina (checklist na seção 7 da arquitetura) e
  combinar quem abre a conexão de dados.
- Numerar os segmentos, para que um nó que troca de pai saiba de onde continuar.
- Testar com nós em máquinas diferentes da mesma rede (tracker com `-addr :8000`).
- Medir o tempo de reparo e comparar `-fanout 2` com `-fanout 3` para o relatório.
