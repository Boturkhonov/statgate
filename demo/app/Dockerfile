FROM golang:1.25 AS builder

WORKDIR /app
COPY go.mod main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o demo-app .

FROM gcr.io/distroless/static:nonroot

ARG VERSION=v1

COPY --from=builder /app/demo-app /demo-app

ENV APP_VERSION=$VERSION

USER 65532:65532

ENTRYPOINT ["/demo-app"]
