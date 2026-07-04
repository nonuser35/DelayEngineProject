# DelayEngine - Documentação completa

> Idiomas: Português / English

---

## English version

See the English documentation after the Portuguese section.

---

## O que é o DelayEngine

O DelayEngine é um aplicativo local para Windows feito para controlar delay durante uma live. Ele fica entre o OBS/Streamlabs e a Twitch.

Fluxo principal:

```text
OBS / Streamlabs
↓
MediaMTX local
↓
DelayEngine
↓
Twitch
```

A proposta principal é permitir que o host adicione ou remova delay durante a transmissão, usando um vídeo de loading como transição, sem precisar reiniciar a live.

## O que roda localmente

O DelayEngine roda inteiramente no computador da pessoa.

Ele não depende de servidor externo para:

- receber a live do OBS/Streamlabs;
- guardar o buffer;
- aplicar delay;
- enviar para a Twitch;
- converter vídeos de loading;
- mostrar o painel web;
- guardar configurações locais;
- guardar a stream key protegida no Windows.

A única busca externa opcional feita pelo painel é a atualização do card de apoio/contribuição financeira, que tenta carregar dados públicos de um arquivo no GitHub do projeto. Se não houver internet ou se o arquivo não existir, o card simplesmente não aparece ou usa o cache local. Isso não interfere na live.

## Componentes principais

### MediaMTX local

O MediaMTX recebe a live enviada pelo OBS/Streamlabs em RTMP. Ele roda localmente junto com o app.

O OBS não manda direto para a Twitch. Ele manda para um servidor local, por exemplo:

```text
Servidor: rtmp://127.0.0.1:1935
Chave: live/delayengine-xxxx
```

O DelayEngine lê esse stream local.

### DelayEngine

O DelayEngine lê os pacotes RTMP codificados da live, guarda em buffer e republica para a saída configurada.

Na lógica principal de delay, ele não decodifica nem recodifica áudio/vídeo. Ele trabalha com pacotes codificados, preservando:

- áudio;
- vídeo;
- PTS/DTS;
- keyframes;
- sincronização de áudio e vídeo;
- mensagens RTMP necessárias para publicação.

### Modo Twitch polido

O modo Twitch polido é o modo recomendado para uso real na Twitch.

Nesse modo, o DelayEngine não envia diretamente a troca brusca para a Twitch. Ele publica primeiro em uma saída local:

```text
DelayEngine
↓
saída RTMP local
↓
codificador local
↓
Twitch
```

O codificador local mantém uma transmissão contínua para a Twitch. Isso ajudou nos testes a reduzir o problema da Twitch acumular atraso depois de entrar e sair do delay manual.

Na prática:

- o OBS envia para o DelayEngine;
- o DelayEngine aplica ou remove delay;
- o codificador local mantém a saída para Twitch mais estável;
- a live tende a voltar ao delay natural da Twitch depois de sair do delay manual.

Esse modo usa FFmpeg/ffprobe embutidos apenas para a saída polida/conversão. A lógica principal do buffer e delay da live continua trabalhando com pacotes, sem recodificar no caminho base.

## Buffer de segurança de 1 hora

O buffer de 1 hora é um buffer de segurança em disco. Ele não significa que o usuário deve usar 1 hora de delay manual.

O app cria esse buffer em:

```text
runtime/buffer
```

Cada pacote recebido é gravado em segmentos de disco. O app guarda junto:

- tipo do pacote: áudio ou vídeo;
- codec;
- PTS;
- DTS;
- informação de keyframe;
- payload codificado;
- mensagem RTMP original.

A janela é móvel:

```text
Live com 10 min  -> buffer tem até 10 min
Live com 40 min  -> buffer tem até 40 min
Live com 1h20min -> buffer mantém aproximadamente a última 1h
```

Quando passa de 1 hora, o DelayEngine descarta os pacotes mais antigos. Quando um arquivo de segmento não contém mais pacotes úteis, ele é apagado.

Isso evita crescimento infinito em disco.

O buffer de 1 hora serve para:

- dar margem de segurança para a live;
- manter pacotes recentes disponíveis;
- permitir transições com loading;
- ajudar em ressincronizações;
- evitar depender só de memória RAM;
- proteger o pipeline durante uma live longa.

Ele não serve como gravação permanente e é limpo quando o app inicia.

## Delay manual

