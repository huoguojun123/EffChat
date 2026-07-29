FROM node:24-alpine AS builder

ARG NPM_CONFIG_REGISTRY=https://registry.npmjs.org
ENV NPM_CONFIG_REGISTRY=${NPM_CONFIG_REGISTRY}

WORKDIR /app

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --registry=${NPM_CONFIG_REGISTRY}

COPY frontend/ .
RUN npm run build

FROM nginx:1.29-alpine

COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY frontend/nginx-security-headers.conf /etc/nginx/conf.d/security-headers.conf
COPY --from=builder /app/dist /usr/share/nginx/html
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/effchat/

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -Y off -qO- http://127.0.0.1/health >/dev/null || exit 1
