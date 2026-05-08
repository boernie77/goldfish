# Goldfish 🐠

A lean, single-binary, Jellyfin-style streaming server for home labs.
Written in Go, runs in a ~150 MB Docker image, no external dependencies.

- **Direct Play** over HTTP Range for browser-compatible files
- **On-the-fly HLS transcoding** with Intel VAAPI hardware acceleration
  (software fallback when no iGPU is present)
- **TMDB-powered metadata**, posters, cast, collections
- **Multi-user** with per-user libraries, watched/favorite state, playlists
- **Embedded Video.js** player with trickplay hover previews
- **Full-text search** by title or actor name

## Quickstart

### Easiest: interactive installer

```bash
wget https://raw.githubusercontent.com/<your-fork>/goldfish/main/install.sh
chmod +x install.sh
./install.sh
```

The installer pops up dialog boxes for the install path, media path,
render-group ID (auto-detected), NVIDIA support, optional OIDC SSO config —
then clones the repo, writes `.env`, builds, starts. Done in 2 minutes on a
warm Docker cache.

### Manual

```bash
git clone https://github.com/<your-fork>/goldfish
cd goldfish
cp .env.example .env       # adjust RENDER_GID + MEDIA_ROOT to your host
docker compose up -d --build
# open http://<host>:8096
```

On first launch the UI shows a **setup wizard** that asks for:

- Admin username + password (the first account is automatically admin)
- *Optional:* TMDB API key (free at https://www.themoviedb.org/settings/api)
- *Optional:* OMDb API key (fallback for IMDb-ID lookups)

Without a TMDB key Goldfish still scans and plays your videos — but no
posters, plot or cast info. You can paste the key later in Settings → TMDB.

After the wizard: add a library (📁 icon), point it at a sub-path under
`/media`, hit "Scan". Metadata enrichment runs in the background.

## Host setup before first run

The two values you almost certainly need to adjust in `.env`:

| Variable | What | How to find it |
|----------|------|----------------|
| `RENDER_GID` | The numeric ID of the host's `render` group, so the container can talk to `/dev/dri` for VAAPI. Default 107 (Debian/Ubuntu/Mint). | `getent group render` returns `render:x:<GID>:` — that number. On Arch/Fedora often 989, on Unraid 109. |
| `MEDIA_ROOT` | Absolute path to your media library on the host. Mounted read-only into `/media` inside the container. | Just the path. Examples: `/mnt/media`, `/mnt/user` (Unraid), `/volume1/Video` (Synology), `/srv/media`. |

Both have sensible defaults so the stack will start without `.env`, but the
container won't see your media until `MEDIA_ROOT` points at the right place.

### NVIDIA NVENC (optional)

If your host has a CUDA-capable NVIDIA GPU and the
[NVIDIA Container Runtime](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html),
uncomment the `runtime: nvidia` block and the three NVIDIA env vars in
`docker-compose.yml`. On Unraid: install the NVIDIA Driver plugin first,
then a host reboot — `nvidia-smi` must work on the host before Goldfish can
use the GPU.

After the container is up, switch in **Settings → Hardware** to "NVIDIA NVENC"
or leave it on Auto (which prefers VAAPI > NVENC > Software).

### OIDC SSO (optional)

For single-sign-on with Authentik / Keycloak / Authelia / Zitadel etc., set
`OIDC_*` in `.env` (template at the bottom of `.env.example`). Without
these the SSO button on the login page is hidden — username + password
keeps working as a normal fallback.

## Configuration

All runtime config is stored in the SQLite database under `/config`. No
environment variables are required beyond the defaults.

| Setting | UI location | Notes |
|---------|-------------|-------|
| TMDB API key | Settings → TMDB | Required for metadata/posters |
| OMDb API key | Settings → OMDb | Optional fallback for IMDb-ID lookups |
| Client buffer (s) | Settings → Playback | HLS `maxBufferLength`, 5–180 s |
| Trickplay interval | Settings → Trickplay | Frames per sprite (default 10 s) |

## Architecture at a glance

```
cmd/goldfish/main.go          HTTP server, wiring
internal/api/                 chi routes + handlers
internal/auth/                bcrypt sessions, library-ACL middleware
internal/store/sqlite.go      schema, migrations, all queries
internal/scanner/             recursive walk + ffprobe + thumbnail
internal/playback/            decider + VAAPI ffmpeg runner + HW detection
internal/enrich/              TMDB background worker
internal/tmdb/, internal/omdb/  API clients with rate limiting
internal/trickplay/           sprite-sheet generator
internal/webassets/web/       embedded SPA (index.html, app.js, style.css)
```

## Building

No Go toolchain on the host is required — the Dockerfile is a multi-stage
build that does everything inside the image.

```bash
docker build -t goldfish:latest .
```

For local development with a Go toolchain:

```bash
go run ./cmd/goldfish  # serves on :8096 out of ./config
```

## Credits

Metadata, posters and backdrops are sourced from
[The Movie Database (TMDB)](https://www.themoviedb.org/). **This product uses
the TMDB API but is not endorsed or certified by TMDB.**

The in-browser player is built with [Video.js](https://videojs.com/).

See [`NOTICE.md`](./NOTICE.md) for the complete list of third-party
attributions and licenses.

## License

Goldfish is released under the [MIT License](./LICENSE).

The Docker image bundles third-party components under their own licenses
(notably `ffmpeg` with `libx264`/`libx265` which are GPL-2.0+). When
redistributing binaries or images, review [`NOTICE.md`](./NOTICE.md) for the
obligations that apply.