O delay manual é o atraso que o host escolhe no painel.

Exemplo:

```text
Host escolhe 30s
↓
O público vê loading
↓
DelayEngine espera o buffer atingir o ponto correto
↓
A live volta com aproximadamente 30s de atraso manual
```

Quando o delay entra, o DelayEngine precisa garantir que existe conteúdo suficiente no buffer. Se o buffer ainda não tiver o tempo necessário, o vídeo de loading pode continuar tocando ou repetir até o ponto estar pronto.

## Voltar ao vivo

Quando o host clica em "Voltar ao vivo", o DelayEngine descarta o atraso acumulado e volta para o ponto mais recente seguro da live.

No modo Twitch polido, a ideia é voltar ao vivo mantendo a saída para Twitch o mais contínua possível, para evitar que a Twitch aumente demais o delay natural.

Fluxo:

```text
Live com delay manual
↓
Voltar ao vivo
↓
DelayEngine descarta fila atrasada
↓
Publicação volta ao ponto recente
↓
Twitch continua recebendo pelo modo polido
```

## Vídeo de loading

O vídeo de loading é uma transição visual exibida enquanto o delay entra ou sai.

Ele precisa combinar com a live para evitar cortes estranhos:

- mesma resolução;
- mesmo FPS;
- áudio AAC;
- vídeo H264;
- FLV pronto para RTMP.

O app inclui um conversor para preparar esse vídeo automaticamente.

## Loading completo

Existe a opção "Loading completo".

Com ela desligada:

- o vídeo de loading toca só até o buffer estar pronto;
- se o delay estiver pronto antes do fim do vídeo, a live volta antes;
- é o modo mais direto.

Com ela ligada:

- o vídeo de loading toca inteiro;
- a live volta depois do final do vídeo;
- serve para uma transição mais profissional ou para exibir um aviso completo.

Se o delay estiver em 0s e "Loading completo" estiver ligado, o vídeo funciona como uma vinheta/aviso. Ele toca e depois volta para a live ao vivo normal.

## Conversor de vídeos

O conversor prepara vídeos comuns, como MP4, para virarem loading compatível.

Ele faz:

- ajuste de resolução;
- ajuste de FPS;
- ajuste de bitrate;
- áudio AAC;
- saída em FLV;
- repetição automática se o vídeo for curto;
- corte automático se o vídeo for longo.

Exemplo:

```text
Video original: 8s
Duração escolhida: 30s
Resultado: vídeo repetido até 30s
```

```text
Video original: 2min
Duração escolhida: 30s
Resultado: vídeo cortado em 30s
```

Os vídeos prontos ficam em:

```text
videos/ready
```

O app também tenta detectar automaticamente a qualidade da live recebida para sugerir o melhor formato de conversão.

## Perfil automático da live

Quando a live chega do OBS/Streamlabs, o DelayEngine tenta detectar:

- resolução;
- FPS;
- bitrate aproximado;
- codec de vídeo;
- codec de áudio.

Esses dados ajudam o conversor a preparar um vídeo de loading mais compatível.

Se necessário, o usuário pode editar manualmente.

## Perfil automático do codificador

No modo Twitch polido, o app também usa dados da live para configurar o codificador local.

Na primeira live, ele pode precisar esperar a transmissão chegar para detectar o formato. Nas próximas vezes, ele usa o perfil salvo.

O usuário também pode editar manualmente:

- largura;
- altura;
- FPS;
- bitrate de vídeo;
- bitrate de áudio.

## Painel web

O painel web é a interface de controle.

Ele mostra:

- status geral;
- conexão da live;
- modo delay;
- buffer;
- saída;
- saúde da transmissão;
- preview local;
- logs;
- vídeos de loading;
- configurações;
- modo Twitch polido;
- conversor de vídeos;
- tutorial e FAQ.

O painel é apenas controle visual. Se o navegador for fechado, o app pode continuar rodando pelo ícone perto do relógio do Windows.

## Preview local

O preview local mostra a live recebida pelo DelayEngine.

Ele serve para confirmar que o OBS/Streamlabs está chegando no app.

Importante:

- o preview local não mostra a saída final da Twitch;
- ele não inclui o vídeo de loading;
- ele não representa exatamente o que o público vê;
- ele serve como diagnóstico local.

## Logs

O painel tem abas de logs para:

- DelayEngine;
- MediaMTX;
- live.

Os logs são limitados/rotacionados para evitar crescimento infinito.

## Tray do Windows

O app pode rodar minimizado no tray, perto do relógio do Windows.

Pelo tray, o usuário pode:

- abrir o painel;
- voltar ao vivo;
- abrir logs;
- sair do app.

Isso permite fechar o navegador sem parar a transmissão.

## Chave da Twitch

A stream key da Twitch é salva localmente e protegida pelo Windows, quando o usuário marca a opção de salvar.

Ela não deve ser colocada no OBS/Streamlabs. No fluxo do DelayEngine:

- OBS usa a chave local do DelayEngine;
- DelayEngine guarda a chave real da Twitch;
- DelayEngine/codificador envia para a Twitch.

## Privacidade

O DelayEngine foi pensado para rodar localmente.

Dados que ficam no computador:

- stream key da Twitch;
- configurações;
- logs;
- vídeos convertidos;
- buffer temporário;
- perfil do codificador.

O app não precisa enviar esses dados para servidor externo.

A única busca externa opcional é o card público de apoio/contribuição carregado do GitHub do projeto. Ele pode conter foto, nome, links, QR Code e chave Pix/apoio. Se falhar, a live continua normal.

## Limpeza de dados

A pasta final inclui:

```text
limpeza-de-dados.cmd
```

Esse script remove dados do usuário, como:

- chave Twitch salva;
- configurações locais;
- logs;
- runtime;
- tmp;
- vídeos convertidos em videos/ready.

Ele serve para deixar a pasta limpa antes de compartilhar com outra pessoa.

## Como usar pela primeira vez

1. Abra `DelayEngine.exe`.
2. No painel, vá em Configurações.
3. Cole a chave da Twitch e salve.
4. Vá em Dados para OBS.
5. Copie o servidor local.
6. Copie a chave local.
7. No OBS/Streamlabs, configure transmissão customizada/personalizada.
8. Cole o servidor local no campo Servidor.
9. Cole a chave local no campo Chave.
10. Inicie a transmissão no OBS/Streamlabs.
11. Confira no painel se a live chegou.
12. Crie um video de loading pela opcao de conversao logo abaixo no site(tenha um video de loading, a preferencia do dono)
13. Use Adicionar delay com loading ou Voltar ao vivo.

## Como adicionar delay

1. Escolha o tempo na barra de delay.
2. Escolha o vídeo de loading.
3. Clique em Adicionar delay com loading.
4. O público verá o loading.
5. A live volta com delay.

## Como remover delay

1. Clique em Voltar ao vivo.
2. O app descarta o atraso acumulado.
3. A transmissão volta para o ponto recente da live.

## Como preparar loading

1. Abra a aba Conversor de vídeos.
2. Escolha um arquivo MP4 ou outro vídeo comum.
3. Deixe o perfil automático ou ajuste manualmente.
4. Escolha a duração.
5. Clique para converter.
6. Ative o vídeo convertido como loading.

## Resumo técnico

```text
OBS/Streamlabs
↓ RTMP local
MediaMTX
↓ pacotes codificados
DelayEngine
↓ buffer em disco até 1h
Delay/manual/loading
↓ saída local
Codificador local no modo Twitch polido
↓ RTMP
Twitch
```

O diferencial do projeto é controlar delay durante a live, com transição visual, mantendo a lógica central baseada em pacotes e rodando no PC do usuário.

---

# DelayEngine - Complete Documentation

## What DelayEngine Is

DelayEngine is a local Windows application for controlling delay during a live stream. It sits between OBS/Streamlabs and Twitch.

Main flow:

```text
OBS / Streamlabs
↓
Local MediaMTX
↓
DelayEngine
↓
Twitch
```

The main goal is to let the host add or remove delay while the stream is already live, using a loading video as a visual transition, without restarting the broadcast.

## What Runs Locally

DelayEngine runs entirely on the user's computer.

It does not need an external server to:

- receive the OBS/Streamlabs stream;
- keep the buffer;
- apply delay;
- send to Twitch;
- convert loading videos;
- show the web panel;
- store local settings;
- store the Twitch stream key protected by Windows.

The only optional external request made by the panel is the public support/contribution card, loaded from the project's GitHub. If there is no internet connection or the file does not exist, the card simply does not appear or uses local cache. This does not affect the stream.

