FROM node:22-alpine AS build
WORKDIR /workspace
COPY package.json package-lock.json tsconfig.base.json ./
COPY packages ./packages
COPY apps ./apps
# Configuration is deploy-time (window.__CONSOLE_CONFIG__ from a ConfigMap
# served as /config.js), not baked at build time — one image serves every
# environment. VITE_* values remain as a local-development fallback only.
RUN npm ci
RUN npm run build --workspace @seaplane/console-web

FROM nginxinc/nginx-unprivileged:stable-alpine
COPY apps/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY apps/web/security-headers.conf /etc/nginx/security-headers.conf
COPY --from=build /workspace/apps/web/dist /usr/share/nginx/html
EXPOSE 8080
