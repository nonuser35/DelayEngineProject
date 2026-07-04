🇧🇷 [Português](README.pt-BR.md) | 🇬🇧 [English](README.en.md)

💙 Apoie o projeto / Support the project

Se você quiser apoiar o desenvolvimento do DelayEngine, você pode contribuir via Pix ou PayPal. Qualquer ajuda é muito bem-vinda, seja financeira ou contribuindo com código e melhorias.

🇧🇷 Pix (Brasil)

Escaneie o QR Code abaixo para apoiar via Pix:

🌍 PayPal

Você também pode apoiar via PayPal:

🔗 Links diretos
Pix QR Code:
- 🇧🇷 👉[Pix (Brasil)](https://github.com/nonuser35/DelayEngineProject/blob/main/support/pix-qrcode.png)
PayPal QR Code:
- 🌍 👉 [PayPal](https://github.com/nonuser35/DelayEngineProject/blob/main/support/paypal-qrcode.png)
---

# DelayEngine

> Português / English

---

DelayEngine é um aplicativo local para Windows que permite adicionar ou remover delay durante uma live, usando um vídeo de loading como transição, sem precisar reiniciar a transmissão.

Ele foi criado para funcionar entre OBS/Streamlabs e Twitch:

```text
OBS / Streamlabs
↓
MediaMTX local
↓
DelayEngine
↓
Twitch
```

## Objetivo

O objetivo do DelayEngine é dar ao host controle sobre o atraso da live durante a transmissão.

Com ele, o streamer pode:

- iniciar a live normalmente;
- adicionar delay no meio da live;
- mostrar um vídeo de loading enquanto o delay entra;
- voltar ao vivo depois;
- preparar vídeos de loading no formato correto;
- gerenciar tudo por uma interface web local.

## Principais recursos

- Painel web local.
- App com ícone no tray do Windows.
- Integração com OBS/Streamlabs.
- MediaMTX local incluso.
- Modo Twitch polido para saída mais estável.
- Delay com vídeo de loading.
- Opção de tocar loading completo.
- Conversor de vídeos para FLV H264/AAC.
- Detecção automática de resolução/FPS/bitrate da live.
- Preview local da live recebida.
- Logs separados por área.
- Buffer de segurança em disco de até 1 hora.
- Script para limpar dados antes de compartilhar a pasta.

## Rodando localmente

O DelayEngine roda inteiramente no computador do usuário.

Ele não precisa de servidor externo para transmitir, aplicar delay, converter vídeo ou guardar buffer.

A única busca externa opcional feita pela interface é o card público de apoio/contribuição financeira do projeto, carregado do GitHub. Se a internet falhar, isso não afeta a live.

## Como funciona o buffer

O DelayEngine mantém uma janela de pacotes recentes em disco.

Esse buffer:

- guarda pacotes codificados;
- preserva PTS/DTS;
- preserva keyframes;
- remove pacotes antigos automaticamente;
- mantém aproximadamente a última 1 hora da live;
- não cresce indefinidamente;
- é limpo ao iniciar o app.

Esse buffer de 1 hora é uma margem de segurança do pipeline, não uma gravação permanente.

## Como funciona o delay

Quando o usuário adiciona delay, o DelayEngine espera o buffer chegar ao tempo necessário e passa a publicar um ponto atrasado da live.

Se um vídeo de loading for configurado, ele aparece enquanto o delay entra.

Quando o usuário clica em Voltar ao vivo, o DelayEngine descarta o atraso acumulado e volta para o ponto recente da transmissão.

## Modo Twitch polido

O modo Twitch polido é o modo recomendado para transmissões reais.

Nele, o DelayEngine publica em uma saída local, e um codificador local mantém uma live contínua para a Twitch.

Isso ajuda a reduzir problemas de loading, queda ou acúmulo de latência quando o host entra e sai do delay manual.

## Conversor de vídeos

O app inclui um conversor para preparar vídeos de loading.

Ele pode:

- transformar MP4 em FLV compatível;
- ajustar resolução;
- ajustar FPS;
- ajustar bitrate;
- usar áudio AAC;
- repetir vídeo curto;
- cortar vídeo longo;
- salvar o resultado em `videos/ready`.

O objetivo é fazer o vídeo de loading combinar com a live, evitando troca visual estranha.

## Como usar

1. Baixe a pasta do app.
2. Extraia o ZIP.
3. Abra `DelayEngine.exe`.
4. No painel, salve sua stream key da Twitch.
5. Vá em Dados para OBS.
6. Copie o servidor local e a chave local.
7. No OBS/Streamlabs, use transmissão customizada/personalizada.
8. Cole o servidor local no campo Servidor.
9. Cole a chave local no campo Chave.
10. Inicie a live no OBS/Streamlabs.
11. Use o painel do DelayEngine para adicionar ou remover delay.

## Importante

No OBS/Streamlabs, não cole a chave da Twitch.

O OBS deve enviar para o DelayEngine. A chave da Twitch fica salva no app, e o app cuida da saída para a Twitch.

## Privacidade

Dados do usuário ficam no próprio computador:

- stream key;
- configurações;
- logs;
- vídeos convertidos;
- buffer temporário.

O app não precisa enviar esses dados para nenhum servidor externo.

## Limpeza antes de compartilhar

A pasta inclui:

```text
limpeza-de-dados.cmd
```

Esse script remove dados pessoais e deixa a pasta pronta para ser enviada a outra pessoa.

## Licença

Projeto open source sob licença MIT.

Veja:

- `LICENSE`
- `NOTICE`
- `THIRD_PARTY_NOTICES.md`

---

## Documentação técnica / Technical Documentation

Para documentação técnica detalhada sobre arquitetura, buffer, delay, Twitch polished mode, converter, privacy e fluxo de uso:

- `DOCUMENTACAO_COMPLETA.md` (Português)
- Seções técnicas em inglês disponíveis em `README.en.md`

Regras principais:

- The delay pipeline must not alter video payloads.
- The delay pipeline must not alter audio payloads.
- The delay pipeline must not recode live packets.
- MediaMTX compatibility must be preserved.
- Every change should keep `go test ./...` passing.

## Configuração / Configuration

Variáveis de ambiente / Environment variables:

- `DELAYENGINE_INPUT_URL`: RTMP input URL. Default: `rtmp://127.0.0.1/live/source`
- `DELAYENGINE_OUTPUT_URL`: RTMP output URL. Default: `rtmp://127.0.0.1:1935/live/delayed`
- `DELAYENGINE_HTTP_ADDR`: HTTP API address. Default: `:8080`
- `DELAYENGINE_READ_TIMEOUT`: RTMP read timeout. Default: `10s`
- `DELAYENGINE_WRITE_TIMEOUT`: RTMP write timeout. Default: `10s`
- `DELAYENGINE_FIXED_DELAY`: fixed output delay. Default: `5s`
- `DELAYENGINE_DELAY_ENABLED`: start with delay enabled. Default: `false`

## Como executar / How to run

```sh
go run ./cmd/delayengine
```

Com uma stream MediaMTX publicada em `live/teste`:

```powershell
$env:DELAYENGINE_INPUT_URL="rtmp://127.0.0.1:1935/live/teste"
$env:DELAYENGINE_OUTPUT_URL="rtmp://127.0.0.1:1935/live/delayed"
go run ./cmd/delayengine
```

Status esperado / Expected status:

```text
RTMP pipeline status status=ok input=ok buffer=ok output=ok delay_enabled=false delay=5s queued_for_delay=... buffer_duration=... published_audio=... published_video=...
```

## HTTP API

A API inicia em `DELAYENGINE_HTTP_ADDR`, padrão `:8080`.

```powershell
Invoke-RestMethod http://127.0.0.1:8080/status
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/on
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/off
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body '{"delay":"5s"}'
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body '{"seconds":30}'
```

Endpoints:

- `GET /status`
- `POST /delay/on`
- `POST /delay/off`
- `POST /delay/set`

## Biblioteca / Library choice

RTMP é implementado com `github.com/bluenviron/gortmplib v0.4.0`.

Por quê:

- Mantida pelo mesmo autor/ecossistema do MediaMTX.
- O próprio MediaMTX depende de gortmplib para suporte RTMP.
- Expõe callbacks de mídia codificada para H264, H265, AAC/MPEG-4 Audio e outros codecs RTMP.
- Expõe timestamps RTMP, H264/H265 PTS/DTS, AAC PTS e informações de keyframe de vídeo.
