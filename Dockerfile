# ---------- build stage ----------
FROM golang:1.24-bookworm AS build

# whisper.cpp ZUERST — hängt NICHT vom Source ab. So bleibt diese teure Layer
# über Commits hinweg gecacht (vorher stand sie hinter `COPY . .` und wurde bei
# JEDEM Deploy neu gebaut → klonte jedes Mal whisper.cpp und lief 2026-08-31 in
# GitHubs Anonym-Clone-Rate-Limit vom Build-Host: „could not read Username for
# 'https://github.com'"). Nur ein WHISPER_TAG-Wechsel invalidiert die Layer.
ARG WHISPER_TAG=v1.7.5
RUN apt-get update && apt-get install -y --no-install-recommends \
      cmake g++ git libgomp1 \
      libopenblas-dev \
    && for i in 1 2 3 4 5 6; do \
         git clone --depth 1 --branch ${WHISPER_TAG} https://github.com/ggerganov/whisper.cpp /whisper && break; \
         echo "git clone Versuch $i/6 fehlgeschlagen, warte…"; sleep 25; \
       done \
    && test -d /whisper/.git \
    && cmake -B /whisper/build -S /whisper \
         -DWHISPER_BUILD_TESTS=OFF \
         -DWHISPER_BUILD_EXAMPLES=ON \
         -DBUILD_SHARED_LIBS=OFF \
         -DGGML_BLAS=ON \
         -DGGML_BLAS_VENDOR=OpenBLAS \
         -DCMAKE_BUILD_TYPE=Release \
    && cmake --build /whisper/build --config Release -j$(nproc) \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY . .

# Frontend-Assets lokal bereitstellen (statt CDN-Abhängigkeit).
# Video.js 8 bringt eigene HLS-Unterstützung (VHS) mit — HLS.js nicht mehr nötig.
# videojs-vtt-thumbnails wurde entfernt (2026-09-02, Lizenz-Check): totes Gewicht,
# Trickplay-Hover ist ein eigenes Mini-Plugin inline in player.js (siehe CLAUDE.md),
# das externe Plugin wurde nirgends mehr referenziert.
ARG VIDEOJS_VERSION=8.17.3
RUN set -eux \
 && curl -fsSL "https://cdn.jsdelivr.net/npm/video.js@${VIDEOJS_VERSION}/dist/video.min.js" \
      -o /src/internal/webassets/web/video.min.js \
 && curl -fsSL "https://cdn.jsdelivr.net/npm/video.js@${VIDEOJS_VERSION}/dist/video-js.min.css" \
      -o /src/internal/webassets/web/video-js.min.css

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
      curl \
      libgomp1 \
      libopenblas0 \
      libchromaprint-tools \
      tesseract-ocr \
      tesseract-ocr-deu \
      tesseract-ocr-eng \
      tesseract-ocr-ita \
      mkvtoolnix \
      python3 \
      python3-pip \
    && rm -rf /var/lib/apt/lists/*

# pgsrip: PGS/VOBSUB-Bild-Untertitel → SRT per Tesseract-OCR (internal/ocrsub).
# --break-system-packages: Debian bookworm ist PEP-668-„externally managed".
RUN pip3 install --no-cache-dir --break-system-packages pgsrip

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
COPY --from=build /whisper/build/bin/whisper-cli /usr/local/bin/whisper-cli

EXPOSE 8096
VOLUME ["/config", "/media"]

WORKDIR /app
ENTRYPOINT ["/app/goldfish"]
