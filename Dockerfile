# Shadow Mock server image. Build from the repository root:
#   docker build --build-arg VERSION=0.2.0 -t shadow-mock .
# Published images are on ghcr.io/pwgith/shadow-mock; see docs/guide/15-docker.md.

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG VERSION=0.0.0-dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/shadowmock ./cmd/shadowmock
# The data directory is made here because the final image has no shell.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/shadowmock /shadowmock
COPY --from=build --chown=nonroot:nonroot /out/data /data

# mappings/, __files/ and saved sessions live under /data. Mount a volume there to keep them.
ENV SHADOWMOCK_ROOT=/data \
    SHADOWMOCK_LISTEN_MOCK=:8080
VOLUME /data
EXPOSE 8080 9090
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/shadowmock", "-healthcheck"]
ENTRYPOINT ["/shadowmock"]
