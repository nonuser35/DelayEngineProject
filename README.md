# DelayEngine

DelayEngine is an open source live-stream delay tool for Windows.

It receives a stream published to local MediaMTX, keeps encoded RTMP packets in a buffer, and republishes the live with a configurable delay. The core delay path preserves the encoded packets: it does not decode, recode, or modify the live audio/video while delay is applied.

The desktop app also includes a web panel, tray mode, loading-video management, Twitch workflow helpers, and an optional polished Twitch relay. The optional converter/relay can use bundled FFmpeg/ffprobe, but the delay engine itself still keeps the live packets intact.

## Main Features

- Local OBS/Streamlabs input through MediaMTX.
- RTMP packet buffering with delay control.
- Add or remove delay during the live.
- Loading video transition while delay is applied.
- Web UI for Twitch key, OBS instructions, logs, converter, and stream health.
- Automatic loading-video conversion profile based on the detected live format.
- Portable Windows app folder with `DelayEngine.exe`.
- Data-cleaning script for sharing a clean app folder.

## Documentation / Documentação

Bilingual documentation, Portuguese and English:

- `DOCUMENTACAO_COMPLETA.md`: detailed explanation of the app, buffer, delay, Twitch polished mode, converter, privacy, and usage flow.
- `GITHUB_DESCRICAO.md`: complete GitHub project page text.

## Core Rules

- The delay pipeline must not alter video payloads.
- The delay pipeline must not alter audio payloads.
- The delay pipeline must not recode live packets.
- MediaMTX compatibility must be preserved.
- Every change should keep `go test ./...` passing.

## License

DelayEngine is released under the MIT License. See `LICENSE`.

Third-party tools and libraries keep their own licenses. See `THIRD_PARTY_NOTICES.md`.

## Library choice

RTMP is implemented with github.com/bluenviron/gortmplib v0.4.0.

Why this library:

- It is maintained by the same author/ecosystem as MediaMTX.
- MediaMTX itself currently depends on gortmplib for RTMP support, which keeps compatibility risk low.
- It exposes encoded media callbacks for H264, H265, AAC/MPEG-4 Audio and other RTMP codecs without requiring audio/video decoding.
- Its message model exposes RTMP timestamps, H264/H265 PTS/DTS, AAC PTS, and video keyframe information in the underlying message package.

DelayEngine stores encoded audio/video data in a local disk safety buffer for up to about 1 hour. Buffering preserves encoded payload bytes, PTS/DTS, codec name, packet type, receive time, and detected H264/H265 keyframe flags.

## Configuration

Environment variables:

- DELAYENGINE_INPUT_URL: RTMP input URL. Default: rtmp://127.0.0.1/live/source
- DELAYENGINE_OUTPUT_URL: RTMP output URL. Default: rtmp://127.0.0.1:1935/live/delayed
- DELAYENGINE_HTTP_ADDR: future HTTP API address. Default: :8080
- DELAYENGINE_READ_TIMEOUT: RTMP read timeout, for example 10s. Default: 10s
- DELAYENGINE_WRITE_TIMEOUT: RTMP write timeout, for example 10s. Default: 10s
- DELAYENGINE_FIXED_DELAY: fixed output delay, for example 5s. Default: 5s
- DELAYENGINE_DELAY_ENABLED: start with delay enabled. Default: false

## Run

```sh
go run ./cmd/delayengine
```

With a MediaMTX stream published to live/teste:

```powershell
$env:DELAYENGINE_INPUT_URL="rtmp://127.0.0.1:1935/live/teste"
$env:DELAYENGINE_OUTPUT_URL="rtmp://127.0.0.1:1935/live/delayed"
go run ./cmd/delayengine
```

Expected status includes audio/video confirmation and buffer metrics:

```text
RTMP pipeline status status=ok input=ok buffer=ok output=ok delay_enabled=false delay=5s queued_for_delay=... buffer_duration=... published_audio=... published_video=...
```

## HTTP API

The API starts on DELAYENGINE_HTTP_ADDR, default :8080.

```powershell
Invoke-RestMethod http://127.0.0.1:8080/status
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/on
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/off
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body '{"delay":"5s"}'
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body '{"seconds":30}'
```

Endpoints:

- GET /status
- POST /delay/on
- POST /delay/off
- POST /delay/set

## Dynamic delay test

Run DelayEngine, then use another PowerShell:

```powershell
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/off
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body '{"delay":"30s"}'
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/on
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body '{"delay":"60s"}'
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body '{"delay":"15s"}'
Invoke-RestMethod http://127.0.0.1:8080/status
```

The output keeps running while target_delay changes. effective_delay shows the actual delay currently being applied by the publisher. sync can be stable, increasing, or catching_up.

## Twitch helper

Para testar publicacao na Twitch com um assistente simples no PowerShell:

```powershell
cd C:\Users\Usuario\Documents\Codex\2026-06-30\projeto-nome-delayengine-objetivo-receber-um\DelayEngine
.\scripts\start-twitch.ps1
```

O script pergunta a stream key sem mostrar na tela, configura a entrada RTMP local e publica em rtmp://live.twitch.tv:1935/app. Nao cole sua stream key em chats, prints ou logs.

## Slate delay arm

Experimental endpoint to arm delay using a pre-encoded FLV loading video:

```powershell
Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/arm -ContentType "application/json" -Body '{"delay":"30s","slate":"C:/caminho/loading.flv"}'
```

The slate file must be FLV with H264 video and AAC audio. DelayEngine reads encoded FLV tags and republishes them as RTMP messages without decoding the live stream. A 30-second slate is recommended for a 30-second delay.
