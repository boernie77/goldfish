# ---------- build stage ----------
FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY . .

# Frontend-Assets lokal bereitstellen (statt CDN-Abhängigkeit).
# Video.js 8 bringt eigene HLS-Unterstützung (VHS) mit — HLS.js nicht mehr nötig.
ARG VIDEOJS_VERSION=8.17.3
ARG VTT_THUMBS_VERSION=0.0.13
RUN set -eux \
 && curl -fsSL "https://cdn.jsdelivr.net/npm/video.js@${VIDEOJS_VERSION}/dist/video.min.js" \
      -o /src/internal/webassets/web/video.min.js \
 && curl -fsSL "https://cdn.jsdelivr.net/npm/video.js@${VIDEOJS_VERSION}/dist/video-js.min.css" \
      -o /src/internal/webassets/web/video-js.min.css \
 && curl -fsSL "https://cdn.jsdelivr.net/npm/videojs-vtt-thumbnails@${VTT_THUMBS_VERSION}/dist/videojs-vtt-thumbnails.js" \
      -o /src/internal/webassets/web/videojs-vtt-thumbnails.js \
 && curl -fsSL "https://cdn.jsdelivr.net/npm/videojs-vtt-thumbnails@${VTT_THUMBS_VERSION}/dist/videojs-vtt-thumbnails.css" \
      -o /src/internal/webassets/web/videojs-vtt-thumbnails.css

# go mod tidy erzeugt go.sum und lädt Abhängigkeiten in einem Schritt
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
      -o /out/goldfish ./cmd/goldfish

# ---------- runtime stage ----------
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    VP_CONFIG_DIR=/config \
    VP_LISTEN=:8096

# ffmpeg + Hardware-Acceleration-Stack:
# - VAAPI für Intel/AMD iGPUs (intel-media-va-driver, i965-va-driver)
# - NVENC/CUDA für NVIDIA-Karten (nvidia-smi via nvidia-utils zur Detection)
# Die NVIDIA-Treiber selbst kommen vom Host via --runtime=nvidia.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ffmpeg \
      vainfo \
      libva2 \
      libva-drm2 \
      intel-media-va-driver \
      i965-va-driver \
      ca-certificates \
      tzdata \
    && rm -rf /var/lib/apt/lists/*

# nvidia-smi wird NICHT im Image installiert — es wird vom Host via
# nvidia-container-runtime gemountet, sobald NVIDIA_DRIVER_CAPABILITIES
# "utility" enthält. So bleibt das Image NVIDIA-vendor-frei.

# NVIDIA: signalisiert dem nvidia-container-runtime, alle GPUs durchzureichen
# (wirkt nur, wenn der Container mit --runtime=nvidia gestartet wird).
ENV NVIDIA_VISIBLE_DEVICES=all \
    NVIDIA_DRIVER_CAPABILITIES=compute,video,utility

# Add rendering group so the binary can access /dev/dri when passed through
RUN groupadd -g 107 render 2>/dev/null || true

COPY --from=build /out/goldfish /app/goldfish

EXPOSE 8096
VOLUME ["/config", "/media"]

WORKDIR /app
ENTRYPOINT ["/app/goldfish"]
