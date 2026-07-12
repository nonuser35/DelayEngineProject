# DelayEngine — documentação completa

[Início](README.md) | [Guia PT-BR](README.pt-BR.md) | [English guide](README.en.md)

## 1. Objetivo

DelayEngine é um relay local para Windows, gratuito e aberto à comunidade. Ele recebe a live do OBS/Streamlabs, mantém mídia recente em um buffer de segurança e envia a saída para a Twitch. O objetivo é controlar delay manual sem encerrar a entrada local nem reiniciar o OBS.

## 2. Arquitetura

```text
OBS / Streamlabs
        ↓ RTMP H.264/AAC
MediaMTX local
        ↓
DelayEngine + buffer em disco
        ↓
saída local contínua
        ↓
relay Copy ou Encoded
        ↓
Twitch → HLS → espectador
```

O buffer operacional mantém aproximadamente dois minutos de mídia recente. Quando o delay manual está desligado, a saída é pareada com a entrada e o buffer não se transforma em atraso.

## 3. Modos de saída

### 3.1 Encoded

Recodifica a saída para manter resolução, FPS, GOP e bitrate uniformes durante trocas de mídia. Pode usar AMD AMF, NVIDIA NVENC, Intel Quick Sync ou CPU. É selecionado automaticamente pelos modos Loading curto e Loading completo.

O relay limita a recuperação após uma interrupção a 1,05x. Em um perfil de 6000 kbps, isso oferece aproximadamente 300 kbps de folga temporária, evitando recuperação ilimitada e picos grandes de bitrate.

### 3.2 Copy

Preserva o H.264/AAC recebido do OBS/Streamlabs. Encoder, bitrate, perfil e qualidade são definidos no OBS. É selecionado automaticamente pelo Corte pelo buffer.

Esta é a configuração inicial de uma instalação nova: modo Twitch, Corte pelo buffer e saída Copy. As notificações orientam o preenchimento da stream key, os dados locais do OBS/Streamlabs e a preparação opcional de uma imagem ou vídeo para os modos com loading.

Copy reduz processamento, mas a Twitch pode recompor o HLS com uma margem de buffer diferente da observada no Encoded. Essa diferença pertence à entrega e ao player, mesmo quando o DelayEngine já mostra `0s` e `pareado`.

### 3.3 Auditoria de qualidade

No Copy, uma amostra real foi remultiplexada e os fluxos elementares H.264 e AAC antes/depois produziram hashes SHA-256 idênticos. Portanto, o relay não altera a qualidade recebida do OBS/Streamlabs.

No Encoded, existe uma geração H.264/AAC necessária para unificar live e mídia de transição. Em uma amostra real 1920×1080/60 a 6000 kbps, usando as configurações AMD AMF do app, foram medidos VMAF 97,15 e PSNR médio 58,49 dB. O perfil preservou resolução, FPS, `yuv420p`, H.264 High, GOP de dois segundos e bitrate de aproximadamente 6000 kbps. O resultado representa excelente fidelidade para a amostra testada; outros hardwares devem manter perfil e bitrate equivalentes e usar os presets protegidos do app.

## 4. Três formas de inserir delay

### 4.1 Loading curto

1. O app confirma que o buffer solicitado já existe.
2. A mídia convertida é exibida uma única vez.
3. O cursor atrasado é preparado com alguns segundos de mídia à frente.
4. O trecho atrasado entra no final da mídia.

A duração convertida é obedecida exatamente entre 1 e 5 segundos. Serve para imagem ou vídeo.

### 4.2 Loading completo

1. O app segura o sinal atual para a transição.
2. A mídia de loading permanece visível durante a preparação.
3. Se a mídia terminar antes do trecho atrasado ficar pronto, ela é repetida.
4. O cursor atrasado entra somente quando há mídia suficiente para continuidade.

A duração convertida define cada ciclo. O tempo total visível acompanha a preparação do delay e pode ser maior.

### 4.3 Corte pelo buffer

1. O app mantém o último quadro disponível no player.
2. Prepara um intervalo iniciado em keyframe no buffer.
3. Remapeia o trecho atrasado para uma linha de tempo contínua.
4. Faz o corte sem inserir imagem ou vídeo.

O corte preserva o bitstream do OBS. Em testes reais, a troca não exibiu spinner, mas o player HLS da Twitch manteve uma margem de latência maior que nos modos Encoded. Essa é uma diferença importante ao escolher entre menor processamento e menor latência final.

## 5. Retorno ao vivo

Ao receber Voltar ao vivo, o motor:

1. Prepara o GOP decodificável mais recente.
2. Descarta a fila manualmente atrasada.
3. Publica o GOP de preparação.
4. Reancora o relógio no próximo pacote atual.
5. Continua em 1,00x com delay manual `0s`.

O quadro anterior pode permanecer visível durante a preparação. Essa retenção visual é preferível a enviar P-frames sem referência, o que poderia produzir tela verde ou rebuffer.

## 6. Conversão de imagem e vídeo

O conversor aceita vídeo e imagem. O resultado usa:

- H.264 High, `yuv420p`;
- FPS constante;
- GOP de dois segundos e sem B-frames;
- AAC estéreo em 48 kHz;
- resolução e bitrate escolhidos no perfil.

Imagens ficam estáticas durante toda a duração convertida. Validações realizadas em 1920×1080/60 produziram:

- 4 segundos solicitados → 4,021 segundos;
- 5 segundos solicitados → 5,021 segundos;
- 10 segundos solicitados → 10,021 segundos.

A pequena diferença corresponde ao fechamento do último quadro/bloco de áudio.

O painel usa um único campo de duração, entre 1 e 600 segundos:

- de 1 a 5 segundos, classifica a mídia para Loading curto;
- de 6 a 600 segundos, classifica a mídia para Loading completo.

No Loading curto, a mídia é exibida uma vez. No Loading completo, a duração representa cada ciclo, que pode repetir até o trecho atrasado ficar pronto. Imagens JPG/JPEG, PNG, WebP, BMP e TIFF aceitam todo o intervalo e permanecem estáticas. Vídeos MP4, MOV, MKV, WebM, AVI e outros formatos compatíveis com FFmpeg são repetidos ou cortados até a duração informada.

Nomes gerados:

- imagem: `loading_imagem_<perfil>_<duração>_<data>.flv`;
- vídeo: `loading_video_<perfil>_<duração>_<data>.flv`.

## 7. Estados e notificações

O resumo do painel alterna, a cada quatro segundos, entre a mensagem do sistema e a mensagem remota do projeto.

Estados operacionais esperados:

- **Aguardando OBS/Streamlabs:** entrada ainda não conectada.
- **Preparando a saída:** entrada chegou e o relay final está conectando.
- **Aplicando alteração de delay:** transição em andamento.
- **Delay manual ativo:** trecho atrasado em execução.
- **Retornando ao vivo:** saída sendo reposicionada.
- **Envio para Twitch ativo:** live pareada e relay final em execução.

Os contadores verde, amarelo e vermelho representam apenas condições atuais. Mudanças normais de delay permanecem verdes; avisos são reservados para ações necessárias ou gargalos persistentes.

## 8. Latência

Há três camadas diferentes:

1. Delay manual do DelayEngine.
2. Fila local entre entrada, buffer e relay.
3. Latência da Twitch entre ingestão, HLS, CDN e player.

`delay=0s`, `pareado` e fila local baixa confirmam que o DelayEngine não mantém atraso manual. A estatística “Latência para o streamer” da Twitch inclui as demais camadas e pode mudar após F5, reconexão ou recomposição HLS.

Em validação real a 1080p60 e aproximadamente 6000 kbps:

- Loading curto/Encoded: sem loading visual; retorno perto de 1,0–1,2 s no player observado.
- Loading completo/Encoded, após relógio contínuo: sem loading visual; retorno perto de 1,0–1,3 s.
- Corte pelo buffer/Copy: sem loading visual; o player observado manteve aproximadamente 3,4–3,9 s após o retorno.

Esses números descrevem o ambiente de teste e não são garantia universal. Rota de internet, ingestão, CDN, navegador, hardware e configuração do OBS influenciam o resultado.

## 9. Recomendações

- Configure keyframe de 2 segundos no OBS/Streamlabs.
- Use H.264 e AAC.
- Mantenha resolução e FPS iguais em OBS, DelayEngine e loading.
- Use 6000 kbps como referência conservadora para Twitch.
- Prefira Loading curto ou completo quando a menor latência final for prioridade.
- Use Corte pelo buffer quando a prioridade for preservar o bitstream do OBS e evitar mídia de transição.
- Quando o modo Encoded precisar mudar a resolução, use Bicúbico como padrão equilibrado, Lanczos para maior nitidez ou Bilinear para reduzir processamento. Se entrada e saída já têm a mesma resolução, nenhum filtro de redimensionamento é aplicado.

## 10. Controles

- Painel: `http://127.0.0.1:8080`
- Controle remoto: `http://127.0.0.1:8080/remote`
- Adicionar delay: `Ctrl+Alt+D` por padrão
- Voltar ao vivo: `Ctrl+Alt+A` por padrão
- Bandeja do Windows, OBS/Streamlabs Browser Dock e Stream Deck por URL.

Os atalhos globais são editáveis no painel. Cada comando aceita uma tecla principal sozinha ou acompanhada por um ou dois modificadores. Exemplos: `F7`, `Ctrl+F7` e `Ctrl+Alt+D`. Letras, números, teclas F, setas e teclas de navegação são aceitos; `F12` permanece reservada pelo Windows. Ao salvar, o tray remove os registros anteriores e registra os novos em até aproximadamente um segundo, sem reiniciar o DelayEngine ou a transmissão.

## 11. Privacidade e distribuição

A chave da Twitch é protegida localmente pelo Windows. Não publique stream key, `settings.json`, logs pessoais, runtime ou mídias privadas. Para gerar uma cópia destinada a terceiros, execute `limpeza-de-dados.cmd`.

## 12. Desenvolvimento

```powershell
go test ./...
powershell -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1
```

O executável portátil é montado em `dist\DelayEngineApp\DelayEngine.exe`.
