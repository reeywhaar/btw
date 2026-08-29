# syntax=docker/dockerfile:1

# The frontend, built on the machine doing the building rather than under emulation. The
# bundle is architecture-independent, so there is no reason to run npm through QEMU.
#
# Pinned to the major that developers here actually run, rather than to the current LTS.
# This stage produces static JavaScript and CSS, so the runtime characteristics that make
# LTS worth choosing do not apply to it — what does apply is that a bundle which builds on
# a laptop and fails in CI is an afternoon nobody gets back.
FROM --platform=$BUILDPLATFORM node:26-alpine AS web
WORKDIR /src
# The lockfile first, on its own layer, so a source edit does not reinstall the world.
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/tsconfig.json web/vite.config.ts ./
COPY web/index.html web/login.html ./
COPY web/src ./src
COPY web/public ./public
RUN npm run build
# An empty bundle is otherwise invisible until somebody loads the page and gets the
# placeholder, which looks like a server problem rather than a build one.
RUN test -s dist/index.html && test -s dist/login.html && test -s dist/sw.js

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal ./internal
ARG TARGETOS TARGETARCH VERSION=dev
# CGO off because modernc.org/sqlite is pure Go, which is what keeps this a static binary
# and the runtime image free of a toolchain.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X btw/internal/app.Version=$VERSION" -o /out/btw .

FROM alpine:3.22
# Push services are reached over HTTPS, so the certificate store is not optional.
RUN apk add --no-cache ca-certificates
COPY --from=build /out/btw /usr/local/bin/btw
COPY --from=web /src/dist /srv/web
ENV BTW_WEB_DIR=/srv/web BTW_DATA_DIR=/data
VOLUME /data
EXPOSE 80
# Runs the binary's own subcommand, so the image needs no HTTP client and a wedged process
# fails it.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["btw", "healthcheck"]
ENTRYPOINT ["btw"]
CMD ["serve"]
