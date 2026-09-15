FROM node:22-alpine AS builder
WORKDIR /app
RUN corepack enable pnpm
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
# Origin the reverse proxy forwards /api and /badge requests to — the backend
# Fly.io app's public URL (or its custom domain once DNS/certs are set up).
ENV BACKEND_ORIGIN=http://backend:8080
EXPOSE 80
