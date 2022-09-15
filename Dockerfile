FROM golang:1.18.4 as builder

RUN apt-get update &&  apt-get install -y \
    make \
    gcc \
    upx \
    git \
    musl-dev

RUN mkdir -p /go/src/github.com/smartxworks/elf-csi-driver

WORKDIR /go/src/github.com/smartxworks/elf-csi-driver
COPY . .

RUN make && upx bin/csi-driver

FROM ubuntu:20.04 as csi-driver

RUN apt-get update && apt-get install -y libkmod-dev udev \
    ca-certificates \
    e2fsprogs \
    xfsprogs \
    util-linux \
    lsscsi \
    libisns0

COPY --from=builder /go/src/github.com/smartxworks/elf-csi-driver/bin/csi-driver /usr/sbin/csi-driver
COPY scripts/csi-entrypoint.sh /usr/sbin/csi-entrypoint.sh

WORKDIR /

ENTRYPOINT ["/usr/sbin/csi-entrypoint.sh"]
