# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/burrow ./cmd/burrow \
 && mkdir -p /out/data

# Static binary, no shell, no package manager. Runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/burrow /burrow
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV BURROW_ADDR=:8000 BURROW_DB=/data/burrow.db
VOLUME /data
EXPOSE 8000
ENTRYPOINT ["/burrow"]
