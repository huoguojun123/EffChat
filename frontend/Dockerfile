FROM node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS builder

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
    --output /out/third-party

FROM nginx:1.29-alpine@sha256:5616878291a2eed594aee8db4dade5878cf7edcb475e59193904b198d9b830de

COPY frontend/nginx.conf /etc/nginx/templates/default.conf.template
COPY frontend/nginx-security-headers.conf /etc/nginx/conf.d/security-headers.conf
COPY frontend/docker-entrypoint.d/15-effchat-upload-limit.envsh /docker-entrypoint.d/15-effchat-upload-limit.envsh
RUN chmod +x /docker-entrypoint.d/15-effchat-upload-limit.envsh
COPY --from=builder /app/dist /usr/share/nginx/html
COPY --from=builder /out/third-party /usr/share/licenses/effchat/third-party/frontend
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/effchat/

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -Y off -qO- http://127.0.0.1/health >/dev/null || exit 1