## Main Components

### Local MediaMTX

MediaMTX receives the stream sent by OBS/Streamlabs over RTMP. It runs locally with the app.

OBS does not send directly to Twitch. It sends to a local server, for example:

```text
Server: rtmp://127.0.0.1:1935
Key: live/delayengine-xxxx
```

DelayEngine reads this local stream.

### DelayEngine

DelayEngine reads encoded RTMP packets from the live stream, stores them in a buffer, and republishes them to the configured output.

In the main delay path, it does not decode or recode audio/video. It works with encoded packets and preserves:

- audio;
- video;
- PTS/DTS;
- keyframes;
- audio/video sync;
- RTMP messages needed for publishing.

### Polished Twitch Mode

Polished Twitch mode is the recommended mode for real Twitch usage.

In this mode, DelayEngine first publishes to a local output:

```text
DelayEngine
↓
local RTMP output
↓
local encoder
↓
Twitch
```

The local encoder keeps one continuous stream going to Twitch. In testing, this helped reduce Twitch latency accumulation after entering and leaving manual delay.

In practice:

- OBS sends to DelayEngine;
- DelayEngine applies or removes delay;
- the local encoder keeps the Twitch output more stable;
- the stream tends to return closer to Twitch's natural latency after manual delay is removed.

This mode can use bundled FFmpeg/ffprobe for the polished output and conversion tools. The core buffer/delay logic still works packet-based and does not recode the base live path.

## 1-Hour Safety Buffer

The 1-hour buffer is a disk safety buffer. It does not mean the user should use 1 hour of manual delay.

The app creates this buffer at:

```text
runtime/buffer
```

Each received packet is written into disk segments. The app stores:

- packet type: audio or video;
- codec;
- PTS;
- DTS;
- keyframe information;
- encoded payload;
- original RTMP message.

The window moves over time:

```text
Stream running for 10 min  -> buffer has up to 10 min
Stream running for 40 min  -> buffer has up to 40 min
Stream running for 1h20min -> buffer keeps about the latest 1h
```

After 1 hour, DelayEngine discards the oldest packets. When a segment file no longer contains useful packets, it is deleted.

This prevents infinite disk growth.

The 1-hour buffer is used to:

- provide a safety margin for the stream;
- keep recent packets available;
- support loading transitions;
- help resynchronization;
- avoid relying only on RAM;
- protect the pipeline during long streams.

It is not a permanent recording and it is cleared when the app starts.

## Manual Delay

Manual delay is the delay selected by the host in the panel.

Example:

```text
Host selects 30s
↓
Viewers see the loading video
↓
DelayEngine waits until the buffer has the correct point
↓
The stream returns with about 30s of manual delay
```

When delay is enabled, DelayEngine must make sure there is enough content in the buffer. If the buffer is not ready yet, the loading video can continue or repeat until the delayed point is available.

## Going Live Again

When the host clicks "Go live", DelayEngine discards the accumulated delay and returns to the most recent safe point of the stream.

In polished Twitch mode, the goal is to return live while keeping the Twitch output as continuous as possible, avoiding unnecessary extra latency.

Flow:

```text
Stream with manual delay
↓
Go live
↓
DelayEngine discards delayed queue
↓
Publishing returns to the recent point
↓
Twitch keeps receiving through polished mode
```

## Loading Video

The loading video is a visual transition displayed while delay enters or exits.

It should match the stream to avoid awkward transitions:

- same resolution;
- same FPS;
- AAC audio;
- H264 video;
- FLV ready for RTMP.

The app includes a converter to prepare this video automatically.

## Full Loading

There is a "Full loading" option.

When disabled:

- the loading video plays only until the buffer is ready;
- if delay is ready before the video ends, the stream returns earlier;
- this is the most direct mode.

When enabled:

- the loading video plays until the end;
- the stream returns after the video finishes;
- useful for a more professional transition or complete announcement.

If delay is set to 0s and "Full loading" is enabled, the video works like an intro/announcement. It plays and then returns to the normal live stream.

## Video Converter

The converter prepares common videos, such as MP4 files, to become compatible loading videos.

It can:

- convert to compatible FLV;
- adjust resolution;
- adjust FPS;
- adjust bitrate;
- use AAC audio;
- repeat short videos;
- cut long videos;
- save the result in `videos/ready`.

