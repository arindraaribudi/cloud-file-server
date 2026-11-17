# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/workflow-server ./cmd/workflow-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/workflow-server /usr/local/bin/workflow-server
USER nonroot:nonroot
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/workflow-server"]
