FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/artitrove ./cmd/artitrove \
 && mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/niekcandaele/artitrove" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/artitrove /artitrove
COPY --from=build --chown=65532:65532 /out/data /data
ENV ARTITROVE_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --start-interval=1s --retries=3 \
  CMD ["/artitrove", "healthcheck"]
ENTRYPOINT ["/artitrove"]
