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

```bash
docker compose up -d --build
# open http://<host>:8096
```

On first launch the UI asks you to create the initial admin account.

Add a library (📁 icon), scan it, grab a free
[TMDB API key](https://www.themoviedb.org/settings/api) and paste it into
Settings → TMDB.

## docker-compose.yml (template)

```yaml
services:
  goldfish:
    build: .
    image: goldfish:latest
    restart: unless-stopped
    ports:
      - "8096:8096"
    devices:
      - "/dev/dri:/dev/dri"   # Intel iGPU passthrough for VAAPI
    group_add:
      - "107"                  # render group on most Linux hosts
    volumes:
      - ./config:/config
      - /mnt/media:/media:ro   # your media root (read-only)
    environment:
      - VP_LISTEN=:8096
      - VP_CONFIG_DIR=/config
      - TZ=Europe/Berlin
```

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
