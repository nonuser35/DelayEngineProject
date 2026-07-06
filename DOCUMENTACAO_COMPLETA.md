# DelayEngine - Documentação completa

> Idiomas: Português / English

---

## Português

### O que é o DelayEngine

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

A proposta é permitir que o host adicione ou remova delay durante a transmissão, usando um vídeo de loading como transição, sem reiniciar a live.

### O que roda localmente

O DelayEngine roda inteiramente no computador da pessoa. Ele não depende de servidor externo para receber a live, guardar buffer, aplicar delay, enviar para a Twitch, converter vídeos, mostrar o painel web, guardar configurações ou proteger a stream key.

A única busca externa opcional do painel é para atualizar cards públicos vindos do GitHub, como perfil de apoio, FAQ e tutorial. Se não houver internet, o app usa o cache/local ou simplesmente não mostra aquele bloco. Isso não interfere na transmissão.

### Componentes principais

#### MediaMTX local

O MediaMTX recebe a live enviada pelo OBS ou Streamlabs em RTMP. Ele roda junto com o app.

No OBS/Streamlabs, o usuário usa dados locais parecidos com:

```text
Servidor: rtmp://127.0.0.1:1935
Chave: live/delayengine
```

O servidor e a chave devem ficar separados. Não cole `servidor/chave` no campo de servidor.

#### DelayEngine

O DelayEngine lê os pacotes RTMP codificados, guarda em buffer e republica a saída. Na lógica principal do delay, ele não decodifica nem recodifica áudio/vídeo. Ele preserva:

- áudio;
- vídeo;
- PTS/DTS;
- keyframes;
- sincronização de áudio e vídeo;
- mensagens RTMP necessárias para publicação.

#### Modo Twitch polido

O modo Twitch polido é o modo recomendado para uso real na Twitch.

Nesse modo, o DelayEngine publica primeiro em uma saída RTMP local e um codificador local mantém a transmissão contínua para a Twitch:

```text
DelayEngine
↓
saída RTMP local
↓
codificador local
↓
Twitch
```

Esse modo usa FFmpeg/ffprobe embutidos na pasta do app para a saída polida e para conversão de vídeos. A lógica principal do delay continua trabalhando com pacotes codificados.

O perfil do modo Twitch polido deve ser configurado manualmente com os mesmos valores da live no OBS/Streamlabs:

- largura;
- altura;
- FPS;
- bitrate de vídeo;
- bitrate de áudio;
- aceleração em hardware, quando disponível.

Se resolução/FPS do perfil não baterem com a live, o FFmpeg pode usar escala/padronização de FPS. Isso é correto para padronizar a saída para Twitch, mas pode pesar mais em PC fraco. Melhor cenário: perfil manual igual ao OBS/Streamlabs.

### Delay manual e loading

O botão **Adicionar delay com loading** arma o delay escolhido e toca o vídeo de loading enquanto o buffer fica pronto.

Quando o buffer atinge o atraso pedido, a live volta com o delay manual configurado. O botão fica bloqueado enquanto já existe delay ativo, para evitar adicionar delay em cima de delay.

O botão **Voltar ao vivo** remove o delay manual e tenta voltar para a menor latência possível sem reiniciar a live na Twitch.

O delay natural da Twitch normalmente fica na faixa de 2 a 5 segundos, dependendo de player, região, rede e configurações da Twitch. O DelayEngine usa compensações internas para aproximar o delay final do valor escolhido pelo usuário.

### Vídeo de loading completo

Existe uma opção para reproduzir o loading completo.

- Desligado: o app volta para a live assim que o buffer do delay fica pronto.
- Ligado: o app toca ciclos completos do loading antes de voltar.

Se o vídeo for menor que o delay pedido, ele pode repetir até cobrir o tempo necessário. Se o delay for 0 e essa opção estiver ligada, o loading pode ser usado como aviso/transição e depois a live volta ao vivo.

### Buffer de segurança de 1 hora

O buffer de 1 hora é um buffer de segurança em disco. Ele não significa que o usuário deve usar 1 hora de delay manual.

O app grava pacotes em segmentos locais, preservando:

- tipo do pacote: áudio ou vídeo;
- codec;
- PTS;
- DTS;
- keyframe;
- payload codificado;
- horário de recebimento.

O objetivo desse buffer é dar segurança para o fluxo e permitir recuperação/controle interno durante a live. O delay manual da interface continua limitado para uso prático, normalmente até 60 segundos.

### Conversor de vídeos

O conversor prepara vídeos de loading em FLV H.264/AAC compatível com o fluxo.

Ele usa o mesmo perfil configurado no modo Twitch polido: resolução, FPS, áudio AAC 48 kHz e bitrate. Assim o usuário não precisa preencher duas áreas diferentes com os mesmos dados.

Regras:

- se o vídeo original for curto, o conversor repete até completar a duração escolhida;
- se for longo, ele corta;
- o resultado vai para `videos/ready`;
- o vídeo ativo da live fica em `videos/live/loading.flv`.

