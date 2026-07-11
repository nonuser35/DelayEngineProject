# DelayEngine — guia do pacote portátil

[← Início](README.md) | [English](README.en.md)

## Primeiro uso

1. Abra `DelayEngine.exe`.
2. No OBS/Streamlabs, use o servidor e a chave local exibidos no painel.
3. Abra `http://127.0.0.1:8080` e salve a chave da Twitch.
4. Espere o painel indicar que entrada e saída estão conectadas.

Use **Adicionar delay com loading** para aplicar delay e **Voltar ao vivo** para retornar ao tempo real. Configure keyframe de 2 segundos no OBS/Streamlabs.

## Importante

- **Copy** é o modo recomendado: bitrate e encoder reais vêm do OBS/Streamlabs.
- Use 6000 kbps como referência estável para Twitch.
- **Voltar no buffer** é experimental; o modo com loading é mais previsível.
- Fechar o navegador não encerra a live. Para sair do aplicativo, use o ícone perto do relógio do Windows.

## Seus dados

Esta pasta pode conter suas configurações, vídeos, logs e dados de runtime. Eles foram preservados nesta atualização. Antes de compartilhar uma cópia com outra pessoa, execute `limpeza-de-dados.cmd` na cópia que será enviada.
