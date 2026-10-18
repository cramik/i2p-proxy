# Builds libi2pd.a + our capi/capi2 wrapper objects for linux/amd64, and a
# tiny smoke-test binary proving the whole chain compiles and links. This is
# the proof-of-build step before wiring cgo bindings into the Go module.
FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential git ca-certificates \
    libboost-system-dev libboost-filesystem-dev libboost-date-time-dev \
    libboost-program-options-dev libboost-json-dev \
    libssl-dev zlib1g-dev libminiupnpc-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# Copy only what `make api` needs first, so edits to libi2pd_wrapper/*.cpp
# (iterated on far more often) don't invalidate this expensive layer.
COPY i2pd/libi2pd /src/i2pd/libi2pd
COPY i2pd/libi2pd_client /src/i2pd/libi2pd_client
COPY i2pd/i18n /src/i2pd/i18n
COPY i2pd/Makefile i2pd/Makefile.linux i2pd/filelist.mk /src/i2pd/

# Core router library only (RouterContext/Transports/NetDb/Tunnel/Destination
# /Streaming/Identity/api.cpp) — deliberately skips libi2pd_client (SAM/BOB/
# I2PTunnel/HTTPProxy) and i18n objects (headers still copied above for
# -I include paths), which our capi2 shim doesn't need.
RUN cd /src/i2pd && make api -j2 DEBUG=no

COPY i2pd/libi2pd_wrapper /src/i2pd/libi2pd_wrapper
COPY i2pd/daemon/UPnP.cpp i2pd/daemon/UPnP.h /src/i2pd/daemon/
RUN cd /src/i2pd && \
    g++ -std=c++20 -fPIC -DUSE_UPNP -Ilibi2pd -c libi2pd_wrapper/capi.cpp -o /tmp/capi.o && \
    g++ -std=c++20 -fPIC -DUSE_UPNP -Ilibi2pd -c libi2pd_wrapper/capi2.cpp -o /tmp/capi2.o && \
    g++ -std=c++20 -fPIC -DUSE_UPNP -Ilibi2pd -c daemon/UPnP.cpp -o /tmp/upnp.o && \
    ar rcs libi2pdwrapper.a /tmp/capi.o /tmp/capi2.o /tmp/upnp.o

COPY docker/linux-amd64/smoketest.cpp /src/smoketest.cpp
RUN mkdir -p /out && cd /src && g++ -std=c++20 -Ii2pd/libi2pd -Ii2pd/libi2pd_wrapper \
    smoketest.cpp \
    i2pd/libi2pdwrapper.a i2pd/libi2pd.a \
    -lssl -lcrypto -lz -lboost_system -lboost_filesystem -lboost_date_time \
    -lboost_program_options -lboost_json -lpthread -latomic -lminiupnpc \
    -o /out/smoketest

CMD ["/out/smoketest"]