O app inclui FFmpeg/ffprobe na pasta `tools/ffmpeg/bin`, para funcionar de forma portátil.

### Painel web

O painel web roda localmente em:

```text
http://127.0.0.1:8080
```

Ele permite:

- salvar a key da Twitch com proteção local;
- copiar os dados locais para OBS/Streamlabs;
- configurar modo Twitch polido;
- escolher delay manual;
- adicionar delay com loading;
- voltar ao vivo;
- converter vídeos de loading;
- ver logs;
- ver status de entrada, saída, buffer e Twitch;
- abrir FAQ e tutorial;
- controlar perfil/apoio carregado do GitHub.

### Preview local

O preview local usa o HLS local do MediaMTX. Ele serve para confirmar que a live está chegando ao DelayEngine. Ele não representa exatamente a latência que o público vê na Twitch e não inclui necessariamente o mesmo caminho do loading final.

Por segurança de navegador, autoplay pode depender de interação do usuário. O app tenta iniciar o preview, mas o navegador pode exigir clique.

### Tray do Windows

O DelayEngine possui modo tray. O ícone fica perto do relógio do Windows.

No tray é possível:

- abrir o painel;
- ver se o delay está ativo;
- adicionar delay com loading;
- voltar ao vivo;
- abrir a pasta de logs;
- ligar/desligar **Iniciar com Windows**;
- sair do app.

A opção **Iniciar com Windows** registra o app no iniciar do Windows do usuário atual. Não precisa de administrador.

### Controle remoto / dock

O app possui uma página remota leve em:

```text
http://127.0.0.1:8080/remote
```

Ela foi pensada para usar como dock/fonte de navegador em OBS/Streamlabs/StreamElements. A página mostra o status e oferece botões rápidos para adicionar delay ou voltar ao vivo.

### FAQ, tutorial e card de apoio via GitHub

O painel pode buscar conteúdo público no GitHub:

- `support/profile.json`: perfil do criador, links, QR Codes e apoio;
- `support/tutorial.json`: tutorial completo;
- `support/faq.json`: perguntas frequentes.

Esses arquivos podem conter imagens por URL pública. O app tenta baixar uma versão nova quando a página carrega e mantém cache local quando possível. Campos vazios não devem aparecer no painel.

### Privacidade e dados locais

Dados do usuário ficam no computador:

- stream key da Twitch;
- configurações;
- vídeos convertidos;
- logs;
- cache local;
- nome local da stream.

Antes de enviar a pasta para outra pessoa, use:

```text
limpeza-de-dados.cmd
```

Esse script remove dados pessoais e deixa a pasta limpa para distribuição.

### Pasta portátil

A pasta final gerada pela build fica em:

```text
dist/DelayEngineApp
```

Arquivos principais:

```text
DelayEngine.exe
DelayEngine.lnk
web/
tools/
assets/
videos/
scripts/
limpeza-de-dados.cmd
```

O usuário final deve conseguir extrair a pasta e abrir o `DelayEngine.exe`.

### Biblioteca RTMP

O RTMP usa `github.com/bluenviron/gortmplib`.

Motivos:

- é mantida no ecossistema do MediaMTX;
- preserva compatibilidade com MediaMTX;
- expõe pacotes codificados sem forçar decodificação;
- permite acessar dados necessários como PTS/DTS, timestamps e keyframes.

---

## English

### What DelayEngine Is

DelayEngine is a local Windows app for controlling delay during a live stream. It sits between OBS/Streamlabs and Twitch.

Main flow:

```text
OBS / Streamlabs
↓
local MediaMTX
↓
DelayEngine
↓
Twitch
```

Its goal is to let the host add or remove delay while the stream is already live, using a loading video as a transition, without restarting the stream.

### What Runs Locally

DelayEngine runs entirely on the user's computer. It does not need an external server to receive the stream, buffer packets, apply delay, send to Twitch, convert videos, show the web panel, store settings, or protect the stream key.

The only optional external fetch is for public GitHub content such as support/profile cards, FAQ, and tutorial. If the internet is unavailable, the stream is not affected.

### Main Components

#### Local MediaMTX

MediaMTX receives the RTMP stream from OBS or Streamlabs and runs together with the app.

OBS/Streamlabs should use local values like:

```text
Server: rtmp://127.0.0.1:1935
Key: live/delayengine
```

Server and key must stay separated. Do not paste `server/key` into the server field.

#### DelayEngine

DelayEngine reads encoded RTMP packets, buffers them, and republishes the output. In the core delay path, it does not decode or recode audio/video. It preserves:

- audio;
- video;
- PTS/DTS;
- keyframes;
- audio/video sync;
- RTMP messages required for publishing.

#### Polished Twitch Mode

Polished Twitch mode is the recommended mode for real Twitch usage.

In this mode, DelayEngine publishes to a local RTMP output first, and a local encoder keeps a continuous stream to Twitch:

