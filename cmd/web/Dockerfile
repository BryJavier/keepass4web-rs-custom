FROM golang:1.26.5-alpine AS test

WORKDIR /workspace

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

CMD ["/web"]
