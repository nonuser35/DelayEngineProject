🇬🇧 [English](README.en.md) | 🇧🇷 [Português](README.pt-BR.md)

---

# DelayEngine

DelayEngine is a local Windows app that lets streamers add or remove delay during a live stream, using a loading video as a transition, without restarting the broadcast.

It is designed to run between OBS/Streamlabs and Twitch:

```text
OBS / Streamlabs
↓
Local MediaMTX
↓
DelayEngine
↓
Twitch
```

## Goal

The goal of DelayEngine is to give the host control over stream delay while the broadcast is already running.

With it, the streamer can:

- start the stream normally;
- add delay in the middle of the live stream;
- show a loading video while delay is being applied;
- return live afterward;
- prepare loading videos in the correct format;
- manage everything from a local web interface.

## Main Features

- Local web panel.
- Windows tray app.
- OBS/Streamlabs integration.
- Bundled local MediaMTX.
- Polished Twitch mode for a more stable output.
- Delay with loading video transition.
- Full loading option.
- Video converter to FLV H264/AAC.
- Automatic detection of stream resolution/FPS/bitrate.
- Local preview of the received stream.
- Logs separated by area.
- Disk safety buffer up to 1 hour.
- Data-cleaning script for sharing a clean folder.

## Runs Locally

DelayEngine runs entirely on the user's computer.

It does not need an external server to stream, apply delay, convert videos or keep the buffer.

The only optional external fetch made by the interface is the public support/contribution card from GitHub. If the internet is unavailable, this does not affect the live stream.

## How the Buffer Works

DelayEngine keeps a recent packet window on disk.

This buffer:

- stores encoded packets;
- preserves PTS/DTS;
- preserves keyframes;
- automatically removes old packets;
- keeps about the latest 1 hour of the stream;
- does not grow forever;
- is cleared when the app starts.

This 1-hour buffer is a pipeline safety margin, not a permanent recording.

## How Delay Works

When the user adds delay, DelayEngine waits until the buffer has enough content and then publishes an older point of the stream.

If a loading video is configured, it appears while delay is being applied.

When the user clicks Go live, DelayEngine discards the accumulated delay and returns to the recent point of the stream.

## Polished Twitch Mode

Polished Twitch mode is the recommended mode for real broadcasts.

In this mode, DelayEngine publishes to a local output, and a local encoder keeps one continuous stream going to Twitch.

This helps reduce loading, dropouts or latency accumulation when the host enters and leaves manual delay.

## Video Converter

The app includes a converter for preparing loading videos.

It can:

- convert MP4 to compatible FLV;
- adjust resolution;
- adjust FPS;
- adjust bitrate;
- use AAC audio;
- repeat short videos;
- cut long videos;
- save the result in `videos/ready`.

The goal is to make the loading video match the stream, avoiding awkward visual transitions.

## How to Use

1. Download the app folder.
2. Extract the ZIP.
3. Open `DelayEngine.exe`.
4. Save your Twitch stream key in the panel.
5. Go to OBS Data.
6. Copy the local server and local key.
7. In OBS/Streamlabs, use custom stream settings.
8. Paste the local server into the Server field.
9. Paste the local key into the Key field.
10. Start streaming in OBS/Streamlabs.
11. Use DelayEngine's panel to add or remove delay.

## Important

Do not paste the Twitch key into OBS/Streamlabs.

OBS should send to DelayEngine. The Twitch key stays saved in the app, and the app handles the Twitch output.

## Privacy

User data stays on the user's own computer:

- stream key;
- settings;
- logs;
- converted videos;
- temporary buffer.

The app does not need to send this data to any external server.

## Cleanup Before Sharing

The folder includes:

```text
limpeza-de-dados.cmd
```

This script removes personal data and makes the folder ready to be shared with someone else.

## License

Open source project under the MIT License.

See:

- `LICENSE`
- `NOTICE`
- `THIRD_PARTY_NOTICES.md`
