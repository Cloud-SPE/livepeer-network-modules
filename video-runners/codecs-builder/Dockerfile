FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    nasm \
    yasm \
    cmake \
    pkg-config \
    autoconf \
    automake \
    libtool \
    wget \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# libx264
RUN git clone --depth 1 --branch stable https://github.com/mirror/x264.git && \
    cd x264 && \
    ./configure --prefix=/usr/local --enable-shared --enable-pic && \
    make -j$(nproc) && make install && \
    cd .. && rm -rf x264


# SVT-AV1
RUN git clone --depth 1 --branch v2.3.0 https://gitlab.com/AOMediaCodec/SVT-AV1.git && \
    cd SVT-AV1 && \
    mkdir build && cd build && \
    cmake -G "Unix Makefiles" \
        -DCMAKE_INSTALL_PREFIX=/usr/local \
        -DBUILD_SHARED_LIBS=ON \
        -DBUILD_TESTING=OFF \
        -DBUILD_APPS=OFF \
        .. && \
    make -j$(nproc) && make install && \
    cd /build && rm -rf SVT-AV1

# libopus
RUN git clone --depth 1 --branch v1.5.2 https://github.com/xiph/opus.git && \
    cd opus && \
    autoreconf -fiv && \
    ./configure --prefix=/usr/local --enable-shared && \
    make -j$(nproc) && make install && \
    cd .. && rm -rf opus

# libvpx
RUN git clone --depth 1 --branch v1.15.2 https://github.com/webmproject/libvpx.git && \
    cd libvpx && \
    ./configure --prefix=/usr/local \
        --enable-shared \
        --enable-vp9 \
        --enable-vp9-highbitdepth \
        --enable-pic \
        --disable-examples \
        --disable-docs \
        --disable-unit-tests && \
    make -j$(nproc) && make install && \
    cd .. && rm -rf libvpx

# libzimg (for zscale HDR tonemap CPU fallback)
RUN wget -qO zimg.tar.gz https://github.com/sekrit-twc/zimg/archive/refs/tags/release-3.0.6.tar.gz && \
    tar -xzf zimg.tar.gz && \
    cd zimg-release-3.0.6 && \
    autoreconf -fiv && \
    ./configure --prefix=/usr/local --enable-shared && \
    make -j$(nproc) && make install && \
    cd .. && rm -rf zimg-release-3.0.6 zimg.tar.gz
