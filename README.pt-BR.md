<div align="center">

# wa

**CLI + daemon de automação pessoal de WhatsApp, escrito em Go.**

Um daemon Go hexagonal que mantém uma sessão WhatsApp Multi-Device e um cliente JSON-RPC leve que conversa com ele — seguro o bastante para deixar um modelo de linguagem enviar mensagens em seu nome, à prova de queda de energia no meio de uma migração, e paranoico o bastante para recusar toda flag destrutiva que você imaginar.

[Quickstart](#quickstart) · [Instalação](#instalação) · [Agentes de IA (MCP)](#agentes-de-ia-mcp) · [Documentação completa (inglês)](./README.md) · [Manual](./docs/manual.md) · [Segurança](./SECURITY.md)

</div>

---

> Este é o guia rápido em português do Brasil. A documentação canônica e
> sempre atualizada é a versão em inglês: [README.md](./README.md).

## O que é

Dois binários, um repositório:

- **`wad`** — daemon de longa duração que mantém a sessão WhatsApp, o banco SQLite de chaves e o websocket com `web.whatsapp.com`. Roda sob `systemd` (Linux), `launchd` (macOS) ou módulo NixOS. Uma instância por perfil, **nunca como root**.
- **`wa`** — cliente JSON-RPC leve que fala com o `wad` por um socket Unix. É o que seus scripts shell, cron jobs e agentes de IA invocam de verdade.

Construído sobre [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) — a biblioteca que move o `mautrix-whatsapp` em escala de produção.

## Por que `wa` e não um gateway REST?

Se você procura um gateway multi-tenant (Evolution API, WAHA), este projeto resolve um problema diferente: **automação pessoal com trilho de segurança inegociável**, pensada para ser entregue a um agente de IA sem medo.

| O que importa | `wa` |
|---|---|
| Binário único estático (`CGO_ENABLED=0`), ~30 MB RSS por perfil | sim — sem Postgres, sem Redis, sem Docker obrigatório |
| Allowlist **default-deny** por ação (`send`, `read`, …) | sim — nada sai sem permissão explícita |
| Rate limiter não-burlável (2/s, burst 2; 30/min, burst 30; sem teto diário de envio comum) + rampa de aquecimento | sim — não existe `--force` em lugar nenhum |
| Log de auditoria append-only (JSON Lines) | sim — toda ação fica registrada |
| Envios de agente IA **propostos como rascunho**, aprovados por humano | sim — o diferencial (veja abaixo) |
| Releases assinados (SLSA L2 + Sigstore) + SBOM duplo | sim — verificável com `gh attestation verify` |

**Honestidade sobre banimento:** nenhum transporte de engenharia reversa é imune a bloqueio — desconfie de quem promete o contrário. O que o `wa` oferece é a única pipeline de higiene *imposta por código* do mercado: aquecimento gradual de sessão nova, limites de taxa, allowlist e auditoria, abaixo de todo caminho de RPC.

## Quickstart

```bash
# Instalar (Homebrew — macOS + Linuxbrew)
brew install yolo-labz/tap/wa

# Ou via Nix (recomendado para NixOS/nix-darwin)
nix profile install github:yolo-labz/wa

# Ou o instalador com verificação de checksum (80 linhas — inspecione antes se quiser)
curl -fsSL https://raw.githubusercontent.com/yolo-labz/wa/main/install.sh | bash

# Ou Docker — um único container distroless (~12 MB); /data guarda a sessão
docker compose up -d   # veja docker-compose.yaml; pareie via `docker compose exec`

# Subir o daemon (perfil default)
wad &

# Parear seu telefone — QR code no terminal
wa pair

# Liberar um contato (política default-deny: sem isso, nada é enviado)
wa allow add 5511999999999@s.whatsapp.net --actions send

# Enviar uma mensagem
wa send --to 5511999999999@s.whatsapp.net --body "olá do wa"

# Instalar como serviço persistente do sistema
wad install-service --profile default
```

O flag de destinatário se escreve `--to`, `--jid` ou `--group` conforme o comando; `--chat <jid>` é aceito como apelido universal em todos.

## Agentes de IA (MCP)

O `wa` fala [MCP](https://modelcontextprotocol.io/) nativamente — adicione ao Claude Desktop/Code ou Cursor:

```json
{"mcpServers": {"wa": {"command": "wa", "args": ["mcp", "serve"]}}}
```

Por padrão o agente **não envia nada**: ele *propõe* mensagens para uma fila de revisão humana. Nada sai até você rodar:

```bash
wa draft list      # ver o que o agente quer enviar
wa draft approve   # aprovar (ou `wa draft reject`)
```

Esse portão de rascunho é imposto no daemon — não existe flag do lado do agente que o desligue.

## O que NÃO é

- **Não** é ferramenta de disparo em massa. O rate limiter é inegociável.
- **Não** é SaaS multi-tenant. Cada instalação atende uma pessoa (com perfis isolados para separar trabalho/pessoal).
- **Não** é a API oficial do WhatsApp Cloud. Usa o protocolo Multi-Device de engenharia reversa via `whatsmeow`.

## Mais

- Tour completo (multi-perfil, shell completion, migração, auditoria): [`docs/manual.md`](./docs/manual.md) (inglês)
- Postura de segurança e como verificar os releases assinados: [`SECURITY.md`](./SECURITY.md)
- Como o `wa` se compara a Evolution/WAHA/whatsmeow puro: [README.md §How `wa` compares](./README.md#how-wa-compares)
