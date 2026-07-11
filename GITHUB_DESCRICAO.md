# Texto para GitHub

## Descrição curta

DelayEngine é um relay local para Windows entre OBS/Streamlabs e Twitch, feito para adicionar e remover delay manual durante a live sem reiniciar a transmissão.

## Descrição completa

DelayEngine recebe a live por RTMP local, mantém um buffer seguro e envia a saída para Twitch. O modo Copy, recomendado, preserva o sinal H.264/AAC recebido do OBS/Streamlabs sem recodificar a live. O aplicativo inclui painel local, atalhos na bandeja do Windows, vídeos de loading, conversor integrado, logs e um modo Encoded opcional para situações em que seja necessário controlar a saída.

O fluxo é local:

```text
OBS / Streamlabs → MediaMTX local → DelayEngine → Twitch
```

Leia o [guia em Português](README.pt-BR.md) ou o [English guide](README.en.md).
