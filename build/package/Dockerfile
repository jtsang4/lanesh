# Built by GoReleaser (dockers_v2) from prebuilt binaries; see .goreleaser.yaml.
FROM gcr.io/distroless/static-debian13:nonroot
ARG TARGETPLATFORM

COPY $TARGETPLATFORM/lanesh /usr/bin/lanesh

# The base image runs as the nonroot user (65532); WORKDIR creates /data owned
# by that user so a fresh named volume is writable.
WORKDIR /data

ENV LANESH_LISTEN=:8080 \
    LANESH_DATA_DIR=/data

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/bin/lanesh", "healthcheck"]

ENTRYPOINT ["/usr/bin/lanesh"]
CMD ["server"]
