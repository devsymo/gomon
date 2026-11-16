# syntax=docker/dockerfile:1

FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gomon ./cmd/gomon

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S gomon && adduser -S -G gomon -h /data gomon \
 && mkdir -p /data && chown gomon:gomon /data
COPY --from=build /out/gomon /usr/local/bin/gomon
RUN ln -s /usr/local/bin/gomon /usr/local/bin/xymon
USER gomon
ENV GOMON_DATA_DIR=/data
VOLUME /data
# 8080 web UI/API, 1984 Xymon plaintext, 1985 Xymon over TLS
EXPOSE 8080 1984 1985
ENTRYPOINT ["gomon"]
CMD ["serve"]
