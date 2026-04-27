# Goldfish — Third-Party Notices

Goldfish is distributed under the MIT License. The runtime image and the
compiled binary include third-party components with their own licenses,
listed below. If you redistribute binaries/images, include this NOTICE.

## TMDB API

This product uses the TMDB API but is not endorsed or certified by TMDB.
TMDB's logo and attribution are required when their data is displayed — see
<https://www.themoviedb.org/about/logos-attribution>.

## OMDb API

Movie metadata fallback is optionally sourced from OMDb
(<https://www.omdbapi.com/>). OMDb is a free service with its own terms of
use; an API key is required and configured by the end user.

## Front-end libraries

| Component | License | Upstream |
|-----------|---------|----------|
| Video.js  | Apache-2.0 | <https://github.com/videojs/video.js> |

The Video.js NOTICE accompanying its distribution is preserved in its source
bundle at `internal/webassets/web/video.min.js` (header comment) and
`internal/webassets/web/video-js.min.css`.

## Go modules (compiled into the binary)

| Module | License |
|--------|---------|
| github.com/go-chi/chi/v5 | MIT |
| golang.org/x/crypto | BSD-3-Clause |
| golang.org/x/sys | BSD-3-Clause |
| modernc.org/sqlite | BSD-3-Clause |
| modernc.org/libc | BSD-3-Clause |
| modernc.org/memory | BSD-3-Clause |
| modernc.org/mathutil | BSD-3-Clause |
| modernc.org/strutil | BSD-3-Clause |
| modernc.org/token | BSD-3-Clause |
| modernc.org/gc/v3 | BSD-3-Clause |
| github.com/remyoudompheng/bigfft | BSD-3-Clause |
| github.com/google/uuid | BSD-3-Clause |
| github.com/dustin/go-humanize | MIT |
| github.com/mattn/go-isatty | MIT |
| github.com/ncruces/go-strftime | MIT |
| github.com/hashicorp/golang-lru/v2 | MPL-2.0 |

The `golang-lru` component is licensed under the Mozilla Public License 2.0
(file-level copyleft). The unmodified source is distributed as part of the
Go module cache that Go toolchains embed; if you redistribute binaries, a
copy of the MPL-2.0 text and the source of `golang-lru` must be made
available. See <https://www.mozilla.org/MPL/2.0/>.

## Runtime system components (Debian `bookworm-slim` base image)

The Docker image installs the following binary packages via `apt-get`, each
under its own license. Redistribution must follow those licenses.

- `ffmpeg` — LGPL-2.1+ (and GPL-2.0+ when built with GPL-licensed encoders)
- `libx264` — GPL-2.0+ (used by ffmpeg for H.264 software encoding)
- `libx265` — GPL-2.0+ (used by ffmpeg for HEVC software encoding)
- `intel-media-va-driver`, `i965-va-driver`, `libva*` — MIT / Apache-2.0
- `vainfo` — MIT
- `ca-certificates`, `tzdata` — Mozilla Public License / public domain

When redistributing the Docker image, the GPL-2.0+ components in ffmpeg
require that you either make the corresponding source available or link to
a publicly accessible source location (e.g. Debian's package archive at
<https://packages.debian.org/>).

---

If you find a license or attribution error, please open an issue.
