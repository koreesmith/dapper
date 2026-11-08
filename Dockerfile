FROM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY backend/ .
RUN CGO_ENABLED=0 go build -o /dapper ./cmd/dapper

FROM alpine:3.20
COPY --from=backend /dapper /usr/local/bin/dapper
COPY --from=web /web/dist /srv/web
EXPOSE 8080
ENV DAPPER_STATIC=/srv/web
ENTRYPOINT ["dapper"]
