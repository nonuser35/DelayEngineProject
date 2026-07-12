# DelayEngine — English guide

[← Home](README.md) | [Português (Brasil)](README.pt-BR.md) | [Full documentation](DOCUMENTACAO_COMPLETA.md)

DelayEngine is a free and open-source Windows application built to help the streaming community apply and remove manual live-stream delay without disconnecting OBS, Streamlabs, or the local stream input.

## Download

Download the complete portable Windows build from [Releases](https://github.com/nonuser35/DelayEngineProject/releases/latest). The release ZIP includes the application and its required runtime tools.

## Stream path

```text
OBS / Streamlabs → local RTMP → DelayEngine buffer → local output → Twitch
```

The disk buffer keeps approximately two minutes of recent media. It does not add delay while manual delay is disabled; it is an operational reserve used to prepare transitions.

## Transition modes

### 1. Short loading

- Uses **Encoded** output.
- Accepts video or still images.
- Displays the converted media once.
- Follows the selected duration exactly, from 1 to 5 seconds.
- Prepares the delayed stream before the media ends.

### 2. Full loading

- Uses **Encoded** output.
- Accepts video or still images.
- Keeps the loading media visible while the requested delay is prepared.
- Repeats the media when necessary.
- The converted duration defines each cycle, not necessarily the total visible time.

### 3. Buffer cut

- Uses **Copy** output.
- Displays no loading media.
- Preserves the H.264/AAC signal received from OBS/Streamlabs.
- Keeps the last available frame visible while the next decodable buffer section is prepared.

This mode reduces encoding work and provides a direct cut. Because Twitch rebuilds its HLS timeline from the Copy stream, the viewer may keep more playback buffer than with Encoded modes. Compare the modes on the target computer and channel when minimum end-to-end latency is the main priority.

## Returning live

DelayEngine prepares a keyframe-based GOP, removes delayed packets, and re-anchors the publication clock. The previous frame can remain visible briefly until the first current frame is ready, preventing an incomplete frame sequence from reaching the player.

Encoded output uses bounded recovery up to 1.05x after a short stall. This allows latency to recover gradually without recreating large bitrate spikes.

## Video and image converter

The converter produces H.264 FLV media with constant FPS, a two-second keyframe interval, and AAC audio. Still images receive a static video track and silent audio.

| Input | Generated name | Duration behavior |
| --- | --- | --- |
| Image | `loading_imagem_...flv` | Held for the selected converted duration. |
| Video | `loading_video_...flv` | Repeated or trimmed to the selected duration. |

The converter uses one duration field, from **1 to 600 seconds**, and identifies its intended use automatically:

- **1 to 5 seconds:** media intended for Short loading, displayed once in that mode.
- **6 to 600 seconds:** media intended for Full loading; each duration defines a cycle that may repeat until the delayed stream is ready.

JPG/JPEG, PNG, WebP, BMP, and TIFF images can use any duration in this range and remain static for the entire selected time. Video inputs include MP4, MOV, MKV, WebM, AVI, and other FFmpeg-compatible formats.

## Quality preservation

- **Copy:** does not re-encode. H.264 video and AAC audio received from OBS/Streamlabs are preserved packet for packet.
- **Encoded:** produces one H.264/AAC generation to maintain a continuous timeline during media transitions. The app preserves configured resolution, FPS, High profile, keyframe interval, and bitrate, and avoids the lowest-efficiency NVENC and x264 presets.

In a local audit using a real 1920×1080/60 sample at 6000 kbps, Copy produced elementary audio and video streams with hashes identical to the input. The AMD AMF re-encode configured by the app measured VMAF 97.15 and an average PSNR of 58.49 dB. These results indicate excellent fidelity for the sample; final quality still depends on bitrate, available encoder, and matching resolution/FPS.

## Recommended configuration

- H.264 video and AAC audio in OBS/Streamlabs.
- Two-second keyframe interval.
- Matching resolution and FPS across OBS, DelayEngine, and loading media.
- 6000 kbps as a conservative Twitch reference.
- Auto, AMD AMF, NVIDIA NVENC, Intel Quick Sync, or CPU encoding according to the computer.
- In Encoded mode, choose Bicubic for balance, Lanczos for sharper scaling, or Bilinear for lower processing cost. The filter only applies when the OBS resolution differs from the DelayEngine output.

## Controls

- Main panel: `http://127.0.0.1:8080`
- Remote control: `http://127.0.0.1:8080/remote`
- Add delay: `Ctrl+Alt+D` by default
- Go live: `Ctrl+Alt+A` by default
- Both shortcuts can be changed in the panel to one, two, or three keys. Changes take effect immediately after saving, without restarting the app.
- Windows tray menu
- OBS/Streamlabs browser dock or Stream Deck URL action

The Twitch stream key is protected locally by Windows. Do not publish keys, personal settings, logs, runtime data, or private media. Use `limpeza-de-dados.cmd` before distributing a portable copy.
