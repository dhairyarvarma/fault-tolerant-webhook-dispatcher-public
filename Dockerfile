FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD_PATH=./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -o /out/app ${CMD_PATH}

FROM alpine:3.24
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates
COPY --from=build /out/app /usr/local/bin/app
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/app"]
