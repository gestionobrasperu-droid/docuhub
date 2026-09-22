# syntax=docker/dockerfile:1

# ---------------------------------------------------------------- frontend --
# Se compila primero porque su salida se incrusta en el binario de Go.
FROM node:20-alpine AS frontend
WORKDIR /app/frontend

COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund

COPY frontend/ ./
# vite.config.ts escribe en ../backend/web/dist
RUN npm run build

# -------------------------------------------------------------------- Go ---
FROM golang:1.22-alpine AS backend
WORKDIR /app/backend

COPY backend/go.mod backend/go.sum* ./
RUN go mod download || true

COPY backend/ ./
# El frontend compilado llega aquí para que go:embed lo encuentre.
COPY --from=frontend /app/backend/web/dist ./web/dist

RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /docuhub ./cmd/server

# --------------------------------------------------------------- runtime ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 docuhub
ENV TZ=America/Lima

COPY --from=backend /docuhub /usr/local/bin/docuhub

USER docuhub
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/docuhub"]
