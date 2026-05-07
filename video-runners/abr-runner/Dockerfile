ARG REGISTRY=tztcloud
ARG TAG=v0.8.10
ARG CODECS_IMAGE=${REGISTRY}/codecs-builder:${TAG}
FROM ${CODECS_IMAGE} AS codecs-builder

# ============================================================================
# Stage: ffmpeg-nvidia — ffmpeg with CUDA/NVENC/NVDEC support
# ============================================================================
FROM nvidia/cuda:12.9.1-devel-ubuntu24.04 AS ffmpeg-nvidia

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    nasm \
    yasm \
    pkg-config \
    wget \
    ca-certificates \
    libssl-dev \
    libx265-dev \
    && rm -rf /var/lib/apt/lists/*

COPY --from=codecs-builder /usr/local /usr/local
RUN ldconfig

WORKDIR /build

RUN git clone --depth 1 --branch n12.2.72.0 https://github.com/FFmpeg/nv-codec-headers.git && \
    cd nv-codec-headers && \
    make install PREFIX=/usr/local && \
    cd .. && rm -rf nv-codec-headers

ENV PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:/usr/local/lib/x86_64-linux-gnu/pkgconfig
ENV LD_LIBRARY_PATH=/usr/local/lib:/usr/local/lib/x86_64-linux-gnu

RUN git clone --depth 1 --branch n7.1.3 https://github.com/FFmpeg/FFmpeg.git && \
    cd FFmpeg && \
    ./configure \
        --prefix=/usr/local \
        --enable-gpl \
        --enable-nonfree \
        --enable-cuda-nvcc \
        --enable-openssl \
        --enable-libnpp \
        --enable-nvenc \
        --enable-nvdec \
        --enable-cuvid \
        --enable-libx264 \
        --enable-libx265 \
        --enable-libsvtav1 \
        --enable-libopus \
        --enable-libvpx \
        --enable-libzimg \
        --enable-shared \
        --extra-cflags="-I/usr/local/include -I/usr/local/cuda/include" \
        --extra-ldflags="-L/usr/local/lib -L/usr/local/cuda/lib64" && \
    make -j$(nproc) && make install && \
    cd .. && rm -rf FFmpeg

RUN ldconfig

# ============================================================================
# Stage: ffmpeg-intel — ffmpeg with Intel QSV (oneVPL) + VAAPI support
# ============================================================================
FROM ubuntu:24.04 AS ffmpeg-intel

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    nasm \
    yasm \
    pkg-config \
    wget \
    ca-certificates \
    gpg \
    gpg-agent \
    software-properties-common \
    && rm -rf /var/lib/apt/lists/*

RUN wget -qO - https://repositories.intel.com/gpu/intel-graphics.key | \
    gpg --dearmor -o /usr/share/keyrings/intel-graphics.gpg && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/intel-graphics.gpg] https://repositories.intel.com/gpu/ubuntu noble unified" \
    > /etc/apt/sources.list.d/intel-gpu.list && \
    apt-get update && apt-get install -y --no-install-recommends \
    libva-dev \
    libvpl-dev \
    vainfo \
    intel-media-va-driver-non-free \
    libssl-dev \
    libx265-dev \
    && rm -rf /var/lib/apt/lists/*

COPY --from=codecs-builder /usr/local /usr/local
RUN ldconfig

WORKDIR /build

ENV PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:/usr/local/lib/x86_64-linux-gnu/pkgconfig
ENV LD_LIBRARY_PATH=/usr/local/lib:/usr/local/lib/x86_64-linux-gnu

RUN git clone --depth 1 --branch n7.1.3 https://github.com/FFmpeg/FFmpeg.git && \
    cd FFmpeg && \
    ./configure \
        --prefix=/usr/local \
        --enable-gpl \
        --enable-nonfree \
        --enable-libvpl \
        --enable-openssl \
        --enable-vaapi \
        --enable-libx264 \
        --enable-libx265 \
        --enable-libsvtav1 \
        --enable-libopus \
        --enable-libvpx \
        --enable-libzimg \
        --enable-shared \
        --extra-cflags="-I/usr/local/include" \
        --extra-ldflags="-L/usr/local/lib" && \
    make -j$(nproc) && make install && \
    cd .. && rm -rf FFmpeg

RUN ldconfig

# ============================================================================
# Stage: ffmpeg-amd — ffmpeg with AMD VAAPI support
# ============================================================================
FROM ubuntu:24.04 AS ffmpeg-amd

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    nasm \
    yasm \
    pkg-config \
    wget \
    ca-certificates \
    libva-dev \
    libdrm-dev \
    mesa-va-drivers \
    vainfo \
    libssl-dev \
    libx265-dev \
    && rm -rf /var/lib/apt/lists/*

COPY --from=codecs-builder /usr/local /usr/local
RUN ldconfig

WORKDIR /build

ENV PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:/usr/local/lib/x86_64-linux-gnu/pkgconfig
ENV LD_LIBRARY_PATH=/usr/local/lib:/usr/local/lib/x86_64-linux-gnu

RUN git clone --depth 1 --branch n7.1.3 https://github.com/FFmpeg/FFmpeg.git && \
    cd FFmpeg && \
    ./configure \
        --prefix=/usr/local \
        --enable-gpl \
        --enable-nonfree \
        --enable-vaapi \
        --enable-openssl \
        --enable-libx264 \
        --enable-libx265 \
        --enable-libsvtav1 \
        --enable-libopus \
        --enable-libvpx \
        --enable-libzimg \
        --enable-shared \
        --extra-cflags="-I/usr/local/include" \
        --extra-ldflags="-L/usr/local/lib" && \
    make -j$(nproc) && make install && \
    cd .. && rm -rf FFmpeg

RUN ldconfig

# ============================================================================
# Stage: go-builder — shared across all GPU targets
# ============================================================================
FROM golang:1.22-alpine AS go-builder

RUN apk add --no-cache git

WORKDIR /app

COPY transcode-core/go.mod transcode-core/go.sum* /app/transcode-core/
COPY abr-runner/go.mod abr-runner/go.sum* /app/abr-runner/

WORKDIR /app/abr-runner
RUN go mod download 2>/dev/null || true

COPY transcode-core/ /app/transcode-core/
COPY abr-runner/ /app/abr-runner/

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/abr-runner .

# ============================================================================
# Stage: runtime-nvidia
# ============================================================================
FROM nvidia/cuda:12.9.1-runtime-ubuntu24.04 AS runtime-nvidia

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libx265-199 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ffmpeg-nvidia /usr/local/bin/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-nvidia /usr/local/bin/ffprobe /usr/local/bin/ffprobe
COPY --from=ffmpeg-nvidia /usr/local/lib/ /usr/local/lib/

RUN ldconfig

COPY --from=go-builder /bin/abr-runner /usr/local/bin/abr-runner
COPY abr-runner/offering.yaml /etc/runner/offering.yaml

RUN groupadd -r transcode && useradd -r -g transcode -d /home/transcode -s /sbin/nologin transcode
RUN mkdir -p /tmp/abr && chown transcode:transcode /tmp/abr

USER transcode
ENV CAPABILITY_NAME=abr-ladder
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/abr-runner", "-healthcheck"] || exit 1
ENTRYPOINT ["/usr/local/bin/abr-runner"]

# ============================================================================
# Stage: runtime-intel
# ============================================================================
FROM ubuntu:24.04 AS runtime-intel

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    gpg \
    gpg-agent \
    wget \
    software-properties-common \
    && rm -rf /var/lib/apt/lists/*

RUN wget -qO - https://repositories.intel.com/gpu/intel-graphics.key | \
    gpg --dearmor -o /usr/share/keyrings/intel-graphics.gpg && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/intel-graphics.gpg] https://repositories.intel.com/gpu/ubuntu noble unified" \
    > /etc/apt/sources.list.d/intel-gpu.list && \
    apt-get update && apt-get install -y --no-install-recommends \
    libva2 \
    libmfx-gen1.2 \
    libvpl2 \
    intel-media-va-driver-non-free \
    vainfo \
    libx265-199 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ffmpeg-intel /usr/local/bin/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-intel /usr/local/bin/ffprobe /usr/local/bin/ffprobe
COPY --from=ffmpeg-intel /usr/local/lib/ /usr/local/lib/

RUN ldconfig

COPY --from=go-builder /bin/abr-runner /usr/local/bin/abr-runner
COPY abr-runner/offering.yaml /etc/runner/offering.yaml

RUN groupadd -r transcode && useradd -r -g transcode -d /home/transcode -s /sbin/nologin transcode && \
    groupadd -r render 2>/dev/null || true && \
    usermod -aG video,render transcode
RUN mkdir -p /tmp/abr && chown transcode:transcode /tmp/abr

USER transcode
ENV CAPABILITY_NAME=abr-ladder
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/abr-runner", "-healthcheck"] || exit 1
ENTRYPOINT ["/usr/local/bin/abr-runner"]

# ============================================================================
# Stage: runtime-amd
# ============================================================================
FROM ubuntu:24.04 AS runtime-amd

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libva2 \
    libdrm2 \
    mesa-va-drivers \
    vainfo \
    libx265-199 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ffmpeg-amd /usr/local/bin/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-amd /usr/local/bin/ffprobe /usr/local/bin/ffprobe
COPY --from=ffmpeg-amd /usr/local/lib/ /usr/local/lib/

RUN ldconfig

COPY --from=go-builder /bin/abr-runner /usr/local/bin/abr-runner
COPY abr-runner/offering.yaml /etc/runner/offering.yaml

RUN groupadd -r transcode && useradd -r -g transcode -d /home/transcode -s /sbin/nologin transcode && \
    groupadd -r render 2>/dev/null || true && \
    usermod -aG video,render transcode
RUN mkdir -p /tmp/abr && chown transcode:transcode /tmp/abr

USER transcode
ENV CAPABILITY_NAME=abr-ladder
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/abr-runner", "-healthcheck"] || exit 1
ENTRYPOINT ["/usr/local/bin/abr-runner"]