```text
DelayEngine
↓
local RTMP output
↓
local encoder
↓
Twitch
```

This mode uses bundled FFmpeg/ffprobe for the polished output and video conversion. The core delay logic still works with encoded packets.

The polished Twitch profile should be configured manually with the same values used in OBS/Streamlabs:

- width;
- height;
- FPS;
- video bitrate;
- audio bitrate;
- hardware acceleration, when available.

If the profile resolution/FPS does not match the live stream, FFmpeg may scale/pad and normalize FPS. This is valid for Twitch output, but can be heavier on weak PCs. Best case: manual profile matching OBS/Streamlabs.

### Manual Delay and Loading

The **Add delay with loading** button arms the selected delay and plays the loading video while the buffer becomes ready.

When the buffer reaches the requested delay, the live stream returns with that manual delay applied. The button is locked while delay is active to avoid stacking delay over delay.

The **Return live** button removes manual delay and tries to return to the lowest possible latency without restarting Twitch.

Twitch's natural delay is usually around 2 to 5 seconds, depending on player, region, network, and Twitch settings. DelayEngine uses internal compensation to keep the final perceived delay close to the user's chosen value.

### Full Loading Video

There is an option to play the full loading video.

- Off: the app returns to live as soon as the delay buffer is ready.
- On: the app plays full loading cycles before returning.

If the video is shorter than the requested delay, it can repeat until enough time has passed. If delay is 0 and this option is enabled, the loading can be used as an announcement/transition and then return to live.

### 1-Hour Safety Buffer

The 1-hour buffer is a disk safety buffer. It does not mean the user should use 1 hour of manual delay.

The app stores packet segments locally while preserving:

- packet type: audio or video;
- codec;
- PTS;
- DTS;
- keyframe flag;
- encoded payload;
- receive time.

This exists for safety and internal control during the stream. The UI manual delay remains limited for practical use, usually up to 60 seconds.

### Video Converter

The converter prepares loading videos as FLV H.264/AAC compatible with the stream flow.

It uses the same profile configured in polished Twitch mode: resolution, FPS, AAC 48 kHz audio, and bitrate. This avoids filling the same data twice.

Rules:

- if the source video is short, the converter repeats it until the selected duration is reached;
- if it is long, it cuts it;
- output files go to `videos/ready`;
- the active loading video is stored at `videos/live/loading.flv`.

The app includes FFmpeg/ffprobe under `tools/ffmpeg/bin`, so it can work as a portable folder.

### Web Panel

The web panel runs locally at:

```text
http://127.0.0.1:8080
```

It lets the user:

- save the Twitch key locally;
- copy local OBS/Streamlabs values;
- configure polished Twitch mode;
- choose manual delay;
- add delay with loading;
- return live;
- convert loading videos;
- view logs;
- view input, output, buffer, and Twitch status;
- open FAQ and tutorial;
- load public GitHub profile/support content.

### Local Preview

The local preview uses MediaMTX local HLS. It confirms that the stream is reaching DelayEngine. It does not exactly represent Twitch viewer latency and may not include the same final loading path.

Because of browser rules, autoplay may require user interaction.

### Windows Tray

DelayEngine includes a tray mode. The icon stays near the Windows clock.

From the tray, the user can:

- open the panel;
- see if delay is active;
- add delay with loading;
- return live;
- open the logs folder;
- enable/disable **Start with Windows**;
- quit the app.

The **Start with Windows** option registers the app for the current Windows user and does not require administrator rights.

### Remote Control / Dock

The app includes a lightweight remote page:

```text
http://127.0.0.1:8080/remote
```

It is meant to be used as a dock/browser source in OBS/Streamlabs/StreamElements, with quick buttons to add delay or return live.

### FAQ, Tutorial, and Support Card Through GitHub

The panel can load public GitHub content:

- `support/profile.json`: creator profile, links, QR Codes, support cards;
- `support/tutorial.json`: full tutorial;
- `support/faq.json`: FAQ.

These files can include public image URLs. The app fetches updates when the page loads and keeps local cache when possible. Empty fields should not appear in the panel.

### Privacy and Local Data

User data stays on the computer:

- Twitch stream key;
- settings;
- converted videos;
- logs;
- local cache;
- local stream name.

Before sending the folder to another person, run:

```text
limpeza-de-dados.cmd
```

This clears personal data and prepares a clean portable folder.

### Portable Folder

The final build folder is:

```text
dist/DelayEngineApp
```

Main files:

```text
DelayEngine.exe
DelayEngine.lnk
web/
tools/
assets/
videos/
scripts/
limpeza-de-dados.cmd
```

The final user should be able to extract the folder and run `DelayEngine.exe`.

### RTMP Library

RTMP uses `github.com/bluenviron/gortmplib`.

Reasons:

- it belongs to the MediaMTX ecosystem;
- it helps preserve MediaMTX compatibility;
- it exposes encoded packets without forcing decoding;
- it provides access to required timing/keyframe data such as PTS/DTS, timestamps, and keyframes.
