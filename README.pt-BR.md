# DelayEngine — Guia em Português

[← Início](README.md) | [English](README.en.md)

DelayEngine controla delay manual em lives do Windows sem exigir reiniciar OBS, Streamlabs ou a transmissão para Twitch.

## Como funciona

```text
OBS / Streamlabs → MediaMTX local → DelayEngine → relay local → Twitch
```

No modo padrão **Copy**, a saída preserva o vídeo e áudio H.264/AAC recebidos do OBS. O app não recodifica a live nesse caminho, o que reduz carga e ajuda a manter a menor latência possível.

## Uso rápido

1. Abra `DelayEngine.exe`.
2. No OBS/Streamlabs, use o servidor e a chave local exibidos no painel.
3. Abra `http://127.0.0.1:8080` e salve sua chave da Twitch.
4. Aguarde o painel mostrar entrada e saída conectadas.
5. Use **Adicionar delay com loading** para iniciar o delay e **Voltar ao vivo** para retornar ao tempo real.

Configure o intervalo de keyframe do OBS/Streamlabs em **2 segundos**. Isso deixa a entrada e o retorno ao vivo mais previsíveis.

## Saída para Twitch

- **Copy (recomendado):** o bitrate, codec e encoder reais vêm do OBS/Streamlabs. Ajuste o bitrate no próprio OBS.
- **Encoded (opcional):** o DelayEngine recodifica a saída e pode usar AMD, NVIDIA, Intel ou CPU. Use quando precisar controlar resolução, FPS ou bitrate no app.

Para Twitch, **6000 kbps é a referência mais estável**. Perfis acima disso exigem conexão e entrega muito consistentes e podem causar rebuffer em parte do público.

## Delay manual

O modo **Padrão com loading** é o recomendado. Ele mantém o último quadro da live até a transição estar pronta e entra no conteúdo atrasado de forma segura.

O modo **Voltar no buffer** é experimental. Ele pode alterar a sequência temporal já vista pela Twitch e provocar recarregamento ou rebuffer em alguns players; use apenas para testes.

## Portátil e código-fonte

- `DelayEngine-Codigo`: código-fonte e arquivos de desenvolvimento.
- `DelayEngine-Portatil`: aplicativo pronto para executar. Dados locais, vídeos, logs e configurações permanecem nessa pasta.

Para detalhes de operação, privacidade, atalhos e solução de problemas, leia a [documentação completa](DOCUMENTACAO_COMPLETA.md).
