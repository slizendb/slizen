FROM golang:1.26.4-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/slizendb/slizen/internal/buildinfo.Version=${VERSION} -X github.com/slizendb/slizen/internal/buildinfo.Commit=${COMMIT}" -o /out/slizend ./cmd/slizend
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/slizendb/slizen/internal/buildinfo.Version=${VERSION} -X github.com/slizendb/slizen/internal/buildinfo.Commit=${COMMIT}" -o /out/slizenctl ./cmd/slizenctl

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/slizend /usr/local/bin/slizend
COPY --from=build /out/slizenctl /usr/local/bin/slizenctl
COPY slizen.example.toml /etc/slizen/slizen.toml

EXPOSE 6380 9090
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/slizend"]
CMD ["--config", "/etc/slizen/slizen.toml"]
