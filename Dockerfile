FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
ARG TARGETOS TARGETARCH VERSION=dev
RUN mkdir /data
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /sunspec-meter-bridge .

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/nilseckert/sunspec-meter-bridge" \
      org.opencontainers.image.description="Shelly- und go-e-Messwerte als Fronius-kompatible Modbus-TCP-Zähler (SunSpec)" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /sunspec-meter-bridge /sunspec-meter-bridge
COPY --from=build --chown=65532:65532 /data /data
VOLUME /data
EXPOSE 502 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/sunspec-meter-bridge", "-healthcheck"]
ENTRYPOINT ["/sunspec-meter-bridge"]
CMD ["-config", "/config/config.yaml"]
