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

## Resumo curto para GitHub

DelayEngine é um app local para Windows que fica entre OBS/Streamlabs e Twitch, permitindo adicionar e remover delay durante uma live com vídeo de loading, buffer em disco e painel web, sem depender de servidor externo para operar a transmissão.

---

# DelayEngine

DelayEngine is a local Windows app that lets streamers add or remove delay during a live stream, using a loading video as a transition, without restarting the broadcast.

It is designed to run between OBS/Streamlabs and Twitch:

```text
OBS / Streamlabs
↓
Local MediaMTX
↓
DelayEngine
↓
Twitch
```

## Goal

The goal of DelayEngine is to give the host control over stream delay while the broadcast is already running.

With it, the streamer can:

- start the stream normally;
- add delay in the middle of the live stream;
- show a loading video while delay is being applied;
- return live afterward;
- prepare loading videos in the correct format;
- manage everything from a local web interface.

## Main Features

- Local web panel.
- Windows tray app.
- OBS/Streamlabs integration.
- Bundled local MediaMTX.
- Polished Twitch mode for a more stable output.
- Delay with loading video transition.
- Full loading option.
- Video converter to FLV H264/AAC.
- Automatic detection of stream resolution/FPS/bitrate.
- Local preview of the received stream.
- Logs separated by area.
- Disk safety buffer up to 1 hour.
- Data-cleaning script for sharing a clean folder.

## Runs Locally

DelayEngine runs entirely on the user's computer.

It does not need an external server to stream, apply delay, convert videos or keep the buffer.

The only optional external fetch made by the interface is the public support/contribution card from GitHub. If the internet is unavailable, this does not affect the live stream.

## How the Buffer Works

DelayEngine keeps a recent packet window on disk.

This buffer:

- stores encoded packets;
- preserves PTS/DTS;
- preserves keyframes;
- automatically removes old packets;
- keeps about the latest 1 hour of the stream;
- does not grow forever;
- is cleared when the app starts.

This 1-hour buffer is a pipeline safety margin, not a permanent recording.

## How Delay Works

When the user adds delay, DelayEngine waits until the buffer has enough content and then publishes an older point of the stream.

If a loading video is configured, it appears while delay is being applied.

When the user clicks Go live, DelayEngine discards the accumulated delay and returns to the recent point of the stream.

## Polished Twitch Mode

Polished Twitch mode is the recommended mode for real broadcasts.

In this mode, DelayEngine publishes to a local output, and a local encoder keeps one continuous stream going to Twitch.

This helps reduce loading, dropouts or latency accumulation when the host enters and leaves manual delay.

## Video Converter

The app includes a converter for preparing loading videos.

It can:

- convert MP4 to compatible FLV;
- adjust resolution;
- adjust FPS;
- adjust bitrate;
- use AAC audio;
- repeat short videos;
- cut long videos;
- save the result in `videos/ready`.

The goal is to make the loading video match the stream, avoiding awkward visual transitions.

## How to Use

1. Download the app folder.
2. Extract the ZIP.
3. Open `DelayEngine.exe`.
4. Save your Twitch stream key in the panel.
5. Go to OBS Data.
6. Copy the local server and local key.
7. In OBS/Streamlabs, use custom stream settings.
8. Paste the local server into the Server field.
9. Paste the local key into the Key field.
10. Start streaming in OBS/Streamlabs.
11. Use DelayEngine's panel to add or remove delay.

## Important

Do not paste the Twitch key into OBS/Streamlabs.

OBS should send to DelayEngine. The Twitch key stays saved in the app, and the app handles the Twitch output.

## Privacy

User data stays on the user's own computer:

- stream key;
- settings;
- logs;
- converted videos;
- temporary buffer.

The app does not need to send this data to any external server.

## Cleanup Before Sharing

The folder includes:

```text
limpeza-de-dados.cmd
```

This script removes personal data and makes the folder ready to be shared with someone else.

## License

Open source project under the MIT License.

See:

- `LICENSE`
- `NOTICE`
- `THIRD_PARTY_NOTICES.md`

## Short GitHub Summary

DelayEngine is a local Windows app that sits between OBS/Streamlabs and Twitch, allowing streamers to add and remove delay during a live broadcast with a loading video, disk buffering and a web panel, without relying on an external server to operate the stream.
