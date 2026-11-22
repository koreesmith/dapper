# The web files and the Go program are built on the machine doing the build (BUILDPLATFORM) and cross-compiled, so
# building the arm64 image doesn't run Node or the Go compiler under emulation, which is slow and can hang.
FROM --platform=$BUILDPLATFORM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /dapper ./cmd/dapper

FROM alpine:3.20
ARG VERSION=dev
LABEL org.opencontainers.image.title="Dapper" \
      org.opencontainers.image.description="A web interface for managing LDAP directories, built for people who don't know LDAP" \
      org.opencontainers.image.source="https://github.com/koreesmith/dapper" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"
RUN adduser -D -u 10001 dapper && mkdir /data && chown dapper /data
COPY --from=backend /dapper /usr/local/bin/dapper
COPY --from=web /web/dist /srv/web
EXPOSE 8080
ENV DAPPER_STATIC=/srv/web DAPPER_DATA_DIR=/data
VOLUME /data
USER dapper
ENTRYPOINT ["dapper"]