Example:

```text
Original video: 8s
Selected duration: 30s
Result: video repeated until 30s
```

```text
Original video: 2min
Selected duration: 30s
Result: video cut at 30s
```

The app also tries to automatically detect the received stream quality to suggest the best conversion format.

## Automatic Stream Profile

When the OBS/Streamlabs stream arrives, DelayEngine tries to detect:

- resolution;
- FPS;
- approximate bitrate;
- video codec;
- audio codec.

These values help the converter prepare a loading video that better matches the live stream.

The user can still edit the values manually if needed.

## Automatic Encoder Profile

In polished Twitch mode, the app also uses live stream data to configure the local encoder.

On first use, it may need to wait for the stream to arrive before detecting the format. On later runs, it uses the saved profile.

The user can manually edit:

- width;
- height;
- FPS;
- video bitrate;
- audio bitrate.

## Web Panel

The web panel is the control interface.

It shows:

- overall status;
- stream connection;
- delay mode;
- buffer;
- output;
- stream health;
- local preview;
- logs;
- loading videos;
- settings;
- polished Twitch mode;
- video converter;
- tutorial and FAQ.

The panel is only the visual controller. If the browser is closed, the app can keep running from the Windows tray icon.

## Local Preview

The local preview shows the stream received by DelayEngine.

It is used to confirm that OBS/Streamlabs is reaching the app.

Important:

- the local preview does not show the final Twitch output;
- it does not include the loading video;
- it does not represent exactly what viewers see;
- it is a local diagnostic preview.

## Logs

The panel has log tabs for:

- DelayEngine;
- MediaMTX;
- live stream.

Logs are limited/rotated to avoid infinite growth.

## Windows Tray

The app can run minimized in the Windows tray, near the clock.

From the tray, the user can:

- open the panel;
- go live now;
- open logs;
- exit the app.

This allows the browser to be closed without stopping the stream.

## Twitch Key

The Twitch stream key is stored locally and protected by Windows when the user enables saving.

It should not be placed in OBS/Streamlabs. In the DelayEngine flow:

- OBS uses the local DelayEngine key;
- DelayEngine stores the real Twitch key;
- DelayEngine/encoder sends to Twitch.

## Privacy

DelayEngine is designed to run locally.

Data kept on the user's computer:

- stream key;
- settings;
- logs;
- converted videos;
- temporary buffer;
- encoder profile.

The app does not need to send these values to any external server.

The only optional external fetch is the public support/contribution card loaded from the project's GitHub. It can contain photo, name, links, QR Code and Pix/support key. If it fails, the stream continues normally.

## Data Cleanup

The final app folder includes:

```text
limpeza-de-dados.cmd
```

This script removes user data, such as:

- saved Twitch key;
- local settings;
- logs;
- runtime;
- tmp;
- converted videos in `videos/ready`.

It is useful for cleaning the folder before sharing it with someone else.

## First-Time Setup

1. Open `DelayEngine.exe`.
2. In the panel, go to Settings.
3. Paste and save the Twitch key.
4. Go to OBS Data.
5. Copy the local server.
6. Copy the local key.
7. In OBS/Streamlabs, use custom stream settings.
8. Paste the local server into the Server field.
9. Paste the local key into the Key field.
10. Start streaming in OBS/Streamlabs.
11. Check the panel to confirm that the stream arrived.
12. Use Add delay with loading or Go live.

## Adding Delay

1. Choose the delay duration with the slider.
2. Choose the loading video.
3. Click Add delay with loading.
4. Viewers see the loading video.
5. The stream returns delayed.

## Removing Delay

1. Click Go live.
2. The app discards accumulated delay.
3. The stream returns to the recent live point.

## Preparing a Loading Video

1. Open the Video Converter tab.
2. Choose an MP4 or another common video file.
3. Keep the automatic profile or adjust manually.
4. Choose the duration.
5. Convert.
6. Activate the converted video as the loading video.

## Technical Summary

```text
OBS/Streamlabs
↓ local RTMP
MediaMTX
↓ encoded packets
DelayEngine
↓ disk buffer up to 1h
Delay/manual/loading
↓ local output
Local encoder in polished Twitch mode
↓ RTMP
Twitch
```

The project's main difference is controlling delay during a live stream with a visual transition, while keeping the central logic packet-based and running on the user's own PC.
