# Third-Party Notices

DelayEngine uses open source dependencies and optional bundled tools.

## Go Dependencies

Main runtime dependencies include:

- `github.com/bluenviron/gortmplib`
- `github.com/bluenviron/mediacommon`
- `github.com/abema/go-mp4`

See `go.mod` and `go.sum` for the exact dependency list and versions.

## MediaMTX

The portable app can include MediaMTX for local streaming.

Project: https://github.com/bluenviron/mediamtx

MediaMTX is not owned by DelayEngine. Keep its license and notices when redistributing a bundled copy.

## FFmpeg

The portable app can include FFmpeg/ffprobe for video conversion and the optional polished Twitch relay.

Project: https://ffmpeg.org/

FFmpeg is not owned by DelayEngine. Keep the FFmpeg license and notices that match the build you distribute.

## User Content

Videos, stream keys, logs, and local configuration created by the user are not part of the DelayEngine source license.
