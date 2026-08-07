FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

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
  && apk add --no-cache ca-certificates git

COPY backend/go.mod backend/go.sum ./
# go.mod replaces the Eino Claude adapter with this audited local module.
# Copy its module manifest before downloading dependencies so the cache stage
# remains valid for clean clones and exported source trees.
COPY backend/third_party/eino-claude/go.mod ./third_party/eino-claude/go.mod
RUN go mod download

COPY backend/ .
# -mod=readonly 要求 go.mod/go.sum 已经完整锁定依赖版本。
# 构建时如果源码引入了新依赖但没有先显式更新依赖锁文件，Go 会直接失败，
# 避免 Docker 构建阶段偷偷改动依赖图，保证发布镜像和 Git 中记录的版本一致。
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w -X github.com/huoguojun123/EffChat/pkg/config.BuildRef=${BUILD_REF}" -o /out/effchat-server ./cmd/server

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce

ARG ALPINE_MIRROR=https://dl-cdn.alpinelinux.org/alpine

# Git Skill SSRF protection relies on curloptResolve; fail the image build if a
# future Alpine/Git base image removes that transport capability.
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
  && apk add --no-cache ca-certificates tzdata wget su-exec git \
  && git help --config | grep -qx 'http.curloptResolve' \
  && addgroup -S app \
  && adduser -S app -G app

WORKDIR /app

COPY --from=builder /out/effchat-server /app/effchat-server
COPY backend/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/effchat/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
  && mkdir -p /app/storage/attachments/originals \
    /app/storage/attachments/extracted \
    /app/storage/attachments/ocr-staging \
    /app/storage/avatars \
    /app/storage/fonts \
    /app/storage/skills \
  && chown -R app:app /app

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -Y off -qO- http://127.0.0.1:8080/health >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/effchat-server"]
