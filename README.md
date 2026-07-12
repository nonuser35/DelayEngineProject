# DelayEngine

[Português (Brasil)](README.pt-BR.md) | [English](README.en.md) | [Documentação completa](DOCUMENTACAO_COMPLETA.md)

DelayEngine é um aplicativo gratuito e de código aberto para Windows que adiciona e remove delay manual em transmissões ao vivo sem interromper o OBS ou o Streamlabs. O projeto foi criado para ajudar streamers, moderadores e equipes da comunidade que precisam controlar o tempo da live com uma operação simples, local e transparente.

Distribuído sob a [licença MIT](LICENSE), o DelayEngine pode ser usado, estudado, adaptado e melhorado por qualquer pessoa.

## Download para Windows

Baixe o aplicativo completo e pronto para uso na página de [Releases](https://github.com/nonuser35/DelayEngineProject/releases/latest). O arquivo portátil inclui o DelayEngine e as ferramentas necessárias; os binários grandes não ficam armazenados no histórico do código-fonte.

## Como funciona

```text
OBS / Streamlabs → MediaMTX local → DelayEngine → relay de saída → Twitch
```

O aplicativo mantém até dois minutos de mídia recente em um buffer local. Esse buffer não adiciona atraso enquanto a live está no modo ao vivo: ele funciona como uma reserva para preparar o trecho atrasado e realizar as transições.

## Três modos de inserir delay

| Modo | Saída | Comportamento |
| --- | --- | --- |
| **Loading curto** | Encoded | Exibe uma imagem ou vídeo uma vez, respeitando exatamente a duração convertida entre 1 e 5 segundos, e entra no trecho atrasado já preparado. |
| **Loading completo** | Encoded | Mantém a mídia de loading enquanto o delay é preparado. Se necessário, repete a mídia até o trecho atrasado ficar pronto. |
| **Corte pelo buffer** | Copy | Não mostra mídia de loading. Mantém o último quadro disponível durante a preparação e troca diretamente em um keyframe do próprio sinal do OBS/Streamlabs. |

No retorno ao vivo, o DelayEngine prepara um GOP decodificável, remove a fila atrasada e reancora o relógio de publicação. O objetivo é entregar o primeiro quadro atual por cima do quadro anterior, sem tela verde e sem reiniciar a entrada do OBS.

## Vídeo e imagem

O conversor integrado aceita vídeos e imagens. Imagens são transformadas em H.264 com quadro estático, FPS configurado e áudio AAC silencioso.

- Arquivos de imagem: `loading_imagem_...flv`
- Arquivos de vídeo: `loading_video_...flv`
- Loading curto: respeita exatamente 1 a 5 segundos.
- Loading completo: a duração define cada ciclo e a mídia pode repetir até o delay ficar pronto.

## Destaques

- Delay manual de até 60 segundos.
- Retorno ao vivo sem reiniciar OBS/Streamlabs.
- Modos Copy e Encoded selecionados automaticamente conforme a transição.
- Encoded com AMD AMF, NVIDIA NVENC, Intel Quick Sync ou CPU.
- Controle pelo painel, atalhos globais, bandeja do Windows e controle remoto para OBS, Streamlabs ou Stream Deck.
- Conversor de vídeo e imagem, preview, notificações de estado e logs locais.
- Stream key protegida localmente no Windows.

## Início rápido

1. Abra `DelayEngine.exe`.
   No primeiro uso, o app inicia em Twitch + Corte pelo buffer + Copy. O painel avisa sobre a stream key, a configuração local do OBS/Streamlabs e uma mídia opcional para deixar os modos com loading preparados.
2. Copie o servidor e a chave local exibidos no painel para o OBS/Streamlabs.
3. Salve sua stream key da Twitch no DelayEngine.
4. Configure o OBS/Streamlabs com H.264, AAC e keyframe de **2 segundos**.
5. Aguarde entrada, saída e buffer aparecerem como prontos.
6. Escolha um dos três modos e aplique o delay.
7. Use **Voltar ao vivo** para remover o delay manual.

Para Twitch, 6000 kbps é a referência operacional mais conservadora. O resultado final também depende do upload, da rota até a ingestão da Twitch, do hardware e do buffer HLS de cada espectador.

## Arquivos do projeto

- `Codigo/`: código-fonte Go, interface web, testes e scripts.
- `support/`: conteúdo remoto editável do painel.
- Aplicativo pronto para Windows: disponível em [Releases](https://github.com/nonuser35/DelayEngineProject/releases/latest).

Leia o [guia em português](README.pt-BR.md), a [versão em inglês](README.en.md) e a [documentação técnica](DOCUMENTACAO_COMPLETA.md). Contribuições são bem-vindas em [CONTRIBUTING.md](CONTRIBUTING.md).

Consulte também [SECURITY.md](SECURITY.md), [NOTICE](NOTICE) e [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
