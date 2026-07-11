# DelayEngine — documentação atual

## Visão geral

O DelayEngine é um aplicativo Windows para controlar delay manual em lives sem reiniciar o OBS nem a transmissão para a Twitch.

```text
OBS / Streamlabs → MediaMTX local → DelayEngine → relay local → Twitch
```

O caminho de delay trabalha com pacotes RTMP já codificados. O app preserva o vídeo e áudio da live; não recodifica o conteúdo durante a aplicação ou remoção do delay.

## Uso rápido

1. Abra `DelayEngine.exe`.
2. No OBS/Streamlabs, publique para `rtmp://127.0.0.1:1935/live` com a chave local mostrada no painel.
3. Salve a chave da Twitch no painel local: `http://127.0.0.1:8080`.
4. Aguarde o status mostrar transmissão conectada e saída **pareado**.

O app inicia MediaMTX e o relay da Twitch automaticamente. A chave da Twitch fica protegida localmente e não deve ser incluída em pacotes públicos.

## Modo de saída

O modo recomendado atualmente é **copy**. Ele envia o H.264/AAC recebido do OBS para a Twitch sem recodificação, mantendo baixo uso de CPU/GPU e bitrate próximo ao original.

O modo **encoded** continua disponível para casos em que seja necessário padronizar resolução, FPS ou codec. Ele pode usar AMD, NVIDIA, Intel ou software conforme o hardware disponível.

## Delay manual

### Adicionar delay com loading

Escolha o atraso entre 0 e 60 segundos e use **Adicionar delay com loading**.

- O vídeo ativo em `videos/live/loading.flv` é reproduzido enquanto o buffer é preparado.
- Quando o atraso solicitado fica pronto, a live continua com o delay manual configurado.
- Com **Loading completo** desligado, a live volta assim que o delay está pronto.
- Com **Loading completo** ligado, o app termina ciclos completos do vídeo antes de voltar.

Também existe **Aplicar delay sem loading**, indicado para operação técnica quando uma transição visual não é necessária.

### Voltar ao vivo

**Voltar ao vivo** descarta apenas a fila atrasada e usa o GOP atual iniciado em keyframe para retomar a live sem reiniciar a transmissão na Twitch.

O comportamento esperado é:

- o último quadro atrasado permanece até existir um trecho atual decodificável;
- a live nova entra por keyframe, sem tela verde;
- a proteção de fila não descarta esse GOP preparado;
- a latência volta ao mínimo possível sem reproduzir conteúdo antigo.

O retorno normalmente fica limitado pelo intervalo de keyframe do OBS. Para Twitch, configure keyframe de **2 segundos**.

## Latência e estabilidade

Em modo ao vivo, o objetivo é o relay ficar em **1.00x**:

- acima de 1.00x: está recuperando conteúdo de uma partida/reconexão;
- abaixo de 1.00x por muito tempo: pode indicar acúmulo;
- próximo de 1.00x: fluxo pareado.

O painel mostra:

- **pareado**: sem fila local relevante;
- **recuperando**: há fila curta sendo tratada;
- **ressincronizando**: o app está aguardando keyframe após uma recuperação.

Para evitar delay crescente, a fila de saída em tempo real é limitada. Se a publicação não acompanhar a entrada, o app descarta conteúdo antigo e retoma em keyframe em vez de deixar o público cada vez mais atrasado.

O buffer em disco de dois minutos serve para permitir delay manual e transições; ele não adiciona dois minutos de atraso quando o modo ao vivo está ativo.

## Entrada inicial na Twitch

Ao iniciar, o relay da Twitch espera um keyframe recente, limpa os timestamps da nova conexão e usa um GOP atual. Isso evita despejar uma sequência antiga na plataforma.

Ainda existe o tempo normal de abertura de sessão do FFmpeg/RTMP e da Twitch. Esse é tempo de conexão, não delay acumulado de vídeo. Após a conexão, a transmissão deve estabilizar em 1.00x.

Ao recarregar a página da Twitch, o player do espectador pode escolher alguns segundos de folga para formar seu próprio buffer. Isso é controlado pela Twitch/browser; o DelayEngine não reinicia nem adiciona delay manual por causa do F5.

## Vídeos de loading

Use vídeos FLV com H.264 e áudio AAC. O conversor integrado prepara vídeos compatíveis a partir da área **Ferramentas** e grava os resultados em `videos/ready`.

- Ative o vídeo escolhido antes de usar a transição.
- O vídeo ativo é copiado para `videos/live/loading.flv`.
- O app mantém áudio no loading para evitar troca brusca no player.

## Painel, tray e logs

O painel local permite:

- configurar a Twitch e o perfil de envio;
- consultar dados para OBS/Streamlabs;
- armar/remover delay;
- converter, ativar e apagar vídeos;
- acompanhar entrada, saída, keyframes, fila e logs.

O tray oferece os mesmos comandos essenciais e atalhos:

- `Ctrl+Alt+D`: adicionar delay com loading;
- `Ctrl+Alt+A`: voltar ao vivo.

O comando de armar delay pode aguardar a transição terminar; isso evita mensagens falsas de erro enquanto o loading e o buffer são preparados.

## Notificações

O painel mantém avisos de configuração na área de alertas, mas mostra toast somente quando é necessária ação imediata:

- espaço de disco crítico;
- relay abaixo do tempo real por período sustentado.

Estados comuns — chave ainda não salva, saída iniciando, keyframe fora do ideal, delay alto e configuração incompleta — ficam como aviso discreto, sem notificações repetitivas.

## Privacidade e distribuição

Dados locais incluem chave da Twitch, preferências, logs e vídeos preparados.

Antes de compartilhar uma pasta, execute `limpeza-de-dados.cmd`.

Estrutura recomendada:

- `DelayEngine-Codigo\`: fonte oficial;
- `DelayEngine-Codigo\dist\DelayEngineApp\`: portátil usada para testes;
- `DelayEngine-Portatil\`: pacote limpo para GitHub/distribuição;
- `_Backup-Codex\`: backups e snapshots antigos.

O pacote portátil público não deve conter stream key, logs, runtime, arquivos temporários ou caminhos locais.

## English summary

DelayEngine is a local Windows live-delay controller. It receives OBS/Streamlabs through local MediaMTX, buffers encoded RTMP packets, and relays the stream to Twitch.

The recommended output mode is **copy**, preserving the original H.264/AAC stream without re-encoding. Manual delay can be armed with a loading video and removed through a keyframe-safe return-to-live transition.

Live mode aims for **1.00x** relay speed. The app limits realtime queues and resynchronizes at a keyframe instead of allowing viewer latency to grow indefinitely. The portable public package must be cleaned of Twitch keys, logs, runtime files, and local paths.
