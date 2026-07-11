# DelayEngine — portable package guide

[← Home](README.md) | [Português (Brasil)](README.pt-BR.md)

## First use

1. Open `DelayEngine.exe`.
2. In OBS/Streamlabs, use the local server and stream key displayed in the panel.
3. Open `http://127.0.0.1:8080` and save your Twitch stream key.
4. Wait for the panel to report connected input and output.

Use **Add delay with loading** to apply delay and **Go live** to return to realtime. Set OBS/Streamlabs keyframes to 2 seconds.

## Important

- **Copy** is the recommended mode: actual bitrate and encoder come from OBS/Streamlabs.
- Use 6000 kbps as the stable Twitch reference.
- **Return through buffer** is experimental; loading mode is more predictable.
- Closing the browser does not stop the live stream. Use the Windows tray icon to quit the app.

## Your data

This folder may contain local settings, videos, logs, and runtime data. They were preserved during this update. Before sharing a copy, run `limpeza-de-dados.cmd` in the copy you will distribute.
