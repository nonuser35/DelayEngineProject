# DelayEngine — documentação completa

[Início](README.md) | [Guia PT-BR](README.pt-BR.md) | [English guide](README.en.md)

## Objetivo

DelayEngine é um relay local para Windows. Ele recebe a transmissão do OBS/Streamlabs, mantém um buffer em disco e repassa a live para Twitch. O objetivo é permitir adicionar e remover delay manual sem parar a transmissão.

## Fluxo da transmissão

```text
OBS / Streamlabs → RTMP local → DelayEngine → RTMP local de saída → Twitch
```

O buffer em disco é uma reserva para transições e delay manual. Ele não adiciona dois minutos de atraso quando a live está em modo ao vivo.

## Modos de saída

### Copy

É o modo recomendado. O relay mantém o conteúdo H.264/AAC que chega do OBS/Streamlabs, sem recodificação. Codec, hardware e bitrate reais são definidos no OBS/Streamlabs.

### Encoded

É opcional. O app recodifica a saída para controlar resolução, FPS e bitrate. O seletor de hardware tenta usar AMD, NVIDIA ou Intel quando disponíveis; CPU continua como alternativa. Esse modo usa mais recursos e pode acrescentar complexidade, por isso Copy é o padrão.

## Bitrate e Twitch

Use 6000 kbps como referência estável. O painel pede confirmação ao escolher ou salvar perfis acima de 6000 kbps. Em Copy, o aviso é uma referência: o bitrate que chega à Twitch ainda é o que está no OBS/Streamlabs.

## Operação do delay

1. Confirme que a entrada OBS e a saída Twitch estão conectadas.
2. Escolha o atraso desejado, de 0 a 60 segundos.
3. Use **Adicionar delay com loading**.
4. Para encerrar, use **Voltar ao vivo**.

No retorno ao vivo, o app prepara um GOP iniciado em keyframe e descarta a fila atrasada. O último quadro atrasado pode ficar visível até a chegada de um trecho atual decodificável; isso evita tela verde e reduz a chance de rebuffer.

O intervalo de keyframe recomendado no OBS/Streamlabs é 2 segundos.

### Voltar no buffer

Este modo é experimental e não é recomendado para operação normal. Alguns players da Twitch podem interpretar a troca como uma sequência temporal inválida, recarregar ou entrar em rebuffer. Use o modo com loading para a transição mais previsível.

## Latência e saúde

Em modo ao vivo, o objetivo é relay próximo de `1.00x`.

- **pareado:** fila local sob controle.
- **recuperando:** há uma fila curta ou o relay está voltando ao tempo real.
- **ressincronizando:** o app está aguardando um keyframe após reconexão ou correção.

Se a saída não acompanhar a entrada, o app privilegia conteúdo atual e pode descartar pacotes antigos em vez de deixar a latência crescer continuamente.

O atraso mostrado pelo player da Twitch após um F5 é controlado pelo próprio player, navegador e CDN. Não representa necessariamente delay manual no DelayEngine.

## Vídeos de loading

Use o conversor do painel para criar um loading compatível. O vídeo ativo fica em `videos/live/loading.flv` e deve usar H.264 com áudio AAC. Para uma transição mais limpa, use resolução e FPS iguais aos da live.

## Atalhos e painel

- `Ctrl+Alt+D`: adicionar delay com loading.
- `Ctrl+Alt+A`: voltar ao vivo.
- Painel local: `http://127.0.0.1:8080`.

O painel exibe conexão, fila, FPS, bitrate, relay, logs e alertas relevantes.

## Privacidade e publicação

A chave da Twitch é armazenada localmente. Não publique stream key, `settings.json`, logs, dados de runtime ou vídeos pessoais.

O repositório ignora esses arquivos. Antes de distribuir uma cópia limpa, use `limpeza-de-dados.cmd`. A pasta portátil de uso pessoal pode manter seus próprios dados locais.

## Desenvolvimento

Requisitos: Go e ferramentas Windows usadas pelo projeto.

```powershell
go test ./...
powershell -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1
```

O executável de teste é gerado em `dist\DelayEngineApp\DelayEngine.exe`.
