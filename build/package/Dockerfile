# Built by GoReleaser (dockers_v2) from prebuilt binaries; see .goreleaser.yaml.

FROM busybox:stable AS data
RUN mkdir -p /data

FROM gcr.io/distroless/static-debian13:nonroot
ARG TARGETPLATFORM

COPY --from=data --chown=65532:65532 /data /data
COPY $TARGETPLATFORM/lanesh /usr/bin/lanesh

ENV LANESH_LISTEN=:8080 \
    LANESH_DATA_DIR=/data

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/bin/lanesh", "healthcheck"]

ENTRYPOINT ["/usr/bin/lanesh"]
CMD ["server"]
