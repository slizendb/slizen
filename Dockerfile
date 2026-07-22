FROM golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/slizendb/slizen/internal/buildinfo.Version=${VERSION} -X github.com/slizendb/slizen/internal/buildinfo.Commit=${COMMIT}" -o /out/slizend ./cmd/slizend
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/slizendb/slizen/internal/buildinfo.Version=${VERSION} -X github.com/slizendb/slizen/internal/buildinfo.Commit=${COMMIT}" -o /out/slizenctl ./cmd/slizenctl

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="Slizen" \
      org.opencontainers.image.description="Self-hosted adaptive cache layer for Redis and Valkey" \
      org.opencontainers.image.source="https://github.com/slizendb/slizen" \
      org.opencontainers.image.documentation="https://github.com/slizendb/slizen#readme" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"

COPY --from=build /out/slizend /usr/local/bin/slizend
COPY --from=build /out/slizenctl /usr/local/bin/slizenctl
COPY slizen.example.toml /etc/slizen/slizen.toml
COPY LICENSE NOTICE /licenses/slizen/

EXPOSE 6380 9090
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/slizend"]
CMD ["--config", "/etc/slizen/slizen.toml"]
