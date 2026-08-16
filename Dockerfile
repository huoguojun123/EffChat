ARG BUILDPLATFORM

FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine@sha256:af8d6740070b8906d12eae1c3e3ea0957fb63f492051ea05e354c38ef9fe88df AS backend-builder

ARG ALPINE_MIRROR=https://dl-cdn.alpinelinux.org/alpine
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG TARGETOS=linux
ARG TARGETARCH
ARG BUILD_REF=unknown
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

WORKDIR /src

RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
  && apk add --no-cache ca-certificates git python3

COPY backend/go.mod backend/go.sum ./
COPY backend/third_party/eino-claude/go.mod ./third_party/eino-claude/go.mod
RUN go mod download

COPY backend/ .
COPY scripts/licenses /license-tools
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath \
    -ldflags="-s -w -X github.com/huoguojun123/EffChat/pkg/config.BuildRef=${BUILD_REF}" \
    -o /out/effchat-server ./cmd/server \
  && GOOS=${TARGETOS} GOARCH=${TARGETARCH} python3 /license-tools/collect-third-party-licenses.py collect backend \
    --root /src \
    --output /out/third-party/backend

FROM node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS frontend-builder

ARG NPM_CONFIG_REGISTRY=https://registry.npmjs.org
ENV NPM_CONFIG_REGISTRY=${NPM_CONFIG_REGISTRY}

WORKDIR /app

RUN apk add --no-cache python3

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --registry=${NPM_CONFIG_REGISTRY}

COPY frontend/ .
COPY scripts/licenses /license-tools
RUN npm run build \
  && python3 /license-tools/collect-third-party-licenses.py collect frontend \
    --root /app \
    --output /out/third-party/frontend

FROM python:3.12-slim@sha256:646fb0bca3dd3ea1bcc6feb72c17ed16eed6e10cffc732fcc1478bd3e7f02d7b

ARG DEBIAN_FRONTEND=noninteractive

ENV PYTHONDONTWRITEBYTECODE=1 \
  PYTHONUNBUFFERED=1 \
  EXTRACTOR_HOST=0.0.0.0 \
  EXTRACTOR_PORT=8090

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates git tzdata \
  && git help --config | grep -qx 'http.curloptResolve' \
  && rm -rf /var/lib/apt/lists/*

RUN apt-get update \
  && apt-get install -y --no-install-recommends curl gettext-base nginx \
  && rm -rf /var/lib/apt/lists/*

RUN apt-get update \
  && apt-get install -y --no-install-recommends gosu postgresql-client \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --gid 10001 app \
  && useradd --uid 10001 --gid 10001 --no-create-home \
    --home-dir /nonexistent --shell /usr/sbin/nologin app

WORKDIR /app

COPY py-extractor/requirements.lock ./requirements.lock
RUN pip install --no-cache-dir --require-hashes -r requirements.lock

COPY py-extractor/app ./app
COPY backend/migrations ./migrations
COPY frontend/nginx.conf ./web/default.conf.template
COPY frontend/nginx-security-headers.conf /etc/nginx/conf.d/security-headers.conf
COPY frontend/docker-entrypoint.d/15-effchat-upload-limit.envsh ./web/15-effchat-upload-limit.envsh
COPY docker/entrypoint.sh /usr/local/bin/effchat-entrypoint
COPY --from=backend-builder /out/effchat-server ./effchat-server
COPY --from=backend-builder /out/third-party/backend /usr/share/licenses/effchat/third-party/backend
COPY --from=frontend-builder /app/dist /usr/share/nginx/html
COPY --from=frontend-builder /out/third-party/frontend /usr/share/licenses/effchat/third-party/frontend
COPY scripts/licenses /tmp/license-tools
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/effchat/

RUN python /tmp/license-tools/collect-third-party-licenses.py collect python \
    --root /app \
    --output /usr/share/licenses/effchat/third-party/python \
  && rm -rf /tmp/license-tools /etc/nginx/sites-enabled/default \
  && chmod +x /usr/local/bin/effchat-entrypoint /app/web/15-effchat-upload-limit.envsh /app/migrations/build_migration_script.sh \
  && mkdir -p \
    /app/storage/attachments/originals \
    /app/storage/attachments/extracted \
    /app/storage/attachments/ocr-staging \
    /app/storage/avatars \
    /app/storage/fonts \
    /app/storage/skills \
  && chown -R app:app /app/storage /app/app \
  && nginx -t

EXPOSE 80 8080 8090

ENTRYPOINT ["/usr/local/bin/effchat-entrypoint"]
CMD ["backend"]
