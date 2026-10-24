FROM --platform=$BUILDPLATFORM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/podtorrent-agent ./cmd/podtorrent-agent

# Non-root, no shell, no package manager. Needs only outbound network access.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/podtorrent-agent /podtorrent-agent
USER nonroot:nonroot
ENTRYPOINT ["/podtorrent-agent"]
