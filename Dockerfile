FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY web ./web
COPY assets ./assets
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /conselho ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -g 10001 conselho && adduser -D -u 10001 -G conselho conselho
WORKDIR /app
COPY --from=build /conselho /app/conselho
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/conselho"]
