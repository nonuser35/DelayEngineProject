# DelayEngine — guia em português

[← Página inicial](README.md) | [English](README.en.md) | [Documentação completa](DOCUMENTACAO_COMPLETA.md)

DelayEngine é um aplicativo gratuito e aberto para a comunidade. Ele permite adicionar e remover delay manual em uma live sem desconectar o OBS, o Streamlabs ou a entrada local da transmissão.

## Download

Baixe a versão portátil completa para Windows na página de [Releases](https://github.com/nonuser35/DelayEngineProject/releases/latest). O ZIP da versão inclui o aplicativo e as ferramentas necessárias.

## Fluxo

```text
OBS / Streamlabs → RTMP local → buffer do DelayEngine → saída local → Twitch
```

O buffer mantém mídia recente em disco por aproximadamente dois minutos. Quando o delay está desligado, a saída permanece pareada com a entrada; o buffer existe apenas como reserva operacional.

## Modos de transição

### 1. Loading curto

- Usa saída **Encoded**.
- Aceita vídeo ou imagem.
- A mídia é exibida uma única vez.
- Respeita exatamente a duração convertida entre 1 e 5 segundos.
- O trecho atrasado é preparado antes do final da mídia.

É a opção indicada quando a prioridade é uma transição curta, personalizada e com a menor latência final observada no modo Encoded.

### 2. Loading completo

- Usa saída **Encoded**.
- Aceita vídeo ou imagem.
- Mantém a mídia visível durante a preparação do delay.
- Se o arquivo for menor que o tempo necessário, ele pode repetir.
- A duração convertida define cada ciclo, não necessariamente o tempo total visível.

É a opção indicada quando a apresentação visual deve cobrir toda a preparação do trecho atrasado.

### 3. Corte pelo buffer

- Usa saída **Copy**.
- Não exibe vídeo ou imagem de loading.
- Preserva o H.264/AAC recebido do OBS/Streamlabs.
- Mantém o último quadro disponível durante a preparação e entra pelo próximo trecho decodificável do buffer.

Essa opção reduz a carga de codificação e faz um corte direto. Como a Twitch recompõe a linha HLS a partir do fluxo Copy, o player pode manter uma margem de buffer maior que nos modos Encoded. Se a menor latência final for a prioridade principal, compare os modos no computador e no canal usados na transmissão.

## Voltar ao vivo

O retorno prepara um GOP iniciado em keyframe, remove a fila atrasada e reancora o relógio da saída. O último quadro pode permanecer visível por um instante enquanto o primeiro quadro atual fica pronto. Essa proteção evita entregar um trecho incompleto ao player.

No modo Encoded, pequenas interrupções podem ser recuperadas de forma limitada a até 1,05x, evitando tanto atraso permanente quanto picos grandes de bitrate.

## Conversor de imagem e vídeo

O conversor gera FLV com H.264, FPS constante, keyframe de dois segundos e áudio AAC. Imagens recebem quadro estático e áudio silencioso.

| Entrada | Nome gerado | Duração |
| --- | --- | --- |
| Imagem | `loading_imagem_...flv` | Mantida pelo tempo escolhido. |
| Vídeo | `loading_video_...flv` | Repetido ou cortado até o tempo escolhido. |

O conversor usa uma única duração, de **1 a 600 segundos**, e identifica automaticamente a finalidade:

- **1 a 5 segundos:** mídia indicada para Loading curto, exibida uma vez nesse modo.
- **6 a 600 segundos:** mídia indicada para Loading completo; cada duração define um ciclo que pode repetir até o delay ficar pronto.

Imagens JPG/JPEG, PNG, WebP, BMP e TIFF podem usar qualquer duração desse intervalo e permanecem estáticas durante todo o tempo escolhido. Para vídeo, são aceitos MP4, MOV, MKV, WebM, AVI e outros formatos compatíveis com FFmpeg.

## Preservação de qualidade

- **Copy:** não recodifica. O áudio AAC e o vídeo H.264 enviados pelo OBS/Streamlabs são preservados pacote a pacote.
- **Encoded:** realiza uma geração H.264/AAC para manter uma linha contínua durante as trocas com mídia. O app mantém resolução, FPS, perfil High, keyframe e bitrate configurados e evita os presets de menor eficiência do NVENC e x264.

Em uma auditoria local com amostra real 1920×1080/60 a 6000 kbps, o Copy produziu fluxos elementares de áudio e vídeo com hashes idênticos aos da entrada. A recodificação AMD AMF configurada pelo app mediu VMAF 97,15 e PSNR médio 58,49 dB. Esses resultados indicam excelente fidelidade na amostra; a qualidade final continua condicionada ao bitrate, ao codificador disponível e à correspondência de resolução/FPS.

## Configuração recomendada

- H.264 e AAC no OBS/Streamlabs.
- Keyframe a cada 2 segundos.
- Mesma resolução e FPS no OBS, no perfil do DelayEngine e na mídia de loading.
- 6000 kbps como referência conservadora para Twitch.
- Aceleração compatível com o computador no modo Encoded: Auto, AMD, NVIDIA, Intel ou CPU.
- No modo Encoded, escolha Bicúbico para equilíbrio, Lanczos para maior nitidez ou Bilinear para menor uso de processamento. O filtro só atua quando a resolução do OBS é diferente da saída do DelayEngine.

## Formas de controle

- Painel local: `http://127.0.0.1:8080`
- Controle remoto: `http://127.0.0.1:8080/remote`
- Atalho para adicionar delay: `Ctrl+Alt+D` por padrão
- Atalho para voltar ao vivo: `Ctrl+Alt+A` por padrão
- Os dois atalhos podem ser alterados no painel para uma tecla, duas teclas ou três teclas. A alteração entra imediatamente após salvar, sem reiniciar o app.
- Ícone na bandeja do Windows
- Dock de navegador no OBS/Streamlabs ou ação de URL no Stream Deck

## Privacidade

A stream key da Twitch é protegida localmente pelo Windows. Não publique chaves, `settings.json`, logs pessoais, runtime ou mídias privadas. Antes de distribuir uma cópia, use `limpeza-de-dados.cmd`.

Para detalhes de estados, notificações, testes e solução de problemas, consulte a [documentação completa](DOCUMENTACAO_COMPLETA.md).
