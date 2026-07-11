# Security Policy

## Sensitive Data

DelayEngine can store a Twitch stream key locally. Treat it as secret.

Do not publish:

- `.twitch-stream-key`
- `.twitch-stream-key.dpapi`
- `.local-stream-name`
- `settings.json` from a personal installation
- logs that expose stream URLs or local paths

Before sharing a portable app folder, run:

```cmd
limpeza-de-dados.cmd
```

Or build a clean package from source:

```powershell
.\scripts\build-windows.ps1 -PortableClean
```

## Reporting Issues

If you find a security problem, open a private report on GitHub if available, or contact the project owner directly before posting public details.
