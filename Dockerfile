FROM golang:1.26.10-alpine AS build
WORKDIR /src
COPY native/go.mod native/go.sum ./
RUN go mod download
COPY native/core ./core
COPY native/cmd/guoapp-web ./cmd/guoapp-web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /guoapp-web ./cmd/guoapp-web

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && mkdir -p /data/modules && chown -R 65532:65532 /data
COPY --from=build /guoapp-web /usr/local/bin/guoapp-web
USER 65532:65532
ENV GUOAPP_WEB_DATA=/data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/guoapp-web"]
