# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ftp-server ./cmd/ftp-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ftp-server /usr/local/bin/ftp-server
USER nonroot:nonroot
EXPOSE 2121
# Passive data ports — see FTP_PASSIVE_PORT_RANGE in compose.
EXPOSE 50000-50999
ENTRYPOINT ["/usr/local/bin/ftp-server"]
