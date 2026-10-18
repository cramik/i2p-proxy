# Verifies the cgo i2pd backend actually compiles and links for
# linux/amd64 using the prebuilt libi2pd.a / libi2pdwrapper.a produced by
# Dockerfile in this same directory. Uses the same ubuntu:24.04 base (Boost
# 1.83, has boost_json) as that build — golang:*-bookworm ships Boost 1.74,
# which predates Boost.JSON and breaks linking against libi2pd.a.
FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl ca-certificates build-essential \
    libboost-system-dev libboost-filesystem-dev libboost-date-time-dev \
    libboost-program-options-dev libboost-json-dev libssl-dev zlib1g-dev \
    libminiupnpc-dev \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://go.dev/dl/go1.23.4.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /src
COPY . /src

ENV CGO_ENABLED=1
RUN go build -o /out/i2p-proxy-linux-amd64-i2pd ./cmd/i2p-proxy

CMD ["/out/i2p-proxy-linux-amd64-i2pd", "-h"]
