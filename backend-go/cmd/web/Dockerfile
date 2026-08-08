FROM golang:1.26.5-alpine AS test

WORKDIR /workspace

RUN apk add --no-cache git

COPY . .

# Compose policy tests require the host Docker resolver and run in operations CI.
RUN go test ./backend-go/cmd/web ./backend-go/internal/...

FROM golang:1.26.5-alpine AS build

WORKDIR /workspace

COPY --from=test /workspace /workspace

RUN CGO_ENABLED=0 go build -o /out/web ./backend-go/cmd/web

FROM node:20.20.2-alpine AS assets

WORKDIR /workspace

COPY package*.json ./
RUN npm ci

COPY frontend/web/static ./frontend/web/static
COPY frontend/web/tailwind.input.css ./frontend/web/tailwind.input.css
COPY frontend/templates ./frontend/templates
COPY backend-go/cmd/web ./backend-go/cmd/web
COPY backend-go/internal/web ./backend-go/internal/web
COPY tailwind.config.js ./
RUN npm run build:css && cp node_modules/htmx.org/dist/htmx.min.js frontend/web/static/htmx.min.js

FROM alpine:3.22 AS runtime

WORKDIR /app

COPY --from=build /out/web /app/web-server
COPY --from=assets /workspace/frontend/web/static /app/frontend/web/static
COPY frontend/templates /app/frontend/templates

EXPOSE 8080
USER 65532:65532

HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD ["wget", "-q", "-O", "-", "http://127.0.0.1:8080/healthz"]

CMD ["/app/web-server"]
