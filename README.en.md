# DelayEngine — English Guide

[← Home](README.md) | [Português (Brasil)](README.pt-BR.md)

DelayEngine is a Windows application for applying and removing manual live-stream delay without restarting OBS, Streamlabs, or the Twitch broadcast.

## How it works

```text
OBS / Streamlabs → local MediaMTX → DelayEngine → local relay → Twitch
```

In the default **Copy** mode, the output preserves the H.264/AAC stream received from OBS. The live path is not re-encoded, reducing system load and helping keep latency low.

## Quick start

1. Open `DelayEngine.exe`.
2. In OBS/Streamlabs, use the local server and stream key shown in the panel.
3. Open `http://127.0.0.1:8080` and save your Twitch stream key.
4. Wait until the panel reports both input and output as connected.
5. Use **Add delay with loading** to arm manual delay and **Go live** to return to realtime.

Set the OBS/Streamlabs keyframe interval to **2 seconds** for predictable transitions.

## Twitch output

- **Copy (recommended):** actual bitrate, codec, and encoder come from OBS/Streamlabs. Change bitrate in OBS.
- **Encoded (optional):** DelayEngine re-encodes the output and can use AMD, NVIDIA, Intel, or CPU. Use it when the app must control resolution, FPS, or bitrate.

For Twitch, **6000 kbps is the most stable reference**. Higher profiles require very consistent delivery and may cause buffering for some viewers.

## Manual delay

**Loading mode** is recommended. It keeps the delayed frame visible until a safe transition to the delayed live stream is ready.

**Return through buffer** is experimental. It may alter the timeline already seen by Twitch and cause reloads or buffering in some players; use it only for testing.

## Source and portable package

- `DelayEngine-Codigo`: source code and development files.
- `DelayEngine-Portatil`: ready-to-run application. Local data, videos, logs, and settings stay in this folder.

See the [full documentation](DOCUMENTACAO_COMPLETA.md) for operation, privacy, hotkeys, and troubleshooting details.
