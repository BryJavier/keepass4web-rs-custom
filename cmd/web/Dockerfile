FROM golang:1.26.5-alpine AS test

WORKDIR /workspace

RUN apk add --no-cache git

COPY . .

RUN go test ./...

FROM golang:1.26.5-alpine AS build

WORKDIR /workspace

COPY --from=test /workspace /workspace

RUN CGO_ENABLED=0 go build -o /out/web ./cmd/web

FROM alpine:3.22 AS runtime

COPY --from=build /out/web /web

EXPOSE 8080
USER 65532:65532

HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD ["wget", "-q", "-O", "-", "http://127.0.0.1:8080/healthz"]

CMD ["/web"]
